package operator

import (
	"context"
	"fmt"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// EnsureService ensures a ClusterIP Service exists in the given namespace,
// creating it if missing. If the service already exists, it is updated to
// match the desired configuration (ports, selector).
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

	desired := &apiv1.Service{
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
	}

	if err := upsert(ctx, client, "Service", serviceName, desired); err != nil {
		return fmt.Errorf("ensuring Service %s: %w", serviceName, err)
	}
	return nil
}
