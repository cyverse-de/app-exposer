package operator

import (
	"context"
	"fmt"

	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// EnsureService ensures a ClusterIP Service exists in the given namespace,
// creating it if missing. If the service already exists, it is left unchanged.
// The service exposes servicePort and routes traffic to targetPort on pods
// matching podSelector.
func EnsureService(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace string,
	serviceName string,
	servicePort int32,
	targetPort int32,
	podSelector map[string]string,
) error {
	client := clientset.CoreV1().Services(namespace)

	_, err := client.Get(ctx, serviceName, metav1.GetOptions{})
	if err == nil {
		log.Debugf("Service %s already exists", serviceName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("checking for existing Service %s: %w", serviceName, err)
	}

	log.Infof("creating Service %s (port=%d, targetPort=%d)", serviceName, servicePort, targetPort)
	_, err = client.Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
		},
		Spec: apiv1.ServiceSpec{
			Selector: podSelector,
			Ports: []apiv1.ServicePort{
				{
					Name:       "http",
					Port:       servicePort,
					TargetPort: intstr.FromInt32(targetPort),
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating Service %s: %w", serviceName, err)
	}
	return nil
}
