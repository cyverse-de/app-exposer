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

// EnsureLoadingService ensures the loading page Service exists in the given
// namespace, creating it if missing. The service maps port 80 to the
// vice-operator's loading page container port. It selects pods using the
// provided podSelector labels.
func EnsureLoadingService(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace string,
	serviceName string,
	targetPort int32,
	podSelector map[string]string,
) error {
	client := clientset.CoreV1().Services(namespace)

	_, err := client.Get(ctx, serviceName, metav1.GetOptions{})
	if err == nil {
		log.Debugf("loading service %s already exists", serviceName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("checking for existing Service %s: %w", serviceName, err)
	}

	log.Infof("creating loading page Service %s (targetPort=%d)", serviceName, targetPort)
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
					Port:       80,
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
