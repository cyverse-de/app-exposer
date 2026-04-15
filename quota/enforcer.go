package quota

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/go-mod/gotelnats"
	"github.com/cyverse-de/go-mod/pbinit"
	"github.com/cyverse-de/model/v10"
	"github.com/cyverse-de/p/go/qms"
	"github.com/jmoiron/sqlx"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"

	"k8s.io/client-go/kubernetes"
)

var log = common.Log

func shouldCountStatus(status string) bool {
	countIt := true

	skipStatuses := []string{
		"Failed",
		"Completed",
		"Canceled",
	}

	for _, s := range skipStatuses {
		if status == s {
			countIt = false
		}
	}

	return countIt
}

type Enforcer struct {
	clientset  kubernetes.Interface
	db         *sqlx.DB
	apps       *apps.Apps
	nec        *nats.EncodedConn
	scheduler  *operatorclient.Scheduler
	userDomain string
}

func NewEnforcer(
	clientset kubernetes.Interface,
	db *sqlx.DB,
	a *apps.Apps,
	ec *nats.EncodedConn,
	userDomain string,
) *Enforcer {
	return &Enforcer{
		clientset:  clientset,
		db:         db,
		apps:       a,
		nec:        ec,
		userDomain: userDomain,
	}
}

// SetScheduler configures the operator scheduler for multi-cluster job counting.
func (e *Enforcer) SetScheduler(s *operatorclient.Scheduler) {
	e.scheduler = s
}

func (e *Enforcer) countJobsForUser(ctx context.Context, namespace, username string) (int, error) {
	if e.scheduler == nil {
		return 0, fmt.Errorf("scheduler not configured for quota enforcer")
	}

	clients := e.scheduler.Clients()
	if len(clients) == 0 {
		return 0, nil
	}

	totalCount := 0
	type result struct {
		count int
		err   error
	}
	results := make([]result, len(clients))

	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Add(1)
		go func(idx int, c *operatorclient.Client) {
			defer wg.Done()
			// Use the Listing endpoint with a user filter to count jobs.
			params := url.Values{}
			params.Set("username", username)
			info, err := c.Listing(ctx, params)
			if err != nil {
				results[idx] = result{err: err}
				return
			}

			// Filter results by status to only count active jobs.
			count := 0
			for _, d := range info.Deployments {
				// We need the analysis status from the DB to decide whether to count it.
				// This is similar to the logic in the original countJobsForUser.
				status, err := e.apps.GetAnalysisStatus(ctx, d.AnalysisID)
				if err != nil {
					log.Errorf("error getting status for analysis %s: %v", d.AnalysisID, err)
					// If we can't get the status, count it to be safe.
					count++
					continue
				}
				if shouldCountStatus(status) {
					count++
				}
			}
			results[idx] = result{count: count}
		}(i, client)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			return 0, r.err
		}
		totalCount += r.count
	}

	return totalCount, nil
}

const getJobLimitForUserSQL = `
	SELECT concurrent_jobs FROM job_limits
	WHERE launcher = regexp_replace($1, '-', '_')
`

func (e *Enforcer) getJobLimitForUser(ctx context.Context, username string) (*int, error) {
	var jobLimit int
	err := e.db.QueryRowContext(ctx, getJobLimitForUserSQL, username).Scan(&jobLimit)
	if stderrors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &jobLimit, nil
}

const getDefaultJobLimitSQL = `
	SELECT concurrent_jobs FROM job_limits
	WHERE launcher IS NULL
`

func (e *Enforcer) getDefaultJobLimit(ctx context.Context) (int, error) {
	var defaultJobLimit int
	if err := e.db.QueryRowContext(ctx, getDefaultJobLimitSQL).Scan(&defaultJobLimit); err != nil {
		return 0, err
	}
	return defaultJobLimit, nil
}

func (e *Enforcer) getResourceOveragesForUser(ctx context.Context, username string) (*qms.OverageList, error) {
	var err error

	subject := "cyverse.qms.user.overages.get"

	req := &qms.AllUserOveragesRequest{
		Username: common.FixUsername(username, e.userDomain),
	}

	_, span := pbinit.InitAllUserOveragesRequest(req, subject)
	defer span.End()

	resp := pbinit.NewOverageList()

	if err = gotelnats.Request(
		ctx,
		e.nec,
		subject,
		req,
		resp,
	); err != nil {
		return nil, err
	}

	return resp, nil
}

func buildLimitError(code, msg string, defaultJobLimit, jobCount int, jobLimit *int) error {
	return common.ErrorResponse{
		ErrorCode: code,
		Message:   msg,
		Details: &map[string]interface{}{
			"defaultJobLimit": defaultJobLimit,
			"jobCount":        jobCount,
			"jobLimit":        jobLimit,
		},
	}
}

func checkOverages(user string, overages *qms.OverageList) (int, error) {
	var inOverage bool
	code := "ERR_RESOURCE_OVERAGE"
	details := make(map[string]interface{})

	for _, ov := range overages.Overages {
		if ov.Usage >= ov.Quota && ov.ResourceName == "cpu.hours" {
			inOverage = true
			details[ov.ResourceName] = fmt.Sprintf("quota: %f, usage: %f", ov.Quota, ov.Usage)
		}
	}

	if inOverage {
		msg := fmt.Sprintf("%s has resource overages.", user)
		return http.StatusBadRequest, common.ErrorResponse{
			ErrorCode: code,
			Message:   msg,
			Details:   &details,
		}
	}

	return http.StatusOK, nil
}

func validateJobLimits(user string, defaultJobLimit, jobCount int, jobLimit *int, overages *qms.OverageList) (int, error) {
	switch {

	// Jobs are disabled by default and the user has not been granted permission yet.
	case jobLimit == nil && defaultJobLimit <= 0:
		code := "ERR_PERMISSION_NEEDED"
		msg := fmt.Sprintf("%s has not been granted permission to run jobs yet", user)
		return http.StatusBadRequest, buildLimitError(code, msg, defaultJobLimit, jobCount, jobLimit)

	// Jobs have been explicitly disabled for the user.
	case jobLimit != nil && *jobLimit <= 0:
		code := "ERR_FORBIDDEN"
		msg := fmt.Sprintf("%s is not permitted to run jobs", user)
		return http.StatusBadRequest, buildLimitError(code, msg, defaultJobLimit, jobCount, jobLimit)

	// The user is using and has reached the default job limit.
	case jobLimit == nil && jobCount >= defaultJobLimit:
		code := "ERR_LIMIT_REACHED"
		msg := fmt.Sprintf("%s is already running %d or more concurrent jobs", user, defaultJobLimit)
		return http.StatusBadRequest, buildLimitError(code, msg, defaultJobLimit, jobCount, jobLimit)

	// The user has explicitly been granted the ability to run jobs and has reached the limit.
	case jobLimit != nil && jobCount >= *jobLimit:
		code := "ERR_LIMIT_REACHED"
		msg := fmt.Sprintf("%s is already running %d or more concurrent jobs", user, *jobLimit)
		return http.StatusBadRequest, buildLimitError(code, msg, defaultJobLimit, jobCount, jobLimit)

	case overages != nil && len(overages.Overages) != 0:
		return checkOverages(user, overages)

	// In every other case, we can permit the job to be launched.
	default:
		return http.StatusOK, nil
	}
}

func (e *Enforcer) ValidateJob(ctx context.Context, job *model.Job, namespace string) (int, error) {
	// Get the username
	usernameLabelValue := common.LabelValueString(job.Submitter)
	user := job.Submitter

	// Validate the number of concurrent jobs for the user.
	jobCount, err := e.countJobsForUser(ctx, namespace, usernameLabelValue)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrapf(err, "unable to determine the number of jobs that %s is currently running", user)
	}
	jobLimit, err := e.getJobLimitForUser(ctx, user)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrapf(err, "unable to determine the concurrent job limit for %s", user)
	}
	defaultJobLimit, err := e.getDefaultJobLimit(ctx)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrapf(err, "unable to determine the default concurrent job limit")
	}
	overages, err := e.getResourceOveragesForUser(ctx, user)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrapf(err, "unable to get list of resource overages for user %s", user)
	}

	return validateJobLimits(user, defaultJobLimit, jobCount, jobLimit, overages)
}

func (e *Enforcer) ValidateBatchJob(ctx context.Context, job *model.Job, namespace string) (int, error) {
	user := job.Submitter

	overages, err := e.getResourceOveragesForUser(ctx, user)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrapf(err, "unable to get list of resource overages for user %s", user)
	}

	if overages != nil && len(overages.Overages) != 0 {
		return checkOverages(user, overages)
	}

	return http.StatusOK, nil
}
