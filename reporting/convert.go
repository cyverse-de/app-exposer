package reporting

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
)

// metaInfoFromLabels builds a MetaInfo from a K8s object's name, namespace,
// creation timestamp, and labels.
func metaInfoFromLabels(name, namespace, creationTimestamp string, labels map[string]string) MetaInfo {
	return MetaInfo{
		Name:              name,
		Namespace:         namespace,
		AnalysisName:      labels["analysis-name"],
		AppName:           labels["app-name"],
		AppID:             labels["app-id"],
		ExternalID:        labels["external-id"],
		UserID:            labels["user-id"],
		Username:          labels["username"],
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
			port = container.Ports[0].ContainerPort
			user = *container.SecurityContext.RunAsUser
			group = *container.SecurityContext.RunAsGroup
		}
	}

	return &DeploymentInfo{
		MetaInfo: metaInfoFromLabels(
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
		MetaInfo: metaInfoFromLabels(
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
		MetaInfo: metaInfoFromLabels(
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
		MetaInfo: metaInfoFromLabels(
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

	return &IngressInfo{
		MetaInfo: metaInfoFromLabels(
			ingress.GetName(),
			ingress.GetNamespace(),
			ingress.GetCreationTimestamp().String(),
			labels,
		),
		Rules: ingress.Spec.Rules,
		DefaultBackend: fmt.Sprintf(
			"%s:%d",
			ingress.Spec.DefaultBackend.Service.Name,
			ingress.Spec.DefaultBackend.Service.Port.Number,
		),
	}
}
