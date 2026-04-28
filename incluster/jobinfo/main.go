package jobinfo

import (
	"context"
	"errors"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/model/v10"
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
	if job.Name == "" {
		return nil, errors.New("job name must not be empty")
	}

	name := []rune(job.Name)
	stringmax := min(len(name), 63)

	ipAddr, err := j.Apps.GetUserIP(ctx, job.UserID)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		constants.ExternalIDLabel: job.InvocationID,
		constants.AnalysisIDLabel: job.ID,
		constants.AppNameLabel:    common.LabelValueString(job.AppName),
		constants.AppIDLabel:      job.AppID,
		constants.UsernameLabel:   common.LabelValueString(job.Submitter),
		constants.UserIDLabel:     job.UserID,
		"analysis-name":           common.LabelValueString(string(name[:stringmax])),
		constants.AppTypeLabel:    "interactive",
		constants.SubdomainLabel:  common.Subdomain(job.UserID, job.InvocationID),
		"login-ip":                ipAddr,
	}, nil
}

// NewJobInfo returns an implementation of JobInfo for the given Apps instance.
func NewJobInfo(apps *apps.Apps) JobInfo {
	return &DefaultJobInfo{Apps: apps}
}
