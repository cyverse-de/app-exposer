package instantlaunches

import (
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
	ql.is_public,
	a.name AS app_name,
	a.description AS app_description,
	a.deleted AS app_deleted,
	a.disabled AS app_disabled,
	iu.username as integrator


FROM instant_launches il
	JOIN quick_launches ql ON il.quick_launch_id = ql.id
	JOIN submissions sub ON ql.submission_id = sub.id
	JOIN apps a ON ql.app_id = a.id
	JOIN integration_data integ ON a.integration_data_id = integ.id
	JOIN users iu ON integ.user_id = iu.id
	JOIN users qlu ON ql.creator = qlu.id
	JOIN users ilu ON il.added_by = ilu.id


WHERE il.id = any($1);
`

// ListFullInstantLaunchesByIDs returns the full instant launches associated with the UUIDs
// passed in. Includes quick launch, app, and submission info.
func (a *App) ListFullInstantLaunchesByIDs(ids []string) ([]FullInstantLaunch, error) {
	fullListing := []FullInstantLaunch{}
	err := a.DB.Select(&fullListing, fullListingQuery, pq.Array(ids))
	return fullListing, err
}

const addInstantLaunchQuery = `
	INSERT INTO instant_launches (quick_launch_id, added_by)
	VALUES ( $1, ( SELECT u.id FROM users u WHERE u.username = $2 ) )
	RETURNING id, quick_launch_id, added_by, added_on;
`

// AddInstantLaunch registers a new instant launch in the database.
func (a *App) AddInstantLaunch(quickLaunchID, username string) (*InstantLaunch, error) {
	newvalues := &InstantLaunch{}
	err := a.DB.QueryRowx(addInstantLaunchQuery, quickLaunchID, username).StructScan(newvalues)
	return newvalues, err
}

const getInstantLaunchQuery = `
	SELECT i.id, i.quick_launch_id, i.added_by, i.added_on
	FROM instant_launches i
	WHERE i.id = $1;
`

// GetInstantLaunch returns a stored instant launch by ID.
func (a *App) GetInstantLaunch(id string) (*InstantLaunch, error) {
	il := &InstantLaunch{}
	err := a.DB.QueryRowx(getInstantLaunchQuery, id).StructScan(il)
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
	ql.is_public,
	a.name AS app_name,
	a.description AS app_description,
	a.deleted AS app_deleted,
	a.disabled AS app_disabled,
	iu.username as integrator


FROM instant_launches il
	JOIN quick_launches ql ON il.quick_launch_id = ql.id
	JOIN submissions sub ON ql.submission_id = sub.id
	JOIN apps a ON ql.app_id = a.id
	JOIN integration_data integ ON a.integration_data_id = integ.id
	JOIN users iu ON integ.user_id = iu.id
	JOIN users qlu ON ql.creator = qlu.id
	JOIN users ilu ON il.added_by = ilu.id


WHERE il.id = $1;
`

// FullInstantLaunch returns an instant launch from the database that
// includes quick launch, app, and submission information.
func (a *App) FullInstantLaunch(id string) (*FullInstantLaunch, error) {
	fil := &FullInstantLaunch{}
	err := a.DB.QueryRowx(fullInstantLaunchQuery, id).StructScan(fil)
	return fil, err
}

const updateInstantLaunchQuery = `
	UPDATE ONLY instant_launches
	SET quick_launch_id = $1
	WHERE id = $2
	RETURNING id, quick_launch_id, added_by, added_by;
`

// UpdateInstantLaunch updates a stored instant launch with new values.
func (a *App) UpdateInstantLaunch(id, quickLaunchID string) (*InstantLaunch, error) {
	il := &InstantLaunch{}
	err := a.DB.QueryRowx(updateInstantLaunchQuery, quickLaunchID, id).StructScan(il)
	return il, err
}

const deleteInstantLaunchQuery = `
	DELETE FROM instant_launches WHERE id = $1;
`

// DeleteInstantLaunch deletes a stored instant launch.
func (a *App) DeleteInstantLaunch(id string) error {
	_, err := a.DB.Exec(deleteInstantLaunchQuery, id)
	return err
}

const listInstantLaunchesQuery = `
	SELECT i.id, i.quick_launch_id, i.added_by, i.added_on
	FROM instant_launches i;
`

// ListInstantLaunches lists all registered instant launches.
func (a *App) ListInstantLaunches() ([]InstantLaunch, error) {
	all := []InstantLaunch{}
	err := a.DB.Select(&all, listInstantLaunchesQuery)
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
	ql.is_public,
	a.name AS app_name,
	a.description AS app_description,
	a.deleted AS app_deleted,
	a.disabled AS app_disabled,
	iu.username as integrator


FROM instant_launches il
	JOIN quick_launches ql ON il.quick_launch_id = ql.id
	JOIN submissions sub ON ql.submission_id = sub.id
	JOIN apps a ON ql.app_id = a.id
	JOIN integration_data integ ON a.integration_data_id = integ.id
	JOIN users iu ON integ.user_id = iu.id
	JOIN users qlu ON ql.creator = qlu.id
	JOIN users ilu ON il.added_by = ilu.id
`

func (a *App) FullListInstantLaunches() ([]FullInstantLaunch, error) {
	all := []FullInstantLaunch{}
	err := a.DB.Select(&all, fullListInstantLaunchesQuery)
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
func (a *App) UserMapping(user string) (*UserInstantLaunchMapping, error) {
	m := &UserInstantLaunchMapping{}
	err := a.DB.Get(m, userMappingQuery, user)
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
func (a *App) UpdateUserMapping(user string, update *InstantLaunchMapping) (*InstantLaunchMapping, error) {
	updated := &InstantLaunchMapping{}
	err := a.DB.QueryRowx(updateUserMappingQuery, update, user).Scan(updated)
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
func (a *App) DeleteUserMapping(user string) error {
	_, err := a.DB.Exec(deleteUserMappingQuery, user)
	return err
}

const createUserMappingQuery = `
	INSERT INTO user_instant_launches (instant_launches, user_id)
	VALUES ( $1, (SELECT id FROM users WHERE username = $2) )
	RETURNING instant_launches;
`

// AddUserMapping adds a new record to the database for the user's instant launches.
func (a *App) AddUserMapping(user string, mapping *InstantLaunchMapping) (*InstantLaunchMapping, error) {
	newvalue := &InstantLaunchMapping{}
	err := a.DB.QueryRowx(createUserMappingQuery, mapping, user).Scan(newvalue)
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
func (a *App) AllUserMappings(user string) ([]UserInstantLaunchMapping, error) {
	m := []UserInstantLaunchMapping{}
	err := a.DB.Select(&m, allUserMappingsQuery, user)
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
func (a *App) UserMappingsByVersion(user string, version int) (UserInstantLaunchMapping, error) {
	m := UserInstantLaunchMapping{}
	err := a.DB.Get(&m, userMappingsByVersionQuery, user, version)
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
func (a *App) UpdateUserMappingsByVersion(user string, version int, update *InstantLaunchMapping) (*InstantLaunchMapping, error) {
	retval := &InstantLaunchMapping{}
	err := a.DB.QueryRowx(updateUserMappingsByVersionQuery, update, version, user).Scan(retval)
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
func (a *App) DeleteUserMappingsByVersion(user string, version int) error {
	_, err := a.DB.Exec(deleteUserMappingsByVersionQuery, user, version)
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
func (a *App) LatestDefaults() (DefaultInstantLaunchMapping, error) {
	m := DefaultInstantLaunchMapping{}
	err := a.DB.Get(&m, latestDefaultsQuery)
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
func (a *App) UpdateLatestDefaults(newjson *InstantLaunchMapping) (*InstantLaunchMapping, error) {
	retval := &InstantLaunchMapping{}
	err := a.DB.QueryRowx(updateLatestDefaultsQuery, newjson).Scan(retval)
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
func (a *App) DeleteLatestDefaults() error {
	_, err := a.DB.Exec(deleteLatestDefaultsQuery)
	return err
}

const createLatestDefaultsQuery = `
	INSERT INTO default_instant_launches (instant_launches, added_by)
	VALUES ( $1, ( SELECT u.id FROM users u WHERE username = $2 ) )
	RETURNING instant_launches;
`

// AddLatestDefaults adds a new version of the default instant launch mappings.
func (a *App) AddLatestDefaults(update *InstantLaunchMapping, addedBy string) (*InstantLaunchMapping, error) {
	newvalue := &InstantLaunchMapping{}
	err := a.DB.QueryRowx(createLatestDefaultsQuery, update, addedBy).Scan(newvalue)
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
func (a *App) DefaultsByVersion(version int) (*DefaultInstantLaunchMapping, error) {
	m := &DefaultInstantLaunchMapping{}
	err := a.DB.Get(m, defaultsByVersionQuery, version)
	return m, err
}

const updateDefaultsByVersionQuery = `
    UPDATE ONLY default_instant_launches AS def
            SET def.instant_launches = jsonb_object(?)
          WHERE def.version = ?
      RETURNING def.instant_launches;
`

// UpdateDefaultsByVersion updates the default mapping for a specific version.
func (a *App) UpdateDefaultsByVersion(newjson *InstantLaunchMapping, version int) (*InstantLaunchMapping, error) {
	updated := &InstantLaunchMapping{}
	err := a.DB.QueryRowx(updateDefaultsByVersionQuery, newjson, version).Scan(updated)
	return updated, err
}

const deleteDefaultsByVersionQuery = `
	DELETE FROM ONLY default_instant_launches as def
	WHERE def.version = ?;
`

// DeleteDefaultsByVersion removes a default instant launch mapping from the database
// based on its version.
func (a *App) DeleteDefaultsByVersion(version int) error {
	_, err := a.DB.Exec(deleteDefaultsByVersionQuery, version)
	return err
}

const listAllDefaultsQuery = `
    SELECT def.id,
           def.version,
           def.instant_launches as mapping
      FROM default_instant_launches def;
`

// ListAllDefaults returns a list of all of the default instant launches, including their version.
func (a *App) ListAllDefaults() (ListAllDefaultsResponse, error) {
	m := ListAllDefaultsResponse{Defaults: []DefaultInstantLaunchMapping{}}
	err := a.DB.Select(&m.Defaults, listAllDefaultsQuery)
	return m, err
}
