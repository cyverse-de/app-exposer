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
	netv1 "k8s.io/api/networking/v1"
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
func getListOptions(customLabels map[string]string, missingLabels []string) metav1.ListOptions {
	// Get the selector populated with the labels that should be present
	s := getListSelector(customLabels)

	// Accumulate requirements for labels that must be absent from listed objects.
	var reqs []labels.Requirement

	for _, missingLabel := range missingLabels {
		newReq, err := labels.NewRequirement(missingLabel, selection.DoesNotExist, []string{})
		if err != nil {
			log.Error(err)
		} else {
			reqs = append(reqs, *newReq)
		}
	}

	s = s.Add(reqs...)

	return metav1.ListOptions{
		LabelSelector: s.String(),
	}
}

// DeploymentList returns all Deployments in namespace matching customLabels
// and lacking any of the missingLabels. The "app-type=interactive" selector
// is always applied.
func (i *Incluster) DeploymentList(ctx context.Context, namespace string, customLabels map[string]string, missingLabels []string) (*v1.DeploymentList, error) {
	listOptions := getListOptions(customLabels, missingLabels)

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
	listOptions := getListOptions(customLabels, missingLabels)

	podList, err := i.clientset.CoreV1().Pods(namespace).List(ctx, listOptions)
	if err != nil {
		return nil, err
	}

	return podList, nil
}

func (i *Incluster) configmapsList(ctx context.Context, namespace string, customLabels map[string]string, missingLabels []string) (*corev1.ConfigMapList, error) {
	listOptions := getListOptions(customLabels, missingLabels)

	cfgList, err := i.clientset.CoreV1().ConfigMaps(namespace).List(ctx, listOptions)
	if err != nil {
		return nil, err
	}

	return cfgList, nil
}

func (i *Incluster) serviceList(ctx context.Context, namespace string, customLabels map[string]string, missingLabels []string) (*corev1.ServiceList, error) {
	listOptions := getListOptions(customLabels, missingLabels)

	svcList, err := i.clientset.CoreV1().Services(namespace).List(ctx, listOptions)
	if err != nil {
		return nil, err
	}

	return svcList, nil
}

func (i *Incluster) ingressList(ctx context.Context, namespace string, customLabels map[string]string, missingLabels []string) (*netv1.IngressList, error) {
	listOptions := getListOptions(customLabels, missingLabels)

	client := i.clientset.NetworkingV1().Ingresses(namespace)
	ingList, err := client.List(ctx, listOptions)
	if err != nil {
		return nil, err
	}

	return ingList, nil
}

// Type aliases for backward compatibility — canonical types live in reporting/.
type MetaInfo = reporting.MetaInfo
type DeploymentInfo = reporting.DeploymentInfo
type PodInfo = reporting.PodInfo
type ConfigMapInfo = reporting.ConfigMapInfo
type ServiceInfoPort = reporting.ServiceInfoPort
type ServiceInfo = reporting.ServiceInfo
type IngressInfo = reporting.IngressInfo
type ResourceInfo = reporting.ResourceInfo

// GetFilteredDeployments returns DeploymentInfo for all VICE deployments
// matching the given label filter.
func (i *Incluster) GetFilteredDeployments(ctx context.Context, filter map[string]string) ([]DeploymentInfo, error) {
	depList, err := i.DeploymentList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	deployments := make([]DeploymentInfo, 0, len(depList.Items))

	for _, dep := range depList.Items {
		info := reporting.DeploymentInfoFrom(&dep)
		deployments = append(deployments, *info)
	}

	return deployments, nil
}

// GetFilteredPods returns PodInfo for all VICE pods matching the given label filter.
func (i *Incluster) GetFilteredPods(ctx context.Context, filter map[string]string) ([]PodInfo, error) {
	podList, err := i.podList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	pods := make([]PodInfo, 0, len(podList.Items))

	for _, pod := range podList.Items {
		info := reporting.PodInfoFrom(&pod)
		pods = append(pods, *info)
	}

	return pods, nil
}

// GetFilteredConfigMaps returns ConfigMapInfo for all VICE ConfigMaps
// matching the given label filter.
func (i *Incluster) GetFilteredConfigMaps(ctx context.Context, filter map[string]string) ([]ConfigMapInfo, error) {
	cmList, err := i.configmapsList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	cms := make([]ConfigMapInfo, 0, len(cmList.Items))

	for _, cm := range cmList.Items {
		info := reporting.ConfigMapInfoFrom(&cm)
		cms = append(cms, *info)
	}

	return cms, nil
}

// GetFilteredServices returns ServiceInfo for all VICE Services
// matching the given label filter.
func (i *Incluster) GetFilteredServices(ctx context.Context, filter map[string]string) ([]ServiceInfo, error) {
	svcList, err := i.serviceList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	svcs := make([]ServiceInfo, 0, len(svcList.Items))

	for _, svc := range svcList.Items {
		info := reporting.ServiceInfoFrom(&svc)
		svcs = append(svcs, *info)
	}

	return svcs, nil
}

// GetFilteredIngresses returns IngressInfo for all VICE Ingresses
// matching the given label filter.
func (i *Incluster) GetFilteredIngresses(ctx context.Context, filter map[string]string) ([]IngressInfo, error) {
	ingList, err := i.ingressList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	ingresses := make([]IngressInfo, 0, len(ingList.Items))

	for _, ingress := range ingList.Items {
		info := reporting.IngressInfoFrom(&ingress)
		ingresses = append(ingresses, *info)
	}

	return ingresses, nil
}

// FixUsername normalizes a username by appending the configured user suffix
// if it is not already present.
func (i *Incluster) FixUsername(username string) string {
	return common.FixUsername(username, i.UserSuffix)
}

// DoResourceListing returns all VICE K8s resources (deployments, pods,
// configmaps, services, ingresses) matching the given label filter.
func (i *Incluster) DoResourceListing(ctx context.Context, filter map[string]string) (*ResourceInfo, error) {
	deployments, err := i.GetFilteredDeployments(ctx, filter)
	if err != nil {
		return nil, err
	}

	pods, err := i.GetFilteredPods(ctx, filter)
	if err != nil {
		return nil, err
	}

	cms, err := i.GetFilteredConfigMaps(ctx, filter)
	if err != nil {
		return nil, err
	}

	svcs, err := i.GetFilteredServices(ctx, filter)
	if err != nil {
		return nil, err
	}

	ingresses, err := i.GetFilteredIngresses(ctx, filter)
	if err != nil {
		return nil, err
	}

	return &ResourceInfo{
		Deployments: deployments,
		Pods:        pods,
		ConfigMaps:  cms,
		Services:    svcs,
		Ingresses:   ingresses,
	}, nil
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
				existingLabels["subdomain"] = IngressName(userID, externalID)
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

func (i *Incluster) relabelDeployments(ctx context.Context) []error {
	filter := map[string]string{} // Empty on purpose. Only filter based on interactive label.
	var errs []error

	deployments, err := i.DeploymentList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		return append(errs, err)
	}

	for _, deployment := range deployments.Items {
		existingLabels := deployment.GetLabels()

		existingLabels = populateSubdomain(existingLabels)

		existingLabels, err = populateLoginIP(ctx, i.apps, existingLabels)
		if err != nil {
			errs = append(errs, err)
		}

		existingLabels, err = populateAnalysisID(ctx, i.apps, existingLabels)
		if err != nil {
			errs = append(errs, err)
		}

		deployment.SetLabels(existingLabels)
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
		existingLabels := configmap.GetLabels()

		existingLabels = populateSubdomain(existingLabels)

		existingLabels, err = populateLoginIP(ctx, i.apps, existingLabels)
		if err != nil {
			errs = append(errs, err)
		}

		existingLabels, err = populateAnalysisID(ctx, i.apps, existingLabels)
		if err != nil {
			errs = append(errs, err)
		}

		configmap.SetLabels(existingLabels)
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
		existingLabels := service.GetLabels()

		existingLabels = populateSubdomain(existingLabels)

		existingLabels, err = populateLoginIP(ctx, i.apps, existingLabels)
		if err != nil {
			errs = append(errs, err)
		}

		existingLabels, err = populateAnalysisID(ctx, i.apps, existingLabels)
		if err != nil {
			errs = append(errs, err)
		}

		service.SetLabels(existingLabels)
		_, err = i.clientset.CoreV1().Services(i.ViceNamespace).Update(ctx, &service, metav1.UpdateOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

func (i *Incluster) relabelIngresses(ctx context.Context) []error {
	filter := map[string]string{} // Empty on purpose. Only filter based on interactive label.
	var errs []error

	ingresses, err := i.ingressList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		return append(errs, err)
	}

	for _, ingress := range ingresses.Items {
		existingLabels := ingress.GetLabels()

		existingLabels = populateSubdomain(existingLabels)

		existingLabels, err = populateLoginIP(ctx, i.apps, existingLabels)
		if err != nil {
			errs = append(errs, err)
		}

		existingLabels, err = populateAnalysisID(ctx, i.apps, existingLabels)
		if err != nil {
			errs = append(errs, err)
		}

		ingress.SetLabels(existingLabels)
		_, err = i.clientset.NetworkingV1().Ingresses(i.ViceNamespace).Update(ctx, &ingress, metav1.UpdateOptions{})
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
	errs = append(errs, i.relabelIngresses(ctx)...)

	return errs
}
