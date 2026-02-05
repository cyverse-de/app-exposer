package incluster

import (
	"context"
	"fmt"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/pkg/errors"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

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

	// the list of requirements for labels that should be missing from the objects
	// in the listing.
	reqs := []labels.Requirement{}

	// populate the requirements
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

// MetaInfo contains useful information provided by multiple resource types.
type MetaInfo struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	AnalysisName      string `json:"analysisName"`
	AppName           string `json:"appName"`
	AppID             string `json:"appID"`
	ExternalID        string `json:"externalID"`
	UserID            string `json:"userID"`
	Username          string `json:"username"`
	CreationTimestamp string `json:"creationTimestamp"`
}

// DeploymentInfo contains information returned about a Deployment.
type DeploymentInfo struct {
	MetaInfo
	Image   string   `json:"image"`
	Command []string `json:"command"`
	Port    int32    `json:"port"`
	User    int64    `json:"user"`
	Group   int64    `json:"group"`
}

func deploymentInfo(deployment *v1.Deployment) *DeploymentInfo {
	var (
		user    int64
		group   int64
		image   string
		port    int32
		command []string
	)

	labels := deployment.GetObjectMeta().GetLabels()
	containers := deployment.Spec.Template.Spec.Containers

	for _, container := range containers {
		if container.Name == "analysis" {
			image = container.Image
			command = container.Command
			port = container.Ports[0].ContainerPort
			user = *container.SecurityContext.RunAsUser
			group = *container.SecurityContext.RunAsGroup
		}

	}

	return &DeploymentInfo{
		MetaInfo: MetaInfo{
			Name:              deployment.GetName(),
			Namespace:         deployment.GetNamespace(),
			AnalysisName:      labels["analysis-name"],
			AppName:           labels["app-name"],
			AppID:             labels["app-id"],
			ExternalID:        labels["external-id"],
			UserID:            labels["user-id"],
			Username:          labels["username"],
			CreationTimestamp: deployment.GetCreationTimestamp().String(),
		},

		Image:   image,
		Command: command,
		Port:    port,
		User:    user,
		Group:   group,
	}
}

// PodInfo tracks information about the pods for a VICE analysis.
type PodInfo struct {
	MetaInfo
	Phase                 string                   `json:"phase"`
	Message               string                   `json:"message"`
	Reason                string                   `json:"reason"`
	ContainerStatuses     []corev1.ContainerStatus `json:"containerStatuses"`
	InitContainerStatuses []corev1.ContainerStatus `json:"initContainerStatuses"`
}

func podInfo(pod *corev1.Pod) *PodInfo {
	labels := pod.GetObjectMeta().GetLabels()

	return &PodInfo{
		MetaInfo: MetaInfo{
			Name:              pod.GetName(),
			Namespace:         pod.GetNamespace(),
			AnalysisName:      labels["analysis-name"],
			AppName:           labels["app-name"],
			AppID:             labels["app-id"],
			ExternalID:        labels["external-id"],
			UserID:            labels["user-id"],
			Username:          labels["username"],
			CreationTimestamp: pod.GetCreationTimestamp().String(),
		},
		Phase:                 string(pod.Status.Phase),
		Message:               pod.Status.Message,
		Reason:                pod.Status.Reason,
		ContainerStatuses:     pod.Status.ContainerStatuses,
		InitContainerStatuses: pod.Status.InitContainerStatuses,
	}
}

// ConfigMapInfo contains useful info about a config map.
type ConfigMapInfo struct {
	MetaInfo
	Data map[string]string `json:"data"`
}

func configMapInfo(cm *corev1.ConfigMap) *ConfigMapInfo {
	labels := cm.GetObjectMeta().GetLabels()

	return &ConfigMapInfo{
		MetaInfo: MetaInfo{
			Name:              cm.GetName(),
			Namespace:         cm.GetNamespace(),
			AnalysisName:      labels["analysis-name"],
			AppName:           labels["app-name"],
			AppID:             labels["app-id"],
			ExternalID:        labels["external-id"],
			UserID:            labels["user-id"],
			Username:          labels["username"],
			CreationTimestamp: cm.GetCreationTimestamp().String(),
		},
		Data: cm.Data,
	}
}

// ServiceInfoPort contains information about a service's Port.
type ServiceInfoPort struct {
	Name           string `json:"name"`
	NodePort       int32  `json:"nodePort"`
	TargetPort     int32  `json:"targetPort"`
	TargetPortName string `json:"targetPortName"`
	Port           int32  `json:"port"`
	Protocol       string `json:"protocol"`
}

// ServiceInfo contains info about a service
type ServiceInfo struct {
	MetaInfo
	Ports []ServiceInfoPort `json:"ports"`
}

func serviceInfo(svc *corev1.Service) *ServiceInfo {
	labels := svc.GetObjectMeta().GetLabels()

	ports := svc.Spec.Ports
	svcInfoPorts := []ServiceInfoPort{}

	for _, port := range ports {
		svcInfoPorts = append(svcInfoPorts, ServiceInfoPort{
			Name:           port.Name,
			NodePort:       port.NodePort,
			TargetPort:     port.TargetPort.IntVal,
			TargetPortName: port.TargetPort.String(),
			Port:           port.Port,
			Protocol:       string(port.Protocol),
		})
	}

	return &ServiceInfo{
		MetaInfo: MetaInfo{
			Name:              svc.GetName(),
			Namespace:         svc.GetNamespace(),
			AnalysisName:      labels["analysis-name"],
			AppName:           labels["app-name"],
			AppID:             labels["app-id"],
			ExternalID:        labels["external-id"],
			UserID:            labels["user-id"],
			Username:          labels["username"],
			CreationTimestamp: svc.GetCreationTimestamp().String(),
		},

		Ports: svcInfoPorts,
	}
}

// IngressInfo contains useful Ingress VICE info.
type IngressInfo struct {
	MetaInfo
	DefaultBackend string              `json:"defaultBackend"`
	Rules          []netv1.IngressRule `json:"rules"`
}

func ingressInfo(ingress *netv1.Ingress) *IngressInfo {
	labels := ingress.GetObjectMeta().GetLabels()

	return &IngressInfo{
		MetaInfo: MetaInfo{
			Name:              ingress.GetName(),
			Namespace:         ingress.GetNamespace(),
			AnalysisName:      labels["analysis-name"],
			AppName:           labels["app-name"],
			AppID:             labels["app-id"],
			ExternalID:        labels["external-id"],
			UserID:            labels["user-id"],
			Username:          labels["username"],
			CreationTimestamp: ingress.GetCreationTimestamp().String(),
		},
		Rules: ingress.Spec.Rules,
		DefaultBackend: fmt.Sprintf(
			"%s:%d",
			ingress.Spec.DefaultBackend.Service.Name,
			ingress.Spec.DefaultBackend.Service.Port.Number,
		),
	}
}

func (i *Incluster) GetFilteredDeployments(ctx context.Context, filter map[string]string) ([]DeploymentInfo, error) {
	depList, err := i.DeploymentList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	deployments := []DeploymentInfo{}

	for _, dep := range depList.Items {
		info := deploymentInfo(&dep)
		deployments = append(deployments, *info)
	}

	return deployments, nil
}

func (i *Incluster) GetFilteredPods(ctx context.Context, filter map[string]string) ([]PodInfo, error) {
	podList, err := i.podList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	pods := []PodInfo{}

	for _, pod := range podList.Items {
		info := podInfo(&pod)
		pods = append(pods, *info)
	}

	return pods, nil
}

func (i *Incluster) GetFilteredConfigMaps(ctx context.Context, filter map[string]string) ([]ConfigMapInfo, error) {
	cmList, err := i.configmapsList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	cms := []ConfigMapInfo{}

	for _, cm := range cmList.Items {
		info := configMapInfo(&cm)
		cms = append(cms, *info)
	}

	return cms, nil
}

func (i *Incluster) GetFilteredServices(ctx context.Context, filter map[string]string) ([]ServiceInfo, error) {
	svcList, err := i.serviceList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	svcs := []ServiceInfo{}

	for _, svc := range svcList.Items {
		info := serviceInfo(&svc)
		svcs = append(svcs, *info)
	}

	return svcs, nil
}

func (i *Incluster) GetFilteredIngresses(ctx context.Context, filter map[string]string) ([]IngressInfo, error) {
	ingList, err := i.ingressList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return nil, err
	}

	ingresses := []IngressInfo{}

	for _, ingress := range ingList.Items {
		info := ingressInfo(&ingress)
		ingresses = append(ingresses, *info)
	}

	return ingresses, nil
}

// ResourceInfo contains all of the k8s resource information about a running VICE analysis
// that we know of and care about.
type ResourceInfo struct {
	Deployments []DeploymentInfo `json:"deployments"`
	Pods        []PodInfo        `json:"pods"`
	ConfigMaps  []ConfigMapInfo  `json:"configMaps"`
	Services    []ServiceInfo    `json:"services"`
	Ingresses   []IngressInfo    `json:"ingresses"`
}

func (i *Incluster) FixUsername(username string) string {
	return common.FixUsername(username, i.UserSuffix)
}

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
	errors := []error{}

	deployments, err := i.DeploymentList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		errors = append(errors, err)
		return errors
	}

	for _, deployment := range deployments.Items {
		existingLabels := deployment.GetLabels()

		existingLabels = populateSubdomain(existingLabels)

		existingLabels, err = populateLoginIP(ctx, i.apps, existingLabels)
		if err != nil {
			errors = append(errors, err)
		}

		existingLabels, err = populateAnalysisID(ctx, i.apps, existingLabels)
		if err != nil {
			errors = append(errors, err)
		}

		deployment.SetLabels(existingLabels)
		_, err = i.clientset.AppsV1().Deployments(i.ViceNamespace).Update(ctx, &deployment, metav1.UpdateOptions{})
		if err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}

func (i *Incluster) relabelConfigMaps(ctx context.Context) []error {
	filter := map[string]string{} // Empty on purpose. Only filter based on interactive label.
	errors := []error{}

	cms, err := i.configmapsList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		errors = append(errors, err)
		return errors
	}

	for _, configmap := range cms.Items {
		existingLabels := configmap.GetLabels()

		existingLabels = populateSubdomain(existingLabels)

		existingLabels, err = populateLoginIP(ctx, i.apps, existingLabels)
		if err != nil {
			errors = append(errors, err)
		}

		existingLabels, err = populateAnalysisID(ctx, i.apps, existingLabels)
		if err != nil {
			errors = append(errors, err)
		}

		configmap.SetLabels(existingLabels)
		_, err = i.clientset.CoreV1().ConfigMaps(i.ViceNamespace).Update(ctx, &configmap, metav1.UpdateOptions{})
		if err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}

func (i *Incluster) relabelServices(ctx context.Context) []error {
	filter := map[string]string{} // Empty on purpose. Only filter based on interactive label.
	errors := []error{}

	svcs, err := i.serviceList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		errors = append(errors, err)
		return errors
	}

	for _, service := range svcs.Items {
		existingLabels := service.GetLabels()

		existingLabels = populateSubdomain(existingLabels)

		existingLabels, err = populateLoginIP(ctx, i.apps, existingLabels)
		if err != nil {
			errors = append(errors, err)
		}

		existingLabels, err = populateAnalysisID(ctx, i.apps, existingLabels)
		if err != nil {
			errors = append(errors, err)
		}

		service.SetLabels(existingLabels)
		_, err = i.clientset.CoreV1().Services(i.ViceNamespace).Update(ctx, &service, metav1.UpdateOptions{})
		if err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}

func (i *Incluster) relabelIngresses(ctx context.Context) []error {
	filter := map[string]string{} // Empty on purpose. Only filter based on interactive label.
	errors := []error{}

	ingresses, err := i.ingressList(ctx, i.ViceNamespace, filter, []string{"subdomain"})
	if err != nil {
		errors = append(errors, err)
		return errors
	}

	for _, ingress := range ingresses.Items {
		existingLabels := ingress.GetLabels()

		existingLabels = populateSubdomain(existingLabels)

		existingLabels, err = populateLoginIP(ctx, i.apps, existingLabels)
		if err != nil {
			errors = append(errors, err)
		}

		existingLabels, err = populateAnalysisID(ctx, i.apps, existingLabels)
		if err != nil {
			errors = append(errors, err)
		}

		ingress.SetLabels(existingLabels)
		client := i.clientset.NetworkingV1().Ingresses(i.ViceNamespace)
		_, err = client.Update(ctx, &ingress, metav1.UpdateOptions{})
		if err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}

// ApplyAsyncLabels ensures that the required labels are applied to all running VICE analyses.
// This is useful to avoid race conditions between the DE database and the k8s cluster,
// and also for adding new labels to "old" analyses during an update.
func (i *Incluster) ApplyAsyncLabels(ctx context.Context) []error {
	errors := []error{}

	labelDepsErrors := i.relabelDeployments(ctx)
	if len(labelDepsErrors) > 0 {
		errors = append(errors, labelDepsErrors...)
	}

	labelCMErrors := i.relabelConfigMaps(ctx)
	if len(labelCMErrors) > 0 {
		errors = append(errors, labelCMErrors...)
	}

	labelSVCErrors := i.relabelServices(ctx)
	if len(labelSVCErrors) > 0 {
		errors = append(errors, labelSVCErrors...)
	}

	labelIngressesErrors := i.relabelIngresses(ctx)
	if len(labelIngressesErrors) > 0 {
		errors = append(errors, labelIngressesErrors...)
	}

	return errors
}
