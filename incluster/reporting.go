package incluster

import (
	"context"
	"fmt"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/pkg/errors"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
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

func (i *Incluster) podList(ctx context.Context, namespace string, customLabels map[string]string, missingLabels []string) (*corev1.PodList, error) {
	listOptions, err := getListOptions(customLabels, missingLabels)
	if err != nil {
		return nil, err
	}

	podList, err := i.clientset.CoreV1().Pods(namespace).List(ctx, listOptions)
	if err != nil {
		return nil, err
	}

	return podList, nil
}

func (i *Incluster) configmapsList(ctx context.Context, namespace string, customLabels map[string]string, missingLabels []string) (*corev1.ConfigMapList, error) {
	listOptions, err := getListOptions(customLabels, missingLabels)
	if err != nil {
		return nil, err
	}

	cfgList, err := i.clientset.CoreV1().ConfigMaps(namespace).List(ctx, listOptions)
	if err != nil {
		return nil, err
	}

	return cfgList, nil
}

func (i *Incluster) serviceList(ctx context.Context, namespace string, customLabels map[string]string, missingLabels []string) (*corev1.ServiceList, error) {
	listOptions, err := getListOptions(customLabels, missingLabels)
	if err != nil {
		return nil, err
	}

	svcList, err := i.clientset.CoreV1().Services(namespace).List(ctx, listOptions)
	if err != nil {
		return nil, err
	}

	return svcList, nil
}

func (i *Incluster) routeList(ctx context.Context, namespace string, customLabels map[string]string, missingLabels []string) (*gatewayv1.HTTPRouteList, error) {
	if i.gatewayClient == nil {
		return &gatewayv1.HTTPRouteList{}, nil
	}

	listOptions, err := getListOptions(customLabels, missingLabels)
	if err != nil {
		return nil, err
	}

	client := i.gatewayClient.HTTPRoutes(namespace)
	routeList, err := client.List(ctx, listOptions)
	if err != nil {
		return nil, err
	}
	return routeList, nil
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

// routeInfo returns a RouteInfo struct for an HTTPRoute.
func routeInfo(route *gatewayv1.HTTPRoute) *RouteInfo {
	return reporting.RouteInfoFrom(route)
}

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

func populateAnalysisID(ctx context.Context, a *apps.Apps, existingLabels map[string]string) (map[string]string, error) {
	if _, ok := existingLabels["analysis-id"]; !ok {
		externalID, ok := existingLabels["external-id"]
		if !ok {
			return existingLabels, fmt.Errorf("missing external-id key")
		}
		analysisID, err := a.GetAnalysisIDByExternalID(ctx, externalID)
		if err != nil {
			log.Debug(errors.Wrapf(err, "error getting analysis id for external id %s", externalID))
		} else {
			existingLabels["analysis-id"] = analysisID
		}
	}
	return existingLabels, nil
}

func populateSubdomain(existingLabels map[string]string) map[string]string {
	if _, ok := existingLabels["subdomain"]; !ok {
		if externalID, ok := existingLabels["external-id"]; ok {
			if userID, ok := existingLabels["user-id"]; ok {
				existingLabels["subdomain"] = common.Subdomain(userID, externalID)
			}
		}
	}

	return existingLabels
}

func populateLoginIP(ctx context.Context, a *apps.Apps, existingLabels map[string]string) (map[string]string, error) {
	if _, ok := existingLabels["login-ip"]; !ok {
		if userID, ok := existingLabels["user-id"]; ok {
			ipAddr, err := a.GetUserIP(ctx, userID)
			if err != nil {
				return existingLabels, err
			}
			existingLabels["login-ip"] = ipAddr
		}
	}

	return existingLabels, nil
}

func (i *Incluster) populateAdditionalLabels(ctx context.Context, existingLabels map[string]string) (map[string]string, []error) {
	var err error
	var errs []error

	existingLabels = populateSubdomain(existingLabels)
	existingLabels, err = populateLoginIP(ctx, i.apps, existingLabels)
	if err != nil {
		errs = append(errs, err)
	}
	existingLabels, err = populateAnalysisID(ctx, i.apps, existingLabels)
	if err != nil {
		errs = append(errs, err)
	}

	return existingLabels, errs
}

func (i *Incluster) relabelDeployments(ctx context.Context) []error {
	filter := map[string]string{} // Empty on purpose. Only filter based on interactive label.
	var errs []error

	deployments, err := i.DeploymentList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		return append(errs, err)
	}

	for _, deployment := range deployments.Items {
		labels, deploymentErrors := i.populateAdditionalLabels(ctx, deployment.GetLabels())
		if len(deploymentErrors) > 0 {
			errs = append(errs, deploymentErrors...)
		}
		deployment.SetLabels(labels)
		_, err = i.clientset.AppsV1().Deployments(i.ViceNamespace).Update(ctx, &deployment, metav1.UpdateOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

func (i *Incluster) relabelConfigMaps(ctx context.Context) []error {
	filter := map[string]string{} // Empty on purpose. Only filter based on interactive label.
	var errs []error

	cms, err := i.configmapsList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		return append(errs, err)
	}

	for _, configmap := range cms.Items {
		labels, configmapErrors := i.populateAdditionalLabels(ctx, configmap.GetLabels())
		if len(configmapErrors) > 0 {
			errs = append(errs, configmapErrors...)
		}
		configmap.SetLabels(labels)
		_, err = i.clientset.CoreV1().ConfigMaps(i.ViceNamespace).Update(ctx, &configmap, metav1.UpdateOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

func (i *Incluster) relabelServices(ctx context.Context) []error {
	filter := map[string]string{} // Empty on purpose. Only filter based on interactive label.
	var errs []error

	svcs, err := i.serviceList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		return append(errs, err)
	}

	for _, service := range svcs.Items {
		labels, serviceErrors := i.populateAdditionalLabels(ctx, service.GetLabels())
		if len(serviceErrors) > 0 {
			errs = append(errs, serviceErrors...)
		}
		service.SetLabels(labels)
		_, err = i.clientset.CoreV1().Services(i.ViceNamespace).Update(ctx, &service, metav1.UpdateOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

func (i *Incluster) relabelRoutes(ctx context.Context) []error {
	if i.gatewayClient == nil {
		return nil
	}

	filter := map[string]string{} // Empty on purpose. Only filter based on interactive label.
	var errs []error

	routes, err := i.routeList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		return append(errs, err)
	}

	for _, route := range routes.Items {
		labels, routeErrors := i.populateAdditionalLabels(ctx, route.GetLabels())
		if len(routeErrors) > 0 {
			errs = append(errs, routeErrors...)
		}
		route.SetLabels(labels)
		_, err = i.gatewayClient.HTTPRoutes(i.ViceNamespace).Update(ctx, &route, metav1.UpdateOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// ApplyAsyncLabels ensures that the required labels are applied to all running VICE analyses.
// This is useful to avoid race conditions between the DE database and the k8s cluster,
// and also for adding new labels to "old" analyses during an update.
func (i *Incluster) ApplyAsyncLabels(ctx context.Context) []error {
	var errs []error

	errs = append(errs, i.relabelDeployments(ctx)...)
	errs = append(errs, i.relabelConfigMaps(ctx)...)
	errs = append(errs, i.relabelServices(ctx)...)
	errs = append(errs, i.relabelRoutes(ctx)...)

	return errs
}
