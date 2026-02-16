package vicetools

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ImportApp creates a new app, tool, and all related records from a VICEAppExport.
// All operations run in a single transaction; any failure rolls back everything.
func ImportApp(ctx context.Context, db *sqlx.DB, export *VICEAppExport) (*ImportResult, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := importAppTx(ctx, tx, export)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return result, nil
}

func importAppTx(ctx context.Context, tx *sqlx.Tx, export *VICEAppExport) (*ImportResult, error) {
	// 1. Look up or create integration_data for the tool
	toolIntDataID, err := lookupOrCreateIntegrationData(ctx, tx,
		export.Tool.IntegrationData.IntegratorEmail,
		export.Tool.IntegrationData.IntegratorName)
	if err != nil {
		return nil, fmt.Errorf("tool integration_data: %w", err)
	}

	// 2. Look up tool_types by name
	toolTypeID, err := lookupToolType(ctx, tx, export.Tool.Type)
	if err != nil {
		return nil, fmt.Errorf("looking up tool_type %q: %w", export.Tool.Type, err)
	}

	// 3. Find or create container_images
	imageID, err := findOrCreateContainerImage(ctx, tx,
		export.Tool.ContainerImage.Name,
		export.Tool.ContainerImage.Tag,
		export.Tool.ContainerImage.URL)
	if err != nil {
		return nil, fmt.Errorf("container_images: %w", err)
	}

	// 4. Find existing tool by name+version, or insert a new one
	toolID, err := findOrCreateTool(ctx, tx, export, toolTypeID, toolIntDataID, imageID)
	if err != nil {
		return nil, fmt.Errorf("tool: %w", err)
	}

	// 5. Insert container_settings only if the tool doesn't already have them
	hasSettings, err := toolHasSettings(ctx, tx, toolID)
	if err != nil {
		return nil, fmt.Errorf("checking container_settings: %w", err)
	}

	if !hasSettings {
		settingsID := uuid.New().String()
		cs := export.Tool.ContainerSettings
		_, err = tx.ExecContext(ctx, `
			INSERT INTO container_settings (id, tools_id, cpu_shares, memory_limit, min_memory_limit,
			                                min_cpu_cores, max_cpu_cores, min_gpus, max_gpus,
			                                min_disk_space, network_mode, working_directory,
			                                entrypoint, uid, skip_tmp_mount, pids_limit)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		`, settingsID, toolID,
			cs.CPUShares, cs.MemoryLimit, cs.MinMemoryLimit,
			cs.MinCPUCores, cs.MaxCPUCores, cs.MinGPUs, cs.MaxGPUs,
			cs.MinDiskSpace,
			nullIfEmpty(cs.NetworkMode),
			nullIfEmpty(cs.WorkingDirectory),
			nullIfEmpty(cs.EntryPoint),
			cs.UID, cs.SkipTmpMount, cs.PIDsLimit)
		if err != nil {
			return nil, fmt.Errorf("inserting container_settings: %w", err)
		}

		// Insert container_ports
		for _, p := range cs.Ports {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO container_ports (id, container_settings_id, host_port, container_port, bind_to_host)
				VALUES ($1, $2, $3, $4, $5)
			`, uuid.New().String(), settingsID, p.HostPort, p.ContainerPort, p.BindToHost)
			if err != nil {
				return nil, fmt.Errorf("inserting container_ports: %w", err)
			}
		}

		// Insert container_devices
		for _, d := range cs.Devices {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO container_devices (id, container_settings_id, host_path, container_path)
				VALUES ($1, $2, $3, $4)
			`, uuid.New().String(), settingsID, d.HostPath, d.ContainerPath)
			if err != nil {
				return nil, fmt.Errorf("inserting container_devices: %w", err)
			}
		}

		// Insert container_volumes
		for _, v := range cs.Volumes {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO container_volumes (id, container_settings_id, host_path, container_path)
				VALUES ($1, $2, $3, $4)
			`, uuid.New().String(), settingsID, v.HostPath, v.ContainerPath)
			if err != nil {
				return nil, fmt.Errorf("inserting container_volumes: %w", err)
			}
		}

		// Insert container_volumes_from (with data_containers)
		for _, vf := range cs.VolumesFrom {
			dcImageID, err := findOrCreateContainerImage(ctx, tx, vf.Name, vf.Tag, vf.URL)
			if err != nil {
				return nil, fmt.Errorf("volumes_from image: %w", err)
			}
			dcID := uuid.New().String()
			_, err = tx.ExecContext(ctx, `
				INSERT INTO data_containers (id, container_images_id, name_prefix, read_only)
				VALUES ($1, $2, $3, $4)
			`, dcID, dcImageID, nullIfEmpty(vf.NamePrefix), vf.ReadOnly)
			if err != nil {
				return nil, fmt.Errorf("inserting data_containers: %w", err)
			}
			_, err = tx.ExecContext(ctx, `
				INSERT INTO container_volumes_from (id, container_settings_id, data_containers_id)
				VALUES ($1, $2, $3)
			`, uuid.New().String(), settingsID, dcID)
			if err != nil {
				return nil, fmt.Errorf("inserting container_volumes_from: %w", err)
			}
		}

		// Insert interactive_apps_proxy_settings if present
		if cs.ProxySettings != nil {
			proxyID := uuid.New().String()
			ps := cs.ProxySettings
			_, err = tx.ExecContext(ctx, `
				INSERT INTO interactive_apps_proxy_settings
				       (id, image, name, frontend_url, cas_url, cas_validate, ssl_cert_path, ssl_key_path)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			`, proxyID,
				nullIfEmpty(ps.Image),
				nullIfEmpty(ps.Name),
				nullIfEmpty(ps.FrontendURL),
				nullIfEmpty(ps.CASURL),
				nullIfEmpty(ps.CASValidate),
				nullIfEmpty(ps.SSLCertPath),
				nullIfEmpty(ps.SSLKeyPath))
			if err != nil {
				return nil, fmt.Errorf("inserting proxy_settings: %w", err)
			}
			_, err = tx.ExecContext(ctx, `
				UPDATE container_settings
				   SET interactive_apps_proxy_settings_id = $1
				 WHERE id = $2
			`, proxyID, settingsID)
			if err != nil {
				return nil, fmt.Errorf("updating container_settings proxy ref: %w", err)
			}
		}
	}

	// 8. Look up or create integration_data for the app
	appIntDataID, err := lookupOrCreateIntegrationData(ctx, tx,
		export.App.IntegrationData.IntegratorEmail,
		export.App.IntegrationData.IntegratorName)
	if err != nil {
		return nil, fmt.Errorf("app integration_data: %w", err)
	}

	// 9. Insert apps
	appID := uuid.New().String()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO apps (id, name, description, wiki_url)
		VALUES ($1, $2, $3, $4)
	`, appID, export.App.Name, export.App.Description, nullIfEmpty(export.App.WikiURL))
	if err != nil {
		return nil, fmt.Errorf("inserting apps: %w", err)
	}

	// 10. Insert app_versions
	versionID := uuid.New().String()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO app_versions (id, app_id, version, integration_data_id, version_order, edited_date)
		VALUES ($1, $2, $3, $4, 0, NOW())
	`, versionID, appID, export.App.Version, appIntDataID)
	if err != nil {
		return nil, fmt.Errorf("inserting app_versions: %w", err)
	}

	// 11. Look up job_types by system_id matching the tool type
	jobTypeID, err := lookupJobType(ctx, tx, export.Tool.Type)
	if err != nil {
		return nil, fmt.Errorf("looking up job_type for %q: %w", export.Tool.Type, err)
	}

	// 12. Insert tasks
	taskID := uuid.New().String()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tasks (id, name, description, tool_id, job_type_id)
		VALUES ($1, $2, $3, $4, $5)
	`, taskID, export.Tool.Name, export.Tool.Description, toolID, jobTypeID)
	if err != nil {
		return nil, fmt.Errorf("inserting tasks: %w", err)
	}

	// 13. Insert app_steps
	_, err = tx.ExecContext(ctx, `
		INSERT INTO app_steps (id, app_version_id, task_id, step)
		VALUES ($1, $2, $3, 0)
	`, uuid.New().String(), versionID, taskID)
	if err != nil {
		return nil, fmt.Errorf("inserting app_steps: %w", err)
	}

	// 14. Insert parameter_groups and parameters
	for _, g := range export.App.ParameterGroups {
		groupID := uuid.New().String()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO parameter_groups (id, task_id, name, description, label, display_order, is_visible)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, groupID, taskID, g.Name,
			nullIfEmpty(g.Description),
			nullIfEmpty(g.Label),
			g.DisplayOrder, g.IsVisible)
		if err != nil {
			return nil, fmt.Errorf("inserting parameter_groups: %w", err)
		}

		for _, p := range g.Parameters {
			paramTypeID, err := lookupParameterType(ctx, tx, p.Type)
			if err != nil {
				return nil, fmt.Errorf("looking up parameter_type %q: %w", p.Type, err)
			}

			paramID := uuid.New().String()
			_, err = tx.ExecContext(ctx, `
				INSERT INTO parameters (id, parameter_group_id, name, label, description,
				                        parameter_type, ordering, required, is_visible, omit_if_blank)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			`, paramID, groupID,
				p.Name,
				nullIfEmpty(p.Label),
				nullIfEmpty(p.Description),
				paramTypeID,
				p.Ordering, p.Required, p.IsVisible, p.OmitIfBlank)
			if err != nil {
				return nil, fmt.Errorf("inserting parameters: %w", err)
			}

			// 15. Insert parameter_values
			for _, v := range p.Values {
				_, err = tx.ExecContext(ctx, `
					INSERT INTO parameter_values (id, parameter_id, name, value, description,
					                              label, is_default, display_order)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				`, uuid.New().String(), paramID,
					nullIfEmpty(v.Name),
					nullIfEmpty(v.Value),
					nullIfEmpty(v.Description),
					nullIfEmpty(v.Label),
					v.IsDefault, v.DisplayOrder)
				if err != nil {
					return nil, fmt.Errorf("inserting parameter_values: %w", err)
				}
			}

			// If there's a default value and no values with is_default, add one
			if p.DefaultValue != "" && !hasDefault(p.Values) {
				_, err = tx.ExecContext(ctx, `
					INSERT INTO parameter_values (id, parameter_id, value, is_default)
					VALUES ($1, $2, $3, true)
				`, uuid.New().String(), paramID, p.DefaultValue)
				if err != nil {
					return nil, fmt.Errorf("inserting default parameter_value: %w", err)
				}
			}
		}
	}

	// 16. Insert app_references
	for _, ref := range export.App.References {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO app_references (id, app_version_id, reference_text)
			VALUES ($1, $2, $3)
		`, uuid.New().String(), versionID, ref)
		if err != nil {
			return nil, fmt.Errorf("inserting app_references: %w", err)
		}
	}

	return &ImportResult{
		AppID:     appID,
		VersionID: versionID,
		ToolID:    toolID,
	}, nil
}

func hasDefault(values []ParameterValueDef) bool {
	for _, v := range values {
		if v.IsDefault {
			return true
		}
	}
	return false
}

func findOrCreateTool(ctx context.Context, tx *sqlx.Tx, export *VICEAppExport, toolTypeID, intDataID, imageID string) (string, error) {
	var id string
	err := tx.QueryRowxContext(ctx, `
		SELECT id FROM tools WHERE name = $1 AND version = $2 LIMIT 1
	`, export.Tool.Name, export.Tool.Version).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	id = uuid.New().String()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tools (id, name, description, version, location, attribution,
		                   interactive, time_limit_seconds, restricted,
		                   tool_type_id, integration_data_id, container_images_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, id,
		export.Tool.Name,
		export.Tool.Description,
		export.Tool.Version,
		nullIfEmpty(export.Tool.Location),
		nullIfEmpty(export.Tool.Attribution),
		export.Tool.Interactive,
		export.Tool.TimeLimitSeconds,
		export.Tool.Restricted,
		toolTypeID,
		intDataID,
		imageID)
	if err != nil {
		return "", err
	}
	return id, nil
}

func toolHasSettings(ctx context.Context, tx *sqlx.Tx, toolID string) (bool, error) {
	var count int
	err := tx.QueryRowxContext(ctx, `
		SELECT COUNT(*) FROM container_settings WHERE tools_id = $1
	`, toolID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func lookupOrCreateIntegrationData(ctx context.Context, tx *sqlx.Tx, email, name string) (string, error) {
	var id string
	err := tx.QueryRowxContext(ctx, `
		SELECT id FROM integration_data
		 WHERE integrator_email = $1
		 LIMIT 1
	`, email).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	id = uuid.New().String()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO integration_data (id, integrator_name, integrator_email)
		VALUES ($1, $2, $3)
	`, id, name, email)
	if err != nil {
		return "", err
	}
	return id, nil
}

func lookupToolType(ctx context.Context, tx *sqlx.Tx, name string) (string, error) {
	var id string
	err := tx.QueryRowxContext(ctx, `
		SELECT id FROM tool_types WHERE name = $1
	`, name).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("tool_type %q not found: %w", name, err)
	}
	return id, nil
}

func lookupJobType(ctx context.Context, tx *sqlx.Tx, toolTypeName string) (string, error) {
	// Job types and tool types are associated by system name.
	// If the tool type name is a valid system_id, use that; otherwise fall back to "de".
	var id string
	err := tx.QueryRowxContext(ctx, `
		SELECT id FROM job_types WHERE system_id = $1
	`, toolTypeName).Scan(&id)
	if err == nil {
		return id, nil
	}
	// Fall back to "de" system
	err = tx.QueryRowxContext(ctx, `
		SELECT id FROM job_types WHERE system_id = 'de'
	`).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("default job_type not found: %w", err)
	}
	return id, nil
}

func lookupParameterType(ctx context.Context, tx *sqlx.Tx, name string) (string, error) {
	var id string
	err := tx.QueryRowxContext(ctx, `
		SELECT id FROM parameter_types WHERE name = $1 AND deprecated = false
	`, name).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("parameter_type %q not found: %w", name, err)
	}
	return id, nil
}

func findOrCreateContainerImage(ctx context.Context, tx *sqlx.Tx, name, tag, url string) (string, error) {
	var id string
	err := tx.QueryRowxContext(ctx, `
		SELECT id FROM container_images
		 WHERE name = $1 AND tag = $2
		 LIMIT 1
	`, name, tag).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	id = uuid.New().String()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO container_images (id, name, tag, url)
		VALUES ($1, $2, $3, $4)
	`, id, name, tag, nullIfEmpty(url))
	if err != nil {
		return "", err
	}
	return id, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
