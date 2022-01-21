package apps

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/apd"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/model"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

var log = common.Log.WithFields(logrus.Fields{"package": "apps"})

type millicoresJob struct {
	ID                 uuid.UUID
	Job                model.Job
	MillicoresReserved *apd.Decimal
}

// Apps provides an API for accessing information about apps.
type Apps struct {
	DB         *sqlx.DB
	UserSuffix string
	addJob     chan millicoresJob
	jobDone    chan uuid.UUID
	exit       chan bool
	jobs       map[string]bool
}

// NewApps allocates a new *Apps instance.
func NewApps(db *sqlx.DB, userSuffix string) *Apps {
	return &Apps{
		DB:         db,
		UserSuffix: userSuffix,
		addJob:     make(chan millicoresJob),
		jobDone:    make(chan uuid.UUID),
		exit:       make(chan bool),
		jobs:       map[string]bool{},
	}
}

// Run runs the goroutine for storing millicores reserved for new jobs.
func (a *Apps) Run() {
	for {
		select {
		case mj := <-a.addJob:
			a.jobs[mj.ID.String()] = true
			go func(mj millicoresJob) {
				var err error

				log.Debugf("storing %s millicores reserved for %s", mj.MillicoresReserved.String(), mj.Job.InvocationID)
				if err = a.storeMillicoresInternal(&mj.Job, mj.MillicoresReserved); err != nil {
					log.Error(err)
				}
				log.Debugf("done storing %s millicores reserved for %s", mj.MillicoresReserved.String(), mj.Job.InvocationID)

				a.jobDone <- mj.ID
			}(mj)

		case doneJobID := <-a.jobDone:
			delete(a.jobs, doneJobID.String())

		case <-a.exit:
			break
		}
	}
}

// Finish exits the goroutine for storing millicores reserved for new jobs.
func (a *Apps) Finish() {
	a.exit <- true
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

func (a *Apps) setMillicoresReserved(analysisID string, millicores *apd.Decimal) error {
	milliInt, err := millicores.Int64()
	if err != nil {
		return err
	}
	_, err = a.DB.Exec(setMillicoresStmt, analysisID, milliInt)
	return err
}

func (a *Apps) tryForAnalysisID(job *model.Job, maxAttempts int) (string, error) {
	for i := 0; i < maxAttempts; i++ {
		analysisID, err := a.GetAnalysisIDByExternalID(job.InvocationID)
		if err != nil {
			time.Sleep(1 * time.Second)
		} else {
			return analysisID, nil
		}
	}
	return "", fmt.Errorf("failed to find analysis ID after %d attempts", maxAttempts)
}

func (a *Apps) storeMillicoresInternal(job *model.Job, millicores *apd.Decimal) error {
	analysisID, err := a.tryForAnalysisID(job, 30)
	if err != nil {
		return err
	}

	if err = a.setMillicoresReserved(analysisID, millicores); err != nil {
		return err
	}

	return err
}

// SetMillicoresReserved updates the number of millicores reserved for a single job.
func (a *Apps) SetMillicoresReserved(job *model.Job, millicores *apd.Decimal) error {
	newjob := millicoresJob{
		ID:                 uuid.New(),
		Job:                *job,
		MillicoresReserved: millicores,
	}

	a.addJob <- newjob

	return nil
}
