package quota

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
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

// shouldCountStatus reports whether a job in the given status should
// count against a user's concurrency quota. Terminal states are
// excluded because their resources have already been reclaimed.
func shouldCountStatus(status string) bool {
	return !slices.Contains([]string{"Failed", "Completed", "Canceled"}, status)
}

// Enforcer validates VICE job launches against per-user concurrency
// quotas and any pending resource overages. Concurrency counts are
// aggregated across every configured operator cluster via the
// scheduler — call SetScheduler before ValidateJob runs, otherwise
// multi-cluster counts fall back to the local clientset only.
type Enforcer struct {
	clientset kubernetes.Interface
	db        *sqlx.DB
	// apps is held as a narrow interface so countJobsForUser can be
	// unit-tested with a fake; production passes *apps.Apps, which
	// satisfies it structurally.
	apps       apps.AnalysisStatusLookup
	nec        *nats.EncodedConn
	scheduler  *operatorclient.Scheduler
	userDomain string
}

// NewEnforcer constructs an Enforcer with its required dependencies.
// The scheduler is set separately via SetScheduler because it's wired
// up after the operator configuration is loaded at startup.
func NewEnforcer(
	clientset kubernetes.Interface,
	db *sqlx.DB,
	a apps.AnalysisStatusLookup,
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

// countJobsForUser returns the number of jobs the named user is currently
// running across all configured operators, together with the names of any
// operators that failed to respond during the count.
//
// Policy: count-surviving with floor semantics. If one or more operators
// are unreachable, the returned count is a *lower bound* on the user's
// actual job count. The caller (ValidateJob) uses this lower bound as the
// input to the quota check: a user who is safely under the limit based
// on the visible operators is permitted to launch, accepting that they
// could, in principle, have invisible jobs on the degraded cluster that
// would push them over. This is deliberately more permissive than the
// stricter alternative of refusing every launch during any partial
// outage — that would take the whole DE down on a single cluster blip,
// which is a worse user experience than the risk of a user exceeding
// their concurrent-job limit by a small amount for the duration of an
// outage. The degraded list is surfaced to the caller so operators can
// be logged and correlated with outages after the fact.
//
// Errors have a different semantics than degraded operators. Listing
// failures (HTTP/network) populate `degraded` but do not return an
// error. A non-ErrNoRows failure from GetAnalysisStatus is treated as a
// fatal DB problem and returns an error, because a DB outage affects
// every count request and cannot be localized to one cluster — falling
// back to count-surviving there would quietly corrupt quota decisions
// for all users.
func (e *Enforcer) countJobsForUser(ctx context.Context, username string) (int, []string, error) {
	if e.scheduler == nil {
		return 0, nil, fmt.Errorf("scheduler not configured for quota enforcer")
	}

	clients := e.scheduler.Clients()
	if len(clients) == 0 {
		return 0, nil, nil
	}

	// Per-goroutine result segregates listing errors (operator-local,
	// tolerable) from DB lookup errors (global, fatal) so the aggregator
	// can apply the right policy to each.
	type result struct {
		count      int
		listingErr error
		dbErr      error
	}
	results := make([]result, len(clients))

	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Add(1)
		go func(idx int, c *operatorclient.Client) {
			defer wg.Done()
			// Filter by username server-side so we don't decode a large
			// cluster-wide listing just to count one user's jobs.
			params := url.Values{}
			params.Set(constants.UsernameLabel, username)
			info, err := c.Listing(ctx, params)
			if err != nil {
				results[idx] = result{listingErr: err}
				return
			}

			count := 0
			for _, d := range info.Deployments {
				// The cluster knows the analysis exists, but the quota
				// only counts jobs in active statuses (not Failed,
				// Completed, or Canceled). Consult the DB for the
				// authoritative status.
				status, err := e.apps.GetAnalysisStatus(ctx, d.AnalysisID)
				if err != nil {
					// ErrNoRows = the deployment is labeled with an
					// analysis id that has no DB row. Legitimate edge
					// case: the analysis may have been purged from the
					// DB while the K8s resources linger, or the
					// deployment may belong to a different subsystem
					// that reuses the analysis-id label. We can't count
					// something we have no authoritative status for, so
					// skip it.
					if stderrors.Is(err, sql.ErrNoRows) {
						log.Debugf("no DB row for analysis %s referenced by deployment %s; not counting", d.AnalysisID, d.Name)
						continue
					}
					// Any other error means the DB itself is in trouble
					// (connection lost, timeout, etc.). Propagate so the
					// caller returns 500 and the user retries rather
					// than getting a silently-wrong quota decision.
					results[idx] = result{dbErr: fmt.Errorf("looking up status for analysis %s: %w", d.AnalysisID, err)}
					return
				}
				if shouldCountStatus(status) {
					count++
				}
			}
			results[idx] = result{count: count}
		}(i, client)
	}
	wg.Wait()

	var (
		totalCount int
		degraded   []string
	)
	for i, r := range results {
		// DB lookup errors are fatal: they indicate an infrastructure
		// problem that affects every per-user count, so returning a
		// partial-success result here would silently corrupt quota
		// decisions for the entire DE.
		if r.dbErr != nil {
			return 0, nil, r.dbErr
		}
		// Listing errors are tolerable: the count becomes a lower bound
		// but the quota check can still proceed against visible jobs.
		// Record the operator name so the caller can log what was
		// missed.
		if r.listingErr != nil {
			log.Warnf("quota count: operator %s listing failed: %v", clients[i].Name(), r.listingErr)
			degraded = append(degraded, clients[i].Name())
			continue
		}
		totalCount += r.count
	}

	return totalCount, degraded, nil
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

// ValidateJob determines whether the given interactive job is allowed
// to launch. It checks concurrency (per-user and global defaults) and
// any pending resource overages on the submitter's account. The
// returned int is an HTTP status code that the handler layer returns
// to the client — 200 means "permitted", 400 means "rejected by
// policy" (quota exceeded, overages pending), and 500 means the
// check could not complete because of a dependency failure.
func (e *Enforcer) ValidateJob(ctx context.Context, job *model.Job, namespace string) (int, error) {
	// Get the username
	usernameLabelValue := common.LabelValueString(job.Submitter)
	user := job.Submitter

	// Validate the number of concurrent jobs for the user. jobCount is a
	// lower bound when degraded is non-empty — see countJobsForUser's
	// doc comment for the policy rationale. We log the degraded list at
	// warning level so on-call can correlate permissive quota decisions
	// with cluster outages.
	jobCount, degraded, err := e.countJobsForUser(ctx, usernameLabelValue)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrapf(err, "unable to determine the number of jobs that %s is currently running", user)
	}
	if len(degraded) > 0 {
		log.Warnf("quota check for %s used visible count only; could not query operator(s): %v", user, degraded)
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

// ValidateBatchJob determines whether the given batch job is allowed
// to launch. Unlike ValidateJob, batch jobs aren't subject to a
// concurrent-job limit — HTCondor manages that on its own — so this
// only checks for pending resource overages. Return semantics match
// ValidateJob: the int is the HTTP status code to propagate to the
// client.
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
