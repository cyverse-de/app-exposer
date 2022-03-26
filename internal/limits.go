package internal

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/model"
	"github.com/pkg/errors"
	v1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

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

func (i *Internal) countJobsForUser(ctx context.Context, username string) (int, error) {
	set := labels.Set(map[string]string{
		"username": username,
	})

	listoptions := metav1.ListOptions{
		LabelSelector: set.AsSelector().String(),
	}

	depclient := i.clientset.AppsV1().Deployments(i.ViceNamespace)
	deplist, err := depclient.List(ctx, listoptions)
	if err != nil {
		return 0, err
	}

	countedDeployments := []v1.Deployment{}

	for _, deployment := range deplist.Items {
		var (
			externalID, analysisID, analysisStatus string
			ok                                     bool
		)

		labels := deployment.GetLabels()

		// If we don't have the external-id on the deployment, count it.
		if externalID, ok = labels["external-id"]; !ok {
			countedDeployments = append(countedDeployments, deployment)
			continue
		}

		if analysisID, err = i.apps.GetAnalysisIDByExternalID(externalID); err != nil {
			// If we failed to get it from the database, count it because it
			// shouldn't be running.
			log.Error(err)
			countedDeployments = append(countedDeployments, deployment)
			continue
		}

		analysisStatus, err = i.apps.GetAnalysisStatus(analysisID)
		if err != nil {
			// If we failed to get the status, then something is horribly wrong.
			// Count the analysis.
			log.Error(err)
			countedDeployments = append(countedDeployments, deployment)
			continue
		}

		// If the running state is Failed, Completed, or Canceled, don't
		// count it because it's probably in the process of shutting down
		// or the database and the cluster are out of sync which is not
		// the user's fault.
		if shouldCountStatus(analysisStatus) {
			countedDeployments = append(countedDeployments, deployment)
		}
	}

	return len(countedDeployments), nil
}

const getJobLimitForUserSQL = `
	SELECT concurrent_jobs FROM job_limits
	WHERE launcher = regexp_replace($1, '-', '_')
`

func (i *Internal) getJobLimitForUser(username string) (*int, error) {
	var jobLimit int
	err := i.db.QueryRow(getJobLimitForUserSQL, username).Scan(&jobLimit)
	if err == sql.ErrNoRows {
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

func (i *Internal) getDefaultJobLimit() (int, error) {
	var defaultJobLimit int
	if err := i.db.QueryRow(getDefaultJobLimitSQL).Scan(&defaultJobLimit); err != nil {
		return 0, err
	}
	return defaultJobLimit, nil
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

func validateJobLimits(user string, defaultJobLimit, jobCount int, jobLimit *int) (int, error) {
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

	// In every other case, we can permit the job to be launched.
	default:
		return http.StatusOK, nil
	}
}

func (i *Internal) validateJob(ctx context.Context, job *model.Job) (int, error) {

	// Verify that the job type is supported by this service
	if strings.ToLower(job.ExecutionTarget) != "interapps" {
		return http.StatusInternalServerError, fmt.Errorf("job type %s is not supported by this service", job.Type)
	}

	// Get the username
	usernameLabelValue := labelValueString(job.Submitter)
	user := job.Submitter

	// Validate the number of concurrent jobs for the user.
	jobCount, err := i.countJobsForUser(ctx, usernameLabelValue)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrapf(err, "unable to determine the number of jobs that %s is currently running", user)
	}
	jobLimit, err := i.getJobLimitForUser(user)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrapf(err, "unable to determine the concurrent job limit for %s", user)
	}
	defaultJobLimit, err := i.getDefaultJobLimit()
	if err != nil {
		return http.StatusInternalServerError, errors.Wrapf(err, "unable to determine the default concurrent job limit")
	}
	return validateJobLimits(user, defaultJobLimit, jobCount, jobLimit)
}
