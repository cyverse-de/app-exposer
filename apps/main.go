package apps

import (
	"database/sql"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Apps provides an API for accessing information about apps.
type Apps struct {
	DB         *sqlx.DB
	UserSuffix string
}

// NewApps allocates a new *Apps instance.
func NewApps(db *sqlx.DB, userSuffix string) *Apps {
	return &Apps{
		DB:         db,
		UserSuffix: userSuffix,
	}
}

const analysisIDByExternalIDQuery = `
	SELECT j.id
	  FROM jobs j
	  JOIN job_steps s ON s.job_id = j.id
	 WHERE s.external_id = $1
`

// GetAnalysisIDByExternalID returns the analysis ID based on the external ID
// passed in.
func (a *Apps) GetAnalysisIDByExternalID(externalID string) (string, error) {
	var analysisID string
	err := a.DB.QueryRow(analysisIDByExternalIDQuery, externalID).Scan(&analysisID)
	if err != nil {
		return "", err
	}
	return analysisID, nil
}

const analysisIDBySubdomainQuery = `
	SELECT j.id
	  FROM jobs j
	 WHERE j.subdomain = $1
`

// GetAnalysisIDBySubdomain returns the analysis ID based on the subdomain
// generated for it.
func (a *Apps) GetAnalysisIDBySubdomain(subdomain string) (string, error) {
	var analysisID string
	err := a.DB.QueryRow(analysisIDBySubdomainQuery, subdomain).Scan(&analysisID)
	if err != nil {
		return "", err
	}
	return analysisID, nil
}

const getUserIPQuery = `
	SELECT l.ip_address
	  FROM logins l
	  JOIN users u on l.user_id = u.id
	 WHERE u.id = $1
  ORDER BY l.login_time DESC
     LIMIT 1
`

// GetUserIP returns the latest login ip address for the given user ID.
func (a *Apps) GetUserIP(userID string) (string, error) {
	var (
		ipAddr sql.NullString
		retval string
	)

	err := a.DB.QueryRow(getUserIPQuery, userID).Scan(&ipAddr)
	if err != nil {
		return "", err
	}

	if ipAddr.Valid {
		retval = ipAddr.String
	} else {
		retval = ""
	}

	return retval, nil
}

const getAnalysisStatusQuery = `
	SELECT j.status
	  FROM jobs j
	 WHERE j.id = $1
`

// GetAnalysisStatus gets the current status of the overall Analysis/Job in the database.
func (a *Apps) GetAnalysisStatus(analysisID string) (string, error) {
	var status string
	err := a.DB.QueryRow(getAnalysisStatusQuery, analysisID).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}

const userByAnalysisIDQuery = `
	SELECT u.username,
	       u.id
		FROM users u
		JOIN jobs j on j.user_id = u.id
	 WHERE j.id = $1
`

// GetUserByAnalysisID returns the username and id of the user that launched the analysis.
func (a *Apps) GetUserByAnalysisID(analysisID string) (string, string, error) {
	var username, id string
	err := a.DB.QueryRow(userByAnalysisIDQuery, analysisID).Scan(&username, &id)
	if err != nil {
		return "", "", err
	}
	username = strings.TrimSuffix(username, a.UserSuffix)
	return username, id, nil
}

const userByUsername = `
	SELECT u.id
	  FROM users u
	 WHERE u.username = $1
`

// GetUserID returns the user's UUID based on their full username, including domain suffix.
func (a *Apps) GetUserID(username string) (string, error) {
	var id string
	err := a.DB.QueryRow(userByUsername, username).Scan(&id)
	return id, err
}

const setMillicoresStmt = `
	UPDATE jobs
	SET millicores_reserved = $2::int
	WHERE id = $1;
`

func (a *Apps) SetMillicoresReserved(analysisID string, millicores float64) error {
	_, err := a.DB.Exec(setMillicoresStmt, analysisID, millicores)
	return err
}
