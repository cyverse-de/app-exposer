package reporting

import (
	"fmt"

	"github.com/cyverse-de/app-exposer/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// MetaInfoFromLabels builds a MetaInfo from a K8s object's name, namespace,
// creation timestamp, and labels.
func MetaInfoFromLabels(name, namespace, creationTimestamp string, labels map[string]string) MetaInfo {
	return MetaInfo{
		Name:              name,
		Namespace:         namespace,
		AnalysisID:        constants.AnalysisID(labels[constants.AnalysisIDLabel]),
		AnalysisName:      labels["analysis-name"],
		AppName:           labels[constants.AppNameLabel],
		AppID:             labels[constants.AppIDLabel],
		ExternalID:        constants.ExternalID(labels[constants.ExternalIDLabel]),
		UserID:            labels[constants.UserIDLabel],
		Username:          labels[constants.UsernameLabel],
		CreationTimestamp: creationTimestamp,
	}
}

// DeploymentInfoFrom converts a K8s Deployment into a DeploymentInfo.
func DeploymentInfoFrom(deployment *appsv1.Deployment) *DeploymentInfo {
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

			if len(container.Ports) > 0 {
				port = container.Ports[0].ContainerPort
			}
			if container.SecurityContext != nil {
				if container.SecurityContext.RunAsUser != nil {
					user = *container.SecurityContext.RunAsUser
				}
				if container.SecurityContext.RunAsGroup != nil {
					group = *container.SecurityContext.RunAsGroup
				}
			}
		}
	}

	return &DeploymentInfo{
		MetaInfo: MetaInfoFromLabels(
			deployment.GetName(),
			deployment.GetNamespace(),
			deployment.GetCreationTimestamp().String(),
			labels,
		),
		Image:   image,
		Command: command,
		Port:    port,
		User:    user,
		Group:   group,
	}
}

// PodInfoFrom converts a K8s Pod into a PodInfo.
func PodInfoFrom(pod *corev1.Pod) *PodInfo {
	labels := pod.GetObjectMeta().GetLabels()

	return &PodInfo{
		MetaInfo: MetaInfoFromLabels(
			pod.GetName(),
			pod.GetNamespace(),
			pod.GetCreationTimestamp().String(),
			labels,
		),
		Phase:                 string(pod.Status.Phase),
		Message:               pod.Status.Message,
		Reason:                pod.Status.Reason,
		ContainerStatuses:     pod.Status.ContainerStatuses,
		InitContainerStatuses: pod.Status.InitContainerStatuses,
	}
}

// ConfigMapInfoFrom converts a K8s ConfigMap into a ConfigMapInfo.
func ConfigMapInfoFrom(cm *corev1.ConfigMap) *ConfigMapInfo {
	labels := cm.GetObjectMeta().GetLabels()

	return &ConfigMapInfo{
		MetaInfo: MetaInfoFromLabels(
			cm.GetName(),
			cm.GetNamespace(),
			cm.GetCreationTimestamp().String(),
			labels,
		),
		Data: cm.Data,
	}
}

// ServiceInfoFrom converts a K8s Service into a ServiceInfo.
func ServiceInfoFrom(svc *corev1.Service) *ServiceInfo {
	labels := svc.GetObjectMeta().GetLabels()

	ports := svc.Spec.Ports
	svcInfoPorts := make([]ServiceInfoPort, 0, len(ports))

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
		MetaInfo: MetaInfoFromLabels(
			svc.GetName(),
			svc.GetNamespace(),
			svc.GetCreationTimestamp().String(),
			labels,
		),
		Ports: svcInfoPorts,
	}
}

// IngressInfoFrom converts a K8s Ingress into an IngressInfo.
func IngressInfoFrom(ingress *netv1.Ingress) *IngressInfo {
	labels := ingress.GetObjectMeta().GetLabels()

	var defaultBackend string
	if db := ingress.Spec.DefaultBackend; db != nil && db.Service != nil {
		defaultBackend = fmt.Sprintf("%s:%d", db.Service.Name, db.Service.Port.Number)
	}

	return &IngressInfo{
		MetaInfo: MetaInfoFromLabels(
			ingress.GetName(),
			ingress.GetNamespace(),
			ingress.GetCreationTimestamp().String(),
			labels,
		),
		Rules:          ingress.Spec.Rules,
		DefaultBackend: defaultBackend,
	}
}

// RouteInfoFrom converts a Gateway API HTTPRoute into a RouteInfo.
func RouteInfoFrom(route *gatewayv1.HTTPRoute) *RouteInfo {
	labels := route.GetObjectMeta().GetLabels()

	hostnames := make([]string, 0, len(route.Spec.Hostnames))
	for _, h := range route.Spec.Hostnames {
		hostnames = append(hostnames, string(h))
	}

	return &RouteInfo{
		MetaInfo: MetaInfoFromLabels(
			route.GetName(),
			route.GetNamespace(),
			route.GetCreationTimestamp().String(),
			labels,
		),
		Hostnames: hostnames,
		Rules:     route.Spec.Rules,
	}
}
