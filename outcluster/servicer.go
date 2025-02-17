package outcluster

import (
	"context"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	typed_corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// ServiceOptions contains the settings needed to create or update a Service for
// an interactive app.
type ServiceOptions struct {
	Name       string
	Namespace  string
	TargetPort int   `json:"target_port"`
	ListenPort int32 `json:"listen_port"`
}

// ServiceCrudder defines the interface for objects that allow CRUD operation
// on Kubernetes Services. Mostly needed to facilitate testing.
type ServiceCrudder interface {
	Create(ctx context.Context, opts *ServiceOptions) (*v1.Service, error)
	Get(ctx context.Context, name string) (*v1.Service, error)
	Update(ctx context.Context, opts *ServiceOptions) (*v1.Service, error)
	Delete(ctx context.Context, name string) error
}

// Servicer is a concrete implementation of a ServiceCrudder.
type Servicer struct {
	svc typed_corev1.ServiceInterface
}

// NewServicer returns a newly instantiated *Servicer.
func NewServicer(s typed_corev1.ServiceInterface) *Servicer {
	return &Servicer{s}
}

// Create uses the Kubernetes API to add a new Service to the indicated
// namespace. Yes, I know that using an int for targetPort and an int32 for
// listenPort is weird, but that weirdness comes from the underlying K8s API.
// I'm letting the weirdness percolate up the stack until I get annoyed enough
// to deal with it.
func (s *Servicer) Create(ctx context.Context, opts *ServiceOptions) (*v1.Service, error) {
	return s.svc.Create(
		ctx,
		&v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      opts.Name,
				Namespace: opts.Namespace,
			},
			Spec: v1.ServiceSpec{
				Ports: []v1.ServicePort{{TargetPort: intstr.FromInt(opts.TargetPort), Port: opts.ListenPort}},
			},
		},
		metav1.CreateOptions{})
}

// Get returns a *v1.Service for an existing Service.
func (s *Servicer) Get(ctx context.Context, name string) (*v1.Service, error) {
	return s.svc.Get(ctx, name, metav1.GetOptions{})
}

// Update applies updates to an existing Service.
func (s *Servicer) Update(ctx context.Context, opts *ServiceOptions) (*v1.Service, error) {
	return s.svc.Update(
		ctx,
		&v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      opts.Name,
				Namespace: opts.Namespace,
			},
			Spec: v1.ServiceSpec{
				Ports: []v1.ServicePort{{TargetPort: intstr.FromInt(opts.TargetPort), Port: opts.ListenPort}},
			},
		},
		metav1.UpdateOptions{})
}

// Delete removes a Service from Kubernetes.
func (s *Servicer) Delete(ctx context.Context, name string) error {
	return s.svc.Delete(ctx, name, metav1.DeleteOptions{})
}
