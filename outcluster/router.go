package outcluster

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1"
)

const ViceGatewayName = "vice"

// RouteOptions contains the settings needed to create or update an HTTP route for an interactive app.
type RouteOptions struct {
	Name      string
	Namespace string
	Service   string
	Port      int
}

// RouteCrudder defines the interface for objects that allow CRUD operations on Kubernetes HTTPRoutes. Mostly needed to
// facilitate testing.
type RouteCrudder interface {
	OptsFromHTTPRoute(route *gatewayv1.HTTPRoute) (*RouteOptions, error)
	HTTPRouteFromOpts(opts *RouteOptions) *gatewayv1.HTTPRoute
	Create(ctx context.Context, opts *RouteOptions) (*gatewayv1.HTTPRoute, error)
	Get(ctx context.Context, namespace, name string) (*gatewayv1.HTTPRoute, error)
	Update(ctx context.Context, opts *RouteOptions) (*gatewayv1.HTTPRoute, error)
	Delete(ctx context.Context, namespace, name string) error
}

// Router is a concrete implementation of the RouteCrudder interface.
type Router struct {
	deNamespace   string
	viceDomain    string
	gatewayClient *gatewayclient.GatewayV1Client
}

// NewRouter creates a new Router instance.
func NewRouter(deNamespace, viceDomain string, gatewayClient *gatewayclient.GatewayV1Client) *Router {
	return &Router{
		deNamespace:   deNamespace,
		viceDomain:    viceDomain,
		gatewayClient: gatewayClient,
	}
}

// Returns the route options corresponding to the given HTTPRoute.
func (r *Router) OptsFromHTTPRoute(route *gatewayv1.HTTPRoute) (*RouteOptions, error) {

	// Check for a malformed route. This shouldn't happen but a panic would be confusing if it ever did.
	if len(route.Spec.Rules) == 0 {
		return nil, fmt.Errorf("no rules defined for HTTPRoute: %s", route.Name)
	}
	if len(route.Spec.Rules) == 0 || len(route.Spec.Rules[0].BackendRefs) == 0 {
		return nil, fmt.Errorf("no backend refs defined for the first rule of HTTPRoute: %s", route.Name)
	}

	// Return the route options.
	backend := route.Spec.Rules[0].BackendRefs[0].BackendRef
	routeOptions := &RouteOptions{
		Name:      route.Name,
		Namespace: route.Namespace,
		Service:   string(backend.Name),
		Port:      int(*backend.Port),
	}
	return routeOptions, nil
}

// Returns the HTTPRoute corresponding to the given route options.
func (r *Router) HTTPRouteFromOpts(opts *RouteOptions) *gatewayv1.HTTPRoute {
	portNumber := gatewayv1.PortNumber(opts.Port)
	gatewayNamespace := gatewayv1.Namespace(r.deNamespace)
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Namespace,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Namespace: &gatewayNamespace,
						Name:      ViceGatewayName,
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{
				gatewayv1.Hostname(opts.Name),
			},
			Rules: []gatewayv1.HTTPRouteRule{
				gatewayv1.HTTPRouteRule{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						gatewayv1.HTTPBackendRef{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: gatewayv1.ObjectName(opts.Service),
									Port: &portNumber,
								},
							},
						},
					},
				},
			},
		},
	}
}

// Create uses the Kubernetes API to add a new HTTPRoute to the indicated namespace.
func (r *Router) Create(ctx context.Context, opts *RouteOptions) (*gatewayv1.HTTPRoute, error) {
	route := r.HTTPRouteFromOpts(opts)
	createOpts := metav1.CreateOptions{}

	return r.gatewayClient.HTTPRoutes(opts.Namespace).Create(ctx, route, createOpts)
}

// Get returns an HTTPRoute for the named HTTP route in Kubernetes.
func (r *Router) Get(ctx context.Context, namespace, name string) (*gatewayv1.HTTPRoute, error) {
	return r.gatewayClient.HTTPRoutes(namespace).Get(ctx, name, metav1.GetOptions{})
}

// Update modifies and existing HTTPRoute in Kubernetes to match the provided info.
func (r *Router) Update(ctx context.Context, opts *RouteOptions) (*gatewayv1.HTTPRoute, error) {
	route := r.HTTPRouteFromOpts(opts)
	updateOpts := metav1.UpdateOptions{}

	return r.gatewayClient.HTTPRoutes(opts.Namespace).Update(ctx, route, updateOpts)
}

// Delete removes the specified HTTPRoute from Kubernetes.
func (r *Router) Delete(ctx context.Context, namespace, name string) error {
	return r.gatewayClient.HTTPRoutes(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}
