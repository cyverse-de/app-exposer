package apps

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/apd"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/model/v10"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
)

const otelName = "github.com/cyverse-de/app-exposer/apps"

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
GoroutineLoop:
	for {
		select {
		case mj := <-a.addJob:
			a.jobs[mj.ID.String()] = true
			go func(mj millicoresJob) {
				ctx, span := otel.Tracer(otelName).Start(context.Background(), "job millicores goroutine")
				defer span.End()
				var err error

				log.Debugf("storing %s millicores reserved for %s", mj.MillicoresReserved.String(), mj.Job.InvocationID)
				if err = a.storeMillicoresInternal(ctx, &mj.Job, mj.MillicoresReserved); err != nil {
					log.Error(err)
				}
				log.Debugf("done storing %s millicores reserved for %s", mj.MillicoresReserved.String(), mj.Job.InvocationID)

				a.jobDone <- mj.ID
			}(mj)

		case doneJobID := <-a.jobDone:
			delete(a.jobs, doneJobID.String())

		case <-a.exit:
			break GoroutineLoop
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
func (a *Apps) GetAnalysisIDByExternalID(ctx context.Context, externalID constants.ExternalID) (constants.AnalysisID, error) {
	var analysisID constants.AnalysisID
	err := a.DB.QueryRowContext(ctx, analysisIDByExternalIDQuery, externalID).Scan(&analysisID)
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
func (a *Apps) GetAnalysisIDBySubdomain(ctx context.Context, subdomain string) (constants.AnalysisID, error) {
	var analysisID constants.AnalysisID
	err := a.DB.QueryRowContext(ctx, analysisIDBySubdomainQuery, subdomain).Scan(&analysisID)
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

// GetUserIP returns the latest login IP address for the given user ID.
func (a *Apps) GetUserIP(ctx context.Context, userID string) (string, error) {
	var ipAddr sql.NullString
	err := a.DB.QueryRowContext(ctx, getUserIPQuery, userID).Scan(&ipAddr)
	if err == sql.ErrNoRows {
		log.Errorf("no logins recorded for %s; please check admin Keycloak settings", userID)
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return ipAddr.String, nil
}

const getAnalysisStatusQuery = `
	SELECT j.status
	  FROM jobs j
	 WHERE j.id = $1
`

// GetAnalysisStatus gets the current status of the overall Analysis/Job in the database.
func (a *Apps) GetAnalysisStatus(ctx context.Context, analysisID constants.AnalysisID) (string, error) {
	var status string
	err := a.DB.QueryRowContext(ctx, getAnalysisStatusQuery, analysisID).Scan(&status)
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
func (a *Apps) GetUserByAnalysisID(ctx context.Context, analysisID constants.AnalysisID) (string, string, error) {
	var username, id string
	err := a.DB.QueryRowContext(ctx, userByAnalysisIDQuery, analysisID).Scan(&username, &id)
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
func (a *Apps) GetUserID(ctx context.Context, username string) (string, error) {
	var id string
	err := a.DB.QueryRowContext(ctx, userByUsername, username).Scan(&id)
	return id, err
}

const setMillicoresStmt = `
	UPDATE jobs
	SET millicores_reserved = $2::int
	WHERE id = $1;
`

func (a *Apps) setMillicoresReserved(ctx context.Context, analysisID constants.AnalysisID, millicores *apd.Decimal) error {
	milliInt, err := millicores.Int64()
	if err != nil {
		return err
	}
	_, err = a.DB.ExecContext(ctx, setMillicoresStmt, analysisID, milliInt)
	return err
}

func (a *Apps) tryForAnalysisID(ctx context.Context, job *model.Job, maxAttempts int) (constants.AnalysisID, error) {
	for range maxAttempts {
		analysisID, err := a.GetAnalysisIDByExternalID(ctx, constants.ExternalID(job.InvocationID))
		if err != nil {
			time.Sleep(1 * time.Second)
		} else {
			return analysisID, nil
		}
	}
	return "", fmt.Errorf("failed to find analysis ID after %d attempts", maxAttempts)
}

func (a *Apps) storeMillicoresInternal(ctx context.Context, job *model.Job, millicores *apd.Decimal) error {
	var analysisID constants.AnalysisID

	// Prefer job.ID if available (new vice-proxy provides this directly).
	if job.ID != "" {
		analysisID = constants.AnalysisID(job.ID)
	} else {
		// Fallback to lookup by external ID for backward compatibility.
		var err error
		analysisID, err = a.tryForAnalysisID(ctx, job, 30)
		if err != nil {
			return err
		}
	}

	return a.setMillicoresReserved(ctx, analysisID, millicores)
}

// SetMillicoresReserved enqueues a job to asynchronously update the millicores
// reserved for the given analysis.
func (a *Apps) SetMillicoresReserved(job *model.Job, millicores *apd.Decimal) error {
	a.addJob <- millicoresJob{
		ID:                 uuid.New(),
		Job:                *job,
		MillicoresReserved: millicores,
	}
	return nil
}

const setOperatorIDStmt = `
	UPDATE jobs
	SET operator_id = $2
	WHERE id = $1;
`

// SetOperatorID records which operator is running an analysis by writing
// jobs.operator_id directly. Best-effort: called only from the VICE launch
// handler, which logs and continues on failure. No retry — the retry loop
// previously here amplified write contention on the jobs table when the
// caller's transaction hadn't yet committed. With a single VICE operator
// in prod, a missing operator_id just means subsequent routing falls back
// to the fan-out search, which is acceptable.
func (a *Apps) SetOperatorID(ctx context.Context, analysisID constants.AnalysisID, operatorID uuid.UUID) error {
	result, err := a.DB.ExecContext(ctx, setOperatorIDStmt, analysisID, operatorID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("no jobs row found for analysis %s", analysisID)
	}
	log.Infof("set operator_id %s for analysis %s", operatorID, analysisID)
	return nil
}

// JobDebugInfo holds key fields from the jobs table for diagnostic logging.
// OperatorID is a typed uuid.UUID — its Scan implementation tolerates NULL,
// leaving the value as uuid.Nil, so callers can test with `id != uuid.Nil`
// rather than juggling a sql.NullString.
type JobDebugInfo struct {
	ID         string    `db:"id"`
	OperatorID uuid.UUID `db:"operator_id"`
	Status     string    `db:"status"`
	AppID      string    `db:"app_id"`
}

const getJobDebugInfoQuery = `
	SELECT j.id, j.operator_id, j.status, j.app_id
	FROM jobs j
	WHERE j.id = $1
`

// GetJobDebugInfo returns diagnostic fields for a job by its analysis ID,
// or nil if no row exists.
func (a *Apps) GetJobDebugInfo(ctx context.Context, analysisID constants.AnalysisID) (*JobDebugInfo, error) {
	var info JobDebugInfo
	err := a.DB.QueryRowContext(ctx, getJobDebugInfoQuery, analysisID).Scan(
		&info.ID, &info.OperatorID, &info.Status, &info.AppID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &info, nil
}

const getOperatorIDQuery = `
	SELECT operator_id
	FROM jobs
	WHERE id = $1
`

// GetOperatorID returns the UUID of the operator running an analysis, or
// uuid.Nil if no row exists or operator_id is NULL. uuid.UUID's Scan
// implementation tolerates NULL by leaving the value as uuid.Nil, so the
// "no operator yet" case (legacy launch, mid-launch before SetOperatorID
// commits, or unknown analysis) is represented uniformly.
func (a *Apps) GetOperatorID(ctx context.Context, analysisID constants.AnalysisID) (uuid.UUID, error) {
	var id uuid.UUID
	err := a.DB.QueryRowContext(ctx, getOperatorIDQuery, analysisID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		// The analysis doesn't exist in the database; treat as none.
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

const externalIDsByStatusQuery = `
	SELECT DISTINCT js.external_id
	  FROM jobs j
	  JOIN job_steps js ON j.id = js.job_id
	  JOIN job_types jt ON js.job_type_id = jt.id
     WHERE j.status = $1
       AND jt.system_id = $2;
`

// ListExternalIDs lists the external IDs of analyses filtered by status and kind.
func (a *Apps) ListExternalIDs(ctx context.Context, status constants.AnalysisStatus, kind constants.AnalysisKind) ([]string, error) {
	var ids []string
	if err := a.DB.SelectContext(ctx, &ids, externalIDsByStatusQuery, status, kind); err != nil {
		return ids, err
	}
	return ids, nil
}
