package main

import (
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typed_corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// EndpointOptions contains the settings needed to create or update an
// Endpoint for an interactive app.
type EndpointOptions struct {
	Name      string
	Namespace string
	IP        string
	Port      int32
}

// EndpointCrudder defines the interface for objects that allow CRUD operations
// on Kubernetes Endpoints. Mostly needed to facilitate testing.
type EndpointCrudder interface {
	Create(opts *EndpointOptions) (*v1.Endpoints, error)
	Get(name string) (*v1.Endpoints, error)
	Update(opts *EndpointOptions) (*v1.Endpoints, error)
	Delete(name string) error
}

// Endpointer is a concreate implementation of a EndpointCrudder.
type Endpointer struct {
	ept typed_corev1.EndpointsInterface
}

// Create uses the Kubernetes API to add a new Endpoint to the indicated
// namespace.
func (e *Endpointer) Create(opts *EndpointOptions) (*v1.Endpoints, error) {
	return e.ept.Create(&v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Namespace,
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{{IP: opts.IP}},
				Ports:     []v1.EndpointPort{{Port: opts.Port}},
			},
		},
	})
}

// Get returns a *v1.Endpoints for an existing Endpoints configuration in K8s.
func (e *Endpointer) Get(name string) (*v1.Endpoints, error) {
	return e.ept.Get(name, metav1.GetOptions{})
}

// Update applies updates to an existing set of Endpoints in K8s.
func (e *Endpointer) Update(opts *EndpointOptions) (*v1.Endpoints, error) {
	return e.ept.Update(&v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Namespace,
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{{IP: opts.IP}},
				Ports:     []v1.EndpointPort{{Port: opts.Port}},
			},
		},
	})
}

// Delete removes an Endpoints object from K8s.
func (e *Endpointer) Delete(name string) error {
	return e.ept.Delete(name, &metav1.DeleteOptions{})
}

// NewEndpointer returns a newly instantiated *Endpointer.
func NewEndpointer(e typed_corev1.EndpointsInterface) *Endpointer {
	return &Endpointer{e}
}
