package vicetools

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// ExportApp queries the apps database and assembles a VICEAppExport for the given app UUID.
func ExportApp(ctx context.Context, db *sqlx.DB, appID string) (*VICEAppExport, error) {
	// 1. Get the app record
	var app struct {
		ID          string `db:"id"`
		Name        string `db:"name"`
		Description string `db:"description"`
		WikiURL     sql.NullString `db:"wiki_url"`
	}
	err := db.QueryRowxContext(ctx, `SELECT id, name, description, wiki_url FROM apps WHERE id = $1`, appID).StructScan(&app)
	if err != nil {
		return nil, fmt.Errorf("querying app: %w", err)
	}

	// 2. Get the latest non-deleted app_version
	var version struct {
		ID                string `db:"id"`
		Version           string `db:"version"`
		IntegrationDataID string `db:"integration_data_id"`
	}
	err = db.QueryRowxContext(ctx, `
		SELECT id, version, integration_data_id
		  FROM app_versions
		 WHERE app_id = $1
		   AND deleted = false
		 ORDER BY version_order DESC
		 LIMIT 1
	`, appID).StructScan(&version)
	if err != nil {
		return nil, fmt.Errorf("querying app_versions: %w", err)
	}

	// 3. Get integration_data for the version
	appIntData, err := getIntegrationData(ctx, db, version.IntegrationDataID)
	if err != nil {
		return nil, fmt.Errorf("querying app integration_data: %w", err)
	}

	// 4. Get app_steps -> tasks -> tools chain
	var stepInfo struct {
		StepID string `db:"step_id"`
		TaskID string `db:"task_id"`
		ToolID string `db:"tool_id"`
	}
	err = db.QueryRowxContext(ctx, `
		SELECT s.id AS step_id, t.id AS task_id, t.tool_id AS tool_id
		  FROM app_steps s
		  JOIN tasks t ON s.task_id = t.id
		 WHERE s.app_version_id = $1
		 ORDER BY s.step ASC
		 LIMIT 1
	`, version.ID).StructScan(&stepInfo)
	if err != nil {
		return nil, fmt.Errorf("querying app_steps/tasks: %w", err)
	}

	// 5. Get tool details + tool_type name
	var tool struct {
		ID                string         `db:"id"`
		Name              string         `db:"name"`
		Description       string         `db:"description"`
		Version           string         `db:"version"`
		Location          sql.NullString `db:"location"`
		Attribution       sql.NullString `db:"attribution"`
		Interactive       bool           `db:"interactive"`
		TimeLimitSeconds  int            `db:"time_limit_seconds"`
		Restricted        bool           `db:"restricted"`
		ToolTypeName      string         `db:"tool_type_name"`
		ContainerImagesID string         `db:"container_images_id"`
		IntegrationDataID string         `db:"integration_data_id"`
	}
	err = db.QueryRowxContext(ctx, `
		SELECT t.id, t.name, t.description, t.version,
		       t.location, t.attribution, t.interactive,
		       t.time_limit_seconds, t.restricted,
		       tt.name AS tool_type_name,
		       t.container_images_id,
		       t.integration_data_id
		  FROM tools t
		  JOIN tool_types tt ON t.tool_type_id = tt.id
		 WHERE t.id = $1
	`, stepInfo.ToolID).StructScan(&tool)
	if err != nil {
		return nil, fmt.Errorf("querying tool: %w", err)
	}

	// 6. Get container_images
	var image struct {
		Name         string         `db:"name"`
		Tag          string         `db:"tag"`
		URL          sql.NullString `db:"url"`
		OSGImagePath sql.NullString `db:"osg_image_path"`
	}
	err = db.QueryRowxContext(ctx, `
		SELECT name, tag, url, osg_image_path
		  FROM container_images
		 WHERE id = $1
	`, tool.ContainerImagesID).StructScan(&image)
	if err != nil {
		return nil, fmt.Errorf("querying container_images: %w", err)
	}

	// 7. Get container_settings
	var settings struct {
		ID               string          `db:"id"`
		CPUShares        sql.NullInt64   `db:"cpu_shares"`
		MemoryLimit      sql.NullInt64   `db:"memory_limit"`
		MinMemoryLimit   sql.NullInt64   `db:"min_memory_limit"`
		MinCPUCores      sql.NullFloat64 `db:"min_cpu_cores"`
		MaxCPUCores      sql.NullFloat64 `db:"max_cpu_cores"`
		MinGPUs          sql.NullInt64   `db:"min_gpus"`
		MaxGPUs          sql.NullInt64   `db:"max_gpus"`
		MinDiskSpace     sql.NullInt64   `db:"min_disk_space"`
		NetworkMode      sql.NullString  `db:"network_mode"`
		WorkingDirectory sql.NullString  `db:"working_directory"`
		EntryPoint       sql.NullString  `db:"entrypoint"`
		UID              sql.NullInt32   `db:"uid"`
		SkipTmpMount     sql.NullBool    `db:"skip_tmp_mount"`
		PIDsLimit        sql.NullInt64   `db:"pids_limit"`
		ProxySettingsID  sql.NullString  `db:"interactive_apps_proxy_settings_id"`
	}
	err = db.QueryRowxContext(ctx, `
		SELECT id, cpu_shares, memory_limit, min_memory_limit,
		       min_cpu_cores, max_cpu_cores, min_gpus, max_gpus, min_disk_space,
		       network_mode, working_directory, entrypoint, uid,
		       skip_tmp_mount, pids_limit,
		       interactive_apps_proxy_settings_id
		  FROM container_settings
		 WHERE tools_id = $1
	`, stepInfo.ToolID).StructScan(&settings)
	if err != nil {
		return nil, fmt.Errorf("querying container_settings: %w", err)
	}

	// 8. Get container_ports
	ports, err := getContainerPorts(ctx, db, settings.ID)
	if err != nil {
		return nil, fmt.Errorf("querying container_ports: %w", err)
	}

	// Get container_devices
	devices, err := getContainerDevices(ctx, db, settings.ID)
	if err != nil {
		return nil, fmt.Errorf("querying container_devices: %w", err)
	}

	// Get container_volumes
	volumes, err := getContainerVolumes(ctx, db, settings.ID)
	if err != nil {
		return nil, fmt.Errorf("querying container_volumes: %w", err)
	}

	// Get container_volumes_from
	volumesFrom, err := getContainerVolumesFrom(ctx, db, settings.ID)
	if err != nil {
		return nil, fmt.Errorf("querying container_volumes_from: %w", err)
	}

	// 9. Get interactive_apps_proxy_settings
	var proxySettings *ProxySettingsDef
	if settings.ProxySettingsID.Valid {
		ps, err := getProxySettings(ctx, db, settings.ProxySettingsID.String)
		if err != nil {
			return nil, fmt.Errorf("querying proxy_settings: %w", err)
		}
		proxySettings = ps
	}

	// 10. Get parameter_groups for the task
	paramGroups, err := getParameterGroups(ctx, db, stepInfo.TaskID)
	if err != nil {
		return nil, fmt.Errorf("querying parameter_groups: %w", err)
	}

	// 11. Get app_references
	references, err := getAppReferences(ctx, db, version.ID)
	if err != nil {
		return nil, fmt.Errorf("querying app_references: %w", err)
	}

	// Get tool integration data
	toolIntData, err := getIntegrationData(ctx, db, tool.IntegrationDataID)
	if err != nil {
		return nil, fmt.Errorf("querying tool integration_data: %w", err)
	}

	export := &VICEAppExport{
		ExportVersion: "1.0",
		ExportDate:    time.Now().UTC(),
		SourceAppID:   appID,
		App: AppDefinition{
			Name:        app.Name,
			Description: app.Description,
			WikiURL:     app.WikiURL.String,
			Version:     version.Version,
			IntegrationData: *appIntData,
			ParameterGroups: paramGroups,
			References:      references,
		},
		Tool: ToolDefinition{
			Name:             tool.Name,
			Description:      tool.Description,
			Version:          tool.Version,
			Type:             tool.ToolTypeName,
			Interactive:      tool.Interactive,
			TimeLimitSeconds: tool.TimeLimitSeconds,
			Restricted:       tool.Restricted,
			Location:         tool.Location.String,
			Attribution:      tool.Attribution.String,
			IntegrationData:  *toolIntData,
			ContainerImage: ContainerImageDef{
				Name:         image.Name,
				Tag:          image.Tag,
				URL:          image.URL.String,
				OSGImagePath: image.OSGImagePath.String,
			},
			ContainerSettings: ContainerSettingsDef{
				CPUShares:        settings.CPUShares.Int64,
				MemoryLimit:      settings.MemoryLimit.Int64,
				MinMemoryLimit:   settings.MinMemoryLimit.Int64,
				MinCPUCores:      settings.MinCPUCores.Float64,
				MaxCPUCores:      settings.MaxCPUCores.Float64,
				MinGPUs:          settings.MinGPUs.Int64,
				MaxGPUs:          settings.MaxGPUs.Int64,
				MinDiskSpace:     settings.MinDiskSpace.Int64,
				NetworkMode:      settings.NetworkMode.String,
				WorkingDirectory: settings.WorkingDirectory.String,
				EntryPoint:       settings.EntryPoint.String,
				UID:              int(settings.UID.Int32),
				SkipTmpMount:     settings.SkipTmpMount.Bool,
				PIDsLimit:        settings.PIDsLimit.Int64,
				Ports:            ports,
				Devices:          devices,
				Volumes:          volumes,
				VolumesFrom:      volumesFrom,
				ProxySettings:    proxySettings,
			},
		},
	}

	return export, nil
}

func getIntegrationData(ctx context.Context, db *sqlx.DB, id string) (*IntegrationDataDef, error) {
	var data struct {
		IntegratorName  string `db:"integrator_name"`
		IntegratorEmail string `db:"integrator_email"`
	}
	err := db.QueryRowxContext(ctx, `
		SELECT integrator_name, integrator_email
		  FROM integration_data
		 WHERE id = $1
	`, id).StructScan(&data)
	if err != nil {
		return nil, err
	}
	return &IntegrationDataDef{
		IntegratorName:  data.IntegratorName,
		IntegratorEmail: data.IntegratorEmail,
	}, nil
}

func getContainerPorts(ctx context.Context, db *sqlx.DB, settingsID string) ([]PortDef, error) {
	rows, err := db.QueryxContext(ctx, `
		SELECT host_port, container_port, bind_to_host
		  FROM container_ports
		 WHERE container_settings_id = $1
	`, settingsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ports []PortDef
	for rows.Next() {
		var p struct {
			HostPort      sql.NullInt32 `db:"host_port"`
			ContainerPort sql.NullInt32 `db:"container_port"`
			BindToHost    sql.NullBool  `db:"bind_to_host"`
		}
		if err := rows.StructScan(&p); err != nil {
			return nil, err
		}
		ports = append(ports, PortDef{
			HostPort:      int(p.HostPort.Int32),
			ContainerPort: int(p.ContainerPort.Int32),
			BindToHost:    p.BindToHost.Bool,
		})
	}
	return ports, rows.Err()
}

func getContainerDevices(ctx context.Context, db *sqlx.DB, settingsID string) ([]DeviceDef, error) {
	rows, err := db.QueryxContext(ctx, `
		SELECT host_path, container_path
		  FROM container_devices
		 WHERE container_settings_id = $1
	`, settingsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []DeviceDef
	for rows.Next() {
		var d struct {
			HostPath      string `db:"host_path"`
			ContainerPath string `db:"container_path"`
		}
		if err := rows.StructScan(&d); err != nil {
			return nil, err
		}
		devices = append(devices, DeviceDef{
			HostPath:      d.HostPath,
			ContainerPath: d.ContainerPath,
		})
	}
	return devices, rows.Err()
}

func getContainerVolumes(ctx context.Context, db *sqlx.DB, settingsID string) ([]VolumeDef, error) {
	rows, err := db.QueryxContext(ctx, `
		SELECT host_path, container_path
		  FROM container_volumes
		 WHERE container_settings_id = $1
	`, settingsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var volumes []VolumeDef
	for rows.Next() {
		var v struct {
			HostPath      string `db:"host_path"`
			ContainerPath string `db:"container_path"`
		}
		if err := rows.StructScan(&v); err != nil {
			return nil, err
		}
		volumes = append(volumes, VolumeDef{
			HostPath:      v.HostPath,
			ContainerPath: v.ContainerPath,
		})
	}
	return volumes, rows.Err()
}

func getContainerVolumesFrom(ctx context.Context, db *sqlx.DB, settingsID string) ([]VolumesFromDef, error) {
	rows, err := db.QueryxContext(ctx, `
		SELECT ci.name, ci.tag, ci.url,
		       dc.name_prefix, dc.read_only
		  FROM container_volumes_from cvf
		  JOIN data_containers dc ON cvf.data_containers_id = dc.id
		  JOIN container_images ci ON dc.container_images_id = ci.id
		 WHERE cvf.container_settings_id = $1
	`, settingsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vfs []VolumesFromDef
	for rows.Next() {
		var v struct {
			Name       string         `db:"name"`
			Tag        string         `db:"tag"`
			URL        sql.NullString `db:"url"`
			NamePrefix sql.NullString `db:"name_prefix"`
			ReadOnly   bool           `db:"read_only"`
		}
		if err := rows.StructScan(&v); err != nil {
			return nil, err
		}
		vfs = append(vfs, VolumesFromDef{
			Name:       v.Name,
			Tag:        v.Tag,
			URL:        v.URL.String,
			NamePrefix: v.NamePrefix.String,
			ReadOnly:   v.ReadOnly,
		})
	}
	return vfs, rows.Err()
}

func getProxySettings(ctx context.Context, db *sqlx.DB, id string) (*ProxySettingsDef, error) {
	var ps struct {
		Image       sql.NullString `db:"image"`
		Name        sql.NullString `db:"name"`
		FrontendURL sql.NullString `db:"frontend_url"`
		CASURL      sql.NullString `db:"cas_url"`
		CASValidate sql.NullString `db:"cas_validate"`
		SSLCertPath sql.NullString `db:"ssl_cert_path"`
		SSLKeyPath  sql.NullString `db:"ssl_key_path"`
	}
	err := db.QueryRowxContext(ctx, `
		SELECT image, name, frontend_url, cas_url, cas_validate,
		       ssl_cert_path, ssl_key_path
		  FROM interactive_apps_proxy_settings
		 WHERE id = $1
	`, id).StructScan(&ps)
	if err != nil {
		return nil, err
	}
	return &ProxySettingsDef{
		Image:       ps.Image.String,
		Name:        ps.Name.String,
		FrontendURL: ps.FrontendURL.String,
		CASURL:      ps.CASURL.String,
		CASValidate: ps.CASValidate.String,
		SSLCertPath: ps.SSLCertPath.String,
		SSLKeyPath:  ps.SSLKeyPath.String,
	}, nil
}

func getParameterGroups(ctx context.Context, db *sqlx.DB, taskID string) ([]ParameterGroupDef, error) {
	rows, err := db.QueryxContext(ctx, `
		SELECT id, name, description, label, display_order, is_visible
		  FROM parameter_groups
		 WHERE task_id = $1
		 ORDER BY display_order ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []ParameterGroupDef
	for rows.Next() {
		var g struct {
			ID           string        `db:"id"`
			Name         sql.NullString `db:"name"`
			Description  sql.NullString `db:"description"`
			Label        sql.NullString `db:"label"`
			DisplayOrder sql.NullInt32  `db:"display_order"`
			IsVisible    sql.NullBool   `db:"is_visible"`
		}
		if err := rows.StructScan(&g); err != nil {
			return nil, err
		}

		params, err := getParameters(ctx, db, g.ID)
		if err != nil {
			return nil, fmt.Errorf("querying parameters for group %s: %w", g.ID, err)
		}

		groups = append(groups, ParameterGroupDef{
			Name:         g.Name.String,
			Description:  g.Description.String,
			Label:        g.Label.String,
			DisplayOrder: int(g.DisplayOrder.Int32),
			IsVisible:    g.IsVisible.Bool,
			Parameters:   params,
		})
	}
	return groups, rows.Err()
}

func getParameters(ctx context.Context, db *sqlx.DB, groupID string) ([]ParameterDef, error) {
	rows, err := db.QueryxContext(ctx, `
		SELECT p.id, p.name, p.label, p.description,
		       pt.name AS type_name,
		       p.ordering, p.required, p.is_visible, p.omit_if_blank
		  FROM parameters p
		  JOIN parameter_types pt ON p.parameter_type = pt.id
		 WHERE p.parameter_group_id = $1
		 ORDER BY p.ordering ASC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var params []ParameterDef
	for rows.Next() {
		var p struct {
			ID          string         `db:"id"`
			Name        sql.NullString `db:"name"`
			Label       sql.NullString `db:"label"`
			Description sql.NullString `db:"description"`
			TypeName    string         `db:"type_name"`
			Ordering    sql.NullInt32  `db:"ordering"`
			Required    sql.NullBool   `db:"required"`
			IsVisible   sql.NullBool   `db:"is_visible"`
			OmitIfBlank sql.NullBool   `db:"omit_if_blank"`
		}
		if err := rows.StructScan(&p); err != nil {
			return nil, err
		}

		values, err := getParameterValues(ctx, db, p.ID)
		if err != nil {
			return nil, fmt.Errorf("querying values for param %s: %w", p.ID, err)
		}

		// Get default value
		defaultValue := ""
		for _, v := range values {
			if v.IsDefault {
				defaultValue = v.Value
				break
			}
		}

		params = append(params, ParameterDef{
			Name:         p.Name.String,
			Label:        p.Label.String,
			Description:  p.Description.String,
			Type:         p.TypeName,
			Ordering:     int(p.Ordering.Int32),
			Required:     p.Required.Bool,
			IsVisible:    p.IsVisible.Bool,
			OmitIfBlank:  p.OmitIfBlank.Bool,
			DefaultValue: defaultValue,
			Values:       values,
		})
	}
	return params, rows.Err()
}

func getParameterValues(ctx context.Context, db *sqlx.DB, paramID string) ([]ParameterValueDef, error) {
	rows, err := db.QueryxContext(ctx, `
		SELECT id, name, value, description, label, is_default, display_order
		  FROM parameter_values
		 WHERE parameter_id = $1
		 ORDER BY display_order ASC
	`, paramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []ParameterValueDef
	for rows.Next() {
		var v struct {
			ID           string         `db:"id"`
			Name         sql.NullString `db:"name"`
			Value        sql.NullString `db:"value"`
			Description  sql.NullString `db:"description"`
			Label        sql.NullString `db:"label"`
			IsDefault    sql.NullBool   `db:"is_default"`
			DisplayOrder sql.NullInt32  `db:"display_order"`
		}
		if err := rows.StructScan(&v); err != nil {
			return nil, err
		}
		values = append(values, ParameterValueDef{
			Name:         v.Name.String,
			Value:        v.Value.String,
			Description:  v.Description.String,
			Label:        v.Label.String,
			IsDefault:    v.IsDefault.Bool,
			DisplayOrder: int(v.DisplayOrder.Int32),
		})
	}
	return values, rows.Err()
}

func getAppReferences(ctx context.Context, db *sqlx.DB, versionID string) ([]string, error) {
	rows, err := db.QueryxContext(ctx, `
		SELECT reference_text
		  FROM app_references
		 WHERE app_version_id = $1
	`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
