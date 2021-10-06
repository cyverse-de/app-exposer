package external

// Ingresser is a concrete implementation of IngressCrudder
import (
	"context"

	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typednetv1 "k8s.io/client-go/kubernetes/typed/networking/v1"
)

// IngressOptions contains the settings needed to create or update an Ingress
// for an interactive app.
type IngressOptions struct {
	Name      string
	Namespace string
	Service   string
	Port      int
}

// IngressCrudder defines the interface for objects that allow CRUD operations
// on Kubernetes Ingresses. Mostly needed to facilitate testing.
type IngressCrudder interface {
	Create(opts *IngressOptions) (*netv1.Ingress, error)
	Get(name string) (*netv1.Ingress, error)
	Update(opts *IngressOptions) (*netv1.Ingress, error)
	Delete(name string) error
}

// Ingresser is a concrete implementation of an IngressCrudder.
type Ingresser struct {
	ing   typednetv1.IngressInterface
	class string
}

// Create uses the Kubernetes API add a new Ingress to the indicated namespace.
func (i *Ingresser) Create(opts *IngressOptions) (*netv1.Ingress, error) {
	backend := &netv1.IngressBackend{
		Service: &netv1.IngressServiceBackend{
			Name: opts.Service,
			Port: netv1.ServiceBackendPort{
				Number: int32(opts.Port),
			},
		},
	}
	pathType := netv1.PathTypeImplementationSpecific
	return i.ing.Create(
		context.TODO(),
		&netv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      opts.Name,
				Namespace: opts.Namespace,
				Annotations: map[string]string{
					"kubernetes.io/ingress.class": i.class,
				},
			},
			Spec: netv1.IngressSpec{
				DefaultBackend: backend,
				Rules: []netv1.IngressRule{
					{
						Host: opts.Name, // For interactive apps, this is the job ID.
						IngressRuleValue: netv1.IngressRuleValue{
							HTTP: &netv1.HTTPIngressRuleValue{
								Paths: []netv1.HTTPIngressPath{
									{
										PathType: &pathType,
										Backend:  *backend,
									},
								},
							},
						},
					},
				},
			},
		},
		metav1.CreateOptions{})
}

// Get returns a *extv1beta.Ingress instance for the named Ingress in the K8s
// cluster.
func (i *Ingresser) Get(name string) (*netv1.Ingress, error) {
	return i.ing.Get(context.TODO(), name, metav1.GetOptions{})
}

// Update modifies an existing Ingress stored in K8s to match the provided info.
func (i *Ingresser) Update(opts *IngressOptions) (*netv1.Ingress, error) {
	backend := &netv1.IngressBackend{
		Service: &netv1.IngressServiceBackend{
			Name: opts.Service,
			Port: netv1.ServiceBackendPort{
				Number: int32(opts.Port),
			},
		},
	}
	pathType := netv1.PathTypeImplementationSpecific
	return i.ing.Update(
		context.TODO(),
		&netv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      opts.Name,
				Namespace: opts.Namespace,
				Annotations: map[string]string{
					"kubernetes.io/ingress.class": i.class,
				},
			},
			Spec: netv1.IngressSpec{
				DefaultBackend: backend,
				Rules: []netv1.IngressRule{
					{
						Host: opts.Name,
						IngressRuleValue: netv1.IngressRuleValue{
							HTTP: &netv1.HTTPIngressRuleValue{
								Paths: []netv1.HTTPIngressPath{
									{
										PathType: &pathType,
										Backend:  *backend,
									},
								},
							},
						},
					},
				},
			},
		},
		metav1.UpdateOptions{})
}

// Delete removes the specified Ingress from Kubernetes.
func (i *Ingresser) Delete(name string) error {
	return i.ing.Delete(context.TODO(), name, metav1.DeleteOptions{})
}

// NewIngresser returns a newly instantiated *Ingresser.
func NewIngresser(i typednetv1.IngressInterface, class string) *Ingresser {
	return &Ingresser{i, class}
}
