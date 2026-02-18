package jobinfo

import (
	"context"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/model/v9"
)

// JobInfo is an interface for obtaining information about a job.
type JobInfo interface {
	// JobLabels returns the set of labels to use for all Kubernetes resources associated with the given job.
	JobLabels(ctx context.Context, job *model.Job) (map[string]string, error)
}

// DefaultJobInfo is the default JobInfo interface implementation.
type DefaultJobInfo struct {
	Apps *apps.Apps
}

// JobLabels returns the set of labels to use for all Kubernetes resources associated with the given job.
func (j *DefaultJobInfo) JobLabels(ctx context.Context, job *model.Job) (map[string]string, error) {
	name := []rune(job.Name)

	var stringmax int
	if len(name) >= 63 {
		stringmax = 62
	} else {
		stringmax = len(name) - 1
	}

	ipAddr, err := j.Apps.GetUserIP(ctx, job.UserID)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"external-id":   job.InvocationID,
		"app-name":      common.LabelValueString(job.AppName),
		"app-id":        job.AppID,
		"username":      common.LabelValueString(job.Submitter),
		"user-id":       job.UserID,
		"analysis-name": common.LabelValueString(string(name[:stringmax])),
		"app-type":      "interactive",
		"subdomain":     common.Subdomain(job.UserID, job.InvocationID),
		"login-ip":      ipAddr,
	}, nil
}

// NewJobInfo returns an implementation of JobInfo for the given Apps instance.
func NewJobInfo(apps *apps.Apps) JobInfo {
	return JobInfo(&DefaultJobInfo{Apps: apps})
}
