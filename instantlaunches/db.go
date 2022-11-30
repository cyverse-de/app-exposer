package instantlaunches

import (
	"context"

	"github.com/lib/pq"
)

const fullListingQuery = `
SELECT
	il.id,
	ilu.username AS added_by,
	il.added_on,
	il.quick_launch_id,
	ql.name AS ql_name,
	ql.description AS ql_description,
	qlu.username AS ql_creator,
	sub.submission AS submission,
	ql.app_id,
	ql.app_version_id,
	ql.is_public,
	a.name AS app_name,
	a.description AS app_description,
	v.version AS app_version,
	v.deleted AS app_deleted,
	v.disabled AS app_disabled,
	iu.username as integrator


FROM instant_launches il
	JOIN quick_launches ql ON il.quick_launch_id = ql.id
	JOIN submissions sub ON ql.submission_id = sub.id
	JOIN apps a ON ql.app_id = a.id
	JOIN app_versions v ON ql.app_version_id = v.id
	JOIN integration_data integ ON v.integration_data_id = integ.id
	JOIN users iu ON integ.user_id = iu.id
	JOIN users qlu ON ql.creator = qlu.id
	JOIN users ilu ON il.added_by = ilu.id


WHERE il.id = any($1);
`

// ListFullInstantLaunchesByIDs returns the full instant launches associated with the UUIDs
// passed in. Includes quick launch, app, and submission info.
func (a *App) ListFullInstantLaunchesByIDs(ctx context.Context, ids []string) ([]FullInstantLaunch, error) {
	fullListing := []FullInstantLaunch{}
	err := a.DB.SelectContext(ctx, &fullListing, fullListingQuery, pq.Array(ids))
	return fullListing, err
}

const addInstantLaunchQuery = `
	INSERT INTO instant_launches (quick_launch_id, added_by)
	VALUES ( $1, ( SELECT u.id FROM users u WHERE u.username = $2 ) )
	RETURNING id, quick_launch_id, added_by, added_on;
`

// AddInstantLaunch registers a new instant launch in the database.
func (a *App) AddInstantLaunch(ctx context.Context, quickLaunchID, username string) (*InstantLaunch, error) {
	newvalues := &InstantLaunch{}
	err := a.DB.QueryRowxContext(ctx, addInstantLaunchQuery, quickLaunchID, username).StructScan(newvalues)
	return newvalues, err
}

const getInstantLaunchQuery = `
	SELECT i.id, i.quick_launch_id, i.added_by, i.added_on
	FROM instant_launches i
	WHERE i.id = $1;
`

// GetInstantLaunch returns a stored instant launch by ID.
func (a *App) GetInstantLaunch(ctx context.Context, id string) (*InstantLaunch, error) {
	il := &InstantLaunch{}
	err := a.DB.QueryRowxContext(ctx, getInstantLaunchQuery, id).StructScan(il)
	return il, err
}

const fullInstantLaunchQuery = `
SELECT
	il.id,
	ilu.username AS added_by,
	il.added_on,
	il.quick_launch_id,
	ql.name AS ql_name,
	ql.description AS ql_description,
	qlu.username AS ql_creator,
	sub.submission AS submission,
	ql.app_id,
	ql.app_version_id,
	ql.is_public,
	a.name AS app_name,
	a.description AS app_description,
	v.version AS app_version,
	v.deleted AS app_deleted,
	v.disabled AS app_disabled,
	iu.username as integrator


FROM instant_launches il
	JOIN quick_launches ql ON il.quick_launch_id = ql.id
	JOIN submissions sub ON ql.submission_id = sub.id
	JOIN apps a ON ql.app_id = a.id
	JOIN app_versions v ON ql.app_version_id = v.id
	JOIN integration_data integ ON v.integration_data_id = integ.id
	JOIN users iu ON integ.user_id = iu.id
	JOIN users qlu ON ql.creator = qlu.id
	JOIN users ilu ON il.added_by = ilu.id


WHERE il.id = $1;
`

// FullInstantLaunch returns an instant launch from the database that
// includes quick launch, app, and submission information.
func (a *App) FullInstantLaunch(ctx context.Context, id string) (*FullInstantLaunch, error) {
	fil := &FullInstantLaunch{}
	err := a.DB.QueryRowxContext(ctx, fullInstantLaunchQuery, id).StructScan(fil)
	return fil, err
}

const updateInstantLaunchQuery = `
	UPDATE ONLY instant_launches
	SET quick_launch_id = $1
	WHERE id = $2
	RETURNING id, quick_launch_id, added_by, added_by;
`

// UpdateInstantLaunch updates a stored instant launch with new values.
func (a *App) UpdateInstantLaunch(ctx context.Context, id, quickLaunchID string) (*InstantLaunch, error) {
	il := &InstantLaunch{}
	err := a.DB.QueryRowxContext(ctx, updateInstantLaunchQuery, quickLaunchID, id).StructScan(il)
	return il, err
}

const deleteInstantLaunchQuery = `
	DELETE FROM instant_launches WHERE id = $1;
`

// DeleteInstantLaunch deletes a stored instant launch.
func (a *App) DeleteInstantLaunch(ctx context.Context, id string) error {
	_, err := a.DB.ExecContext(ctx, deleteInstantLaunchQuery, id)
	return err
}

const listInstantLaunchesQuery = `
	SELECT i.id, i.quick_launch_id, i.added_by, i.added_on
	FROM instant_launches i;
`

// ListInstantLaunches lists all registered instant launches.
func (a *App) ListInstantLaunches(ctx context.Context) ([]InstantLaunch, error) {
	all := []InstantLaunch{}
	err := a.DB.SelectContext(ctx, &all, listInstantLaunchesQuery)
	return all, err
}

const fullListInstantLaunchesQuery = `
SELECT
	il.id,
	ilu.username AS added_by,
	il.added_on,
	il.quick_launch_id,
	ql.name AS ql_name,
	ql.description AS ql_description,
	qlu.username AS ql_creator,
	sub.submission AS submission,
	ql.app_id,
	ql.app_version_id,
	ql.is_public,
	a.name AS app_name,
	a.description AS app_description,
	v.version AS app_version,
	v.deleted AS app_deleted,
	v.disabled AS app_disabled,
	iu.username as integrator


FROM instant_launches il
	JOIN quick_launches ql ON il.quick_launch_id = ql.id
	JOIN submissions sub ON ql.submission_id = sub.id
	JOIN apps a ON ql.app_id = a.id
	JOIN app_versions v ON ql.app_version_id = v.id
	JOIN integration_data integ ON v.integration_data_id = integ.id
	JOIN users iu ON integ.user_id = iu.id
	JOIN users qlu ON ql.creator = qlu.id
	JOIN users ilu ON il.added_by = ilu.id
`

// FullListInstantLaunches returns a full listing of instant launches.
func (a *App) FullListInstantLaunches(ctx context.Context) ([]FullInstantLaunch, error) {
	all := []FullInstantLaunch{}
	err := a.DB.SelectContext(ctx, &all, fullListInstantLaunchesQuery)
	return all, err
}

const userMappingQuery = `
    SELECT u.id,
           u.version,
           u.instant_launches as mapping
      FROM user_instant_launches u
      JOIN users ON u.user_id = users.id
     WHERE users.username = $1
  ORDER BY u.version DESC
     LIMIT 1;
`

// UserMapping returns the user's instant launch mappings.
func (a *App) UserMapping(ctx context.Context, user string) (*UserInstantLaunchMapping, error) {
	m := &UserInstantLaunchMapping{}
	err := a.DB.GetContext(ctx, m, userMappingQuery, user)
	return m, err
}

const updateUserMappingQuery = `
    UPDATE ONLY user_instant_launches
            SET user_instant_launches.instant_launches = $1
           FROM users
          WHERE user_instant_launches.version = (
              SELECT max(u.version)
                FROM user_instant_launches u
          )
            AND user_id = users.id
            AND users.username = $2
          RETURNING user_instant_launches.instant_launches;
`

// UpdateUserMapping updates the the latest version of the user's custom
// instant launch mappings.
func (a *App) UpdateUserMapping(ctx context.Context, user string, update *InstantLaunchMapping) (*InstantLaunchMapping, error) {
	updated := &InstantLaunchMapping{}
	err := a.DB.QueryRowxContext(ctx, updateUserMappingQuery, update, user).Scan(updated)
	return updated, err
}

const deleteUserMappingQuery = `
	DELETE FROM ONLY user_instant_launches
	USING users
	WHERE user_instant_launches.user_id = users.id
	  AND users.username = $1
	  AND user_instant_launches.version = (
		  SELECT max(u.version)
		    FROM user_instant_launches u
	  );
`

// DeleteUserMapping is intended as an admin only operation that completely removes
// the latest mapping for the user.
func (a *App) DeleteUserMapping(ctx context.Context, user string) error {
	_, err := a.DB.ExecContext(ctx, deleteUserMappingQuery, user)
	return err
}

const createUserMappingQuery = `
	INSERT INTO user_instant_launches (instant_launches, user_id)
	VALUES ( $1, (SELECT id FROM users WHERE username = $2) )
	RETURNING instant_launches;
`

// AddUserMapping adds a new record to the database for the user's instant launches.
func (a *App) AddUserMapping(ctx context.Context, user string, mapping *InstantLaunchMapping) (*InstantLaunchMapping, error) {
	newvalue := &InstantLaunchMapping{}
	err := a.DB.QueryRowxContext(ctx, createUserMappingQuery, mapping, user).Scan(newvalue)
	if err != nil {
		return nil, err
	}
	return newvalue, nil
}

const allUserMappingsQuery = `
  SELECT u.id,
		 u.version,
		 u.user_id,
         u.instant_launches as mapping
    FROM user_instant_launches u
    JOIN users ON u.user_id = users.id
   WHERE users.username = ?;
`

// AllUserMappings returns all of the user's instant launch mappings regardless of version.
func (a *App) AllUserMappings(ctx context.Context, user string) ([]UserInstantLaunchMapping, error) {
	m := []UserInstantLaunchMapping{}
	err := a.DB.SelectContext(ctx, &m, allUserMappingsQuery, user)
	return m, err
}

const userMappingsByVersionQuery = `
    SELECT u.id,
           u.version,
           u.instant_launches as mapping
      FROM user_instant_launches u
      JOIN users ON u.user_id = users.id
     WHERE users.username = ?
       AND u.version = ?
`

// UserMappingsByVersion returns a specific version of the user's instant launch mappings.
func (a *App) UserMappingsByVersion(ctx context.Context, user string, version int) (UserInstantLaunchMapping, error) {
	m := UserInstantLaunchMapping{}
	err := a.DB.GetContext(ctx, &m, userMappingsByVersionQuery, user, version)
	return m, err
}

const updateUserMappingsByVersionQuery = `
    UPDATE ONLY user_instant_launches AS def
            SET def.instant_launches = jsonb_object(?)
           FROM users
          WHERE def.version = ?
            AND def.user_id = users.id
            AND users.username = ?
        RETURNING def.instant_launches;
`

// UpdateUserMappingsByVersion updates the user's instant launches for a specific version.
func (a *App) UpdateUserMappingsByVersion(ctx context.Context, user string, version int, update *InstantLaunchMapping) (*InstantLaunchMapping, error) {
	retval := &InstantLaunchMapping{}
	err := a.DB.QueryRowxContext(ctx, updateUserMappingsByVersionQuery, update, version, user).Scan(retval)
	if err != nil {
		return nil, err
	}
	return retval, nil
}

const deleteUserMappingsByVersionQuery = `
	DELETE FROM ONLY user_instant_launches AS def
	USING users
	WHERE def.user_id = users.id
	  AND users.username = ?
	  AND def.version = ?;
`

// DeleteUserMappingsByVersion deletes a user's instant launch mappings at a specific version.
func (a *App) DeleteUserMappingsByVersion(ctx context.Context, user string, version int) error {
	_, err := a.DB.ExecContext(ctx, deleteUserMappingsByVersionQuery, user, version)
	return err
}

const latestDefaultsQuery = `
    SELECT def.id,
           def.version,
           def.instant_launches AS mapping
      FROM default_instant_launches def
  ORDER BY def.version DESC
     LIMIT 1;
`

// LatestDefaults returns the latest version of the default instant launches.
func (a *App) LatestDefaults(ctx context.Context) (DefaultInstantLaunchMapping, error) {
	m := DefaultInstantLaunchMapping{}
	err := a.DB.GetContext(ctx, &m, latestDefaultsQuery)
	return m, err
}

const updateLatestDefaultsQuery = `
    UPDATE ONLY default_instant_launches
            SET instant_launches = $1
          WHERE version = (
              SELECT max(def.version)
                FROM default_instant_launches def
          )
          RETURNING instant_launches;
`

// UpdateLatestDefaults sets a new value for the latest version of the defaults.
func (a *App) UpdateLatestDefaults(ctx context.Context, newjson *InstantLaunchMapping) (*InstantLaunchMapping, error) {
	retval := &InstantLaunchMapping{}
	err := a.DB.QueryRowxContext(ctx, updateLatestDefaultsQuery, newjson).Scan(retval)
	return retval, err
}

const deleteLatestDefaultsQuery = `
	DELETE FROM ONLY default_instant_launches AS def
	WHERE version = (
		SELECT max(def.version)
		FROM default_instant_launches def
	);
`

// DeleteLatestDefaults removes the latest default mappings from the database.
func (a *App) DeleteLatestDefaults(ctx context.Context) error {
	_, err := a.DB.ExecContext(ctx, deleteLatestDefaultsQuery)
	return err
}

const createLatestDefaultsQuery = `
	INSERT INTO default_instant_launches (instant_launches, added_by)
	VALUES ( $1, ( SELECT u.id FROM users u WHERE username = $2 ) )
	RETURNING instant_launches;
`

// AddLatestDefaults adds a new version of the default instant launch mappings.
func (a *App) AddLatestDefaults(ctx context.Context, update *InstantLaunchMapping, addedBy string) (*InstantLaunchMapping, error) {
	newvalue := &InstantLaunchMapping{}
	err := a.DB.QueryRowxContext(ctx, createLatestDefaultsQuery, update, addedBy).Scan(newvalue)
	return newvalue, err
}

const defaultsByVersionQuery = `
    SELECT def.id,
           def.version,
           def.instant_launches as mapping
      FROM default_instant_launches def
     WHERE def.version = ?;
`

// DefaultsByVersion returns a specific version of the default instant launches.
func (a *App) DefaultsByVersion(ctx context.Context, version int) (*DefaultInstantLaunchMapping, error) {
	m := &DefaultInstantLaunchMapping{}
	err := a.DB.GetContext(ctx, m, defaultsByVersionQuery, version)
	return m, err
}

const updateDefaultsByVersionQuery = `
    UPDATE ONLY default_instant_launches AS def
            SET def.instant_launches = jsonb_object(?)
          WHERE def.version = ?
      RETURNING def.instant_launches;
`

// UpdateDefaultsByVersion updates the default mapping for a specific version.
func (a *App) UpdateDefaultsByVersion(ctx context.Context, newjson *InstantLaunchMapping, version int) (*InstantLaunchMapping, error) {
	updated := &InstantLaunchMapping{}
	err := a.DB.QueryRowxContext(ctx, updateDefaultsByVersionQuery, newjson, version).Scan(updated)
	return updated, err
}

const deleteDefaultsByVersionQuery = `
	DELETE FROM ONLY default_instant_launches as def
	WHERE def.version = ?;
`

// DeleteDefaultsByVersion removes a default instant launch mapping from the database
// based on its version.
func (a *App) DeleteDefaultsByVersion(ctx context.Context, version int) error {
	_, err := a.DB.ExecContext(ctx, deleteDefaultsByVersionQuery, version)
	return err
}

const listAllDefaultsQuery = `
    SELECT def.id,
           def.version,
           def.instant_launches as mapping
      FROM default_instant_launches def;
`

// ListAllDefaults returns a list of all of the default instant launches, including their version.
func (a *App) ListAllDefaults(ctx context.Context) (ListAllDefaultsResponse, error) {
	m := ListAllDefaultsResponse{Defaults: []DefaultInstantLaunchMapping{}}
	err := a.DB.SelectContext(ctx, &m.Defaults, listAllDefaultsQuery)
	return m, err
}

const listPublicQLsQuery = `
	SELECT ql.id,
		u.username as creator,
		ql.app_id,
		ql.app_version_id,
		ql.name,
		ql.description,
		ql.is_public,
		s.submission
	FROM quick_launches ql
	JOIN users u on ql.creator = u.id
	JOIN submissions s on ql.submission_id = s.id
	WHERE u.username = $1 OR ql.is_public = true
`

// ListViablePublicQuickLaunches returns a listing of quick launches that the user is permitted to run. This list
// includes quick launches that were created by the authenticated user and public quick launches.
func (a *App) ListViablePublicQuickLaunches(ctx context.Context, user string) ([]QuickLaunch, error) {
	l := []QuickLaunch{}
	err := a.DB.SelectContext(ctx, &l, listPublicQLsQuery, user)
	return l, err
}
