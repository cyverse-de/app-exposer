package incluster

import (
	"context"
	"fmt"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/reporting"
	v1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

// getListSelector builds a label selector that includes the "app-type=interactive"
// label plus any additional labels provided in customLabels.
func getListSelector(customLabels map[string]string) labels.Selector {
	allLabels := map[string]string{
		"app-type": "interactive",
	}

	for k, v := range customLabels {
		allLabels[k] = v
	}

	set := labels.Set(allLabels)

	return set.AsSelector()
}

// getListOptions returns a ListOptions for listing a resource that has the
// labels provided in customLabels, but is missing the labels provided in missingLabels.
func getListOptions(customLabels map[string]string, missingLabels []string) (metav1.ListOptions, error) {
	// Get the selector populated with the labels that should be present.
	s := getListSelector(customLabels)

	// Accumulate requirements for labels that must be absent from listed objects.
	var reqs []labels.Requirement

	for _, missingLabel := range missingLabels {
		newReq, err := labels.NewRequirement(missingLabel, selection.DoesNotExist, []string{})
		if err != nil {
			return metav1.ListOptions{}, fmt.Errorf("invalid label requirement %q: %w", missingLabel, err)
		}
		reqs = append(reqs, *newReq)
	}

	s = s.Add(reqs...)

	return metav1.ListOptions{
		LabelSelector: s.String(),
	}, nil
}

// DeploymentList returns all Deployments in namespace matching customLabels
// and lacking any of the missingLabels. The "app-type=interactive" selector
// is always applied.
func (i *Incluster) DeploymentList(ctx context.Context, namespace string, customLabels map[string]string, missingLabels []string) (*v1.DeploymentList, error) {
	listOptions, err := getListOptions(customLabels, missingLabels)
	if err != nil {
		return nil, err
	}

	depList, err := i.clientset.AppsV1().Deployments(namespace).List(ctx, listOptions)
	if err != nil {
		return nil, err
	}

	return depList, nil
}

// UserDeploymentInfo contains basic deployment info for logout forwarding
type UserDeploymentInfo struct {
	ExternalID string
	UserID     string
}

// GetDeploymentsByUserID returns all VICE deployments for a given user ID
func (i *Incluster) GetDeploymentsByUserID(ctx context.Context, userID string) ([]UserDeploymentInfo, error) {
	set := labels.Set(map[string]string{
		"app-type": "interactive",
		"user-id":  userID,
	})

	opts := metav1.ListOptions{
		LabelSelector: set.AsSelector().String(),
	}

	depList, err := i.clientset.AppsV1().Deployments(i.ViceNamespace).List(ctx, opts)
	if err != nil {
		return nil, err
	}

	result := make([]UserDeploymentInfo, 0, len(depList.Items))
	for _, dep := range depList.Items {
		if extID, ok := dep.Labels["external-id"]; ok {
			result = append(result, UserDeploymentInfo{
				ExternalID: extID,
				UserID:     userID,
			})
		}
	}

	return result, nil
}

// Type aliases for backward compatibility — canonical types live in reporting/.
type MetaInfo = reporting.MetaInfo
type DeploymentInfo = reporting.DeploymentInfo
type PodInfo = reporting.PodInfo
type ConfigMapInfo = reporting.ConfigMapInfo
type ServiceInfoPort = reporting.ServiceInfoPort
type ServiceInfo = reporting.ServiceInfo
type IngressInfo = reporting.IngressInfo
type RouteInfo = reporting.RouteInfo

// ResourceInfo contains all of the K8s resource information about a running
// VICE analysis that we know of and care about.
type ResourceInfo struct {
	Deployments []DeploymentInfo `json:"deployments"`
	Pods        []PodInfo        `json:"pods"`
	ConfigMaps  []ConfigMapInfo  `json:"configMaps"`
	Services    []ServiceInfo    `json:"services"`
	Routes      []RouteInfo      `json:"routes"`
}

// FixUsername normalizes a username by appending the configured user suffix
// if it is not already present.
func (i *Incluster) FixUsername(username string) string {
	return common.FixUsername(username, i.UserSuffix)
}
