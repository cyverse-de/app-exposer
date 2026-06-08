package vicebuild

import (
	"fmt"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/operatorclient"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Gateway defaults and the traefik CORS middleware reference, matching the
// incluster httproutes builders.
const (
	defaultGatewayName = gatewayv1.ObjectName("vice")

	traefikProvider     = "traefik"
	corsMiddlewareGroup = gatewayv1.Group("traefik.io")
	corsMiddlewareKind  = gatewayv1.Kind("Middleware")
	corsMiddlewareName  = gatewayv1.ObjectName("vice-cors-headers")
)

// HTTPRoute builds the analysis's Gateway API HTTPRoute with cluster-correct
// values baked in: the hostname uses the cluster BaseDomain (old
// TransformHostnames), the parentRef points at the cluster gateway (old
// TransformGatewayNamespace), and the backend points at the loading-page
// service (old TransformBackendToLoadingService). Provider-specific decoration
// (e.g. traefik CORS) is applied last.
func (c *Config) HTTPRoute(spec *operatorclient.VICESpec) *gatewayv1.HTTPRoute {
	subdomain := common.Subdomain(spec.UserID, string(spec.ExternalID))
	hostname := gatewayv1.Hostname(fmt.Sprintf("%s.%s", subdomain, c.BaseDomain))

	gatewayNamespace := c.GatewayNamespace
	if gatewayNamespace == "" {
		gatewayNamespace = c.Namespace
	}
	gwNamespace := gatewayv1.Namespace(gatewayNamespace)

	gatewayName := defaultGatewayName
	if c.GatewayName != "" {
		gatewayName = gatewayv1.ObjectName(c.GatewayName)
	}

	backendName := gatewayv1.ObjectName(c.LoadingServiceName)
	backendPort := gatewayv1.PortNumber(c.LoadingServicePort)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName(spec),
			Namespace: c.Namespace,
			Labels:    BuildLabels(spec),
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{Namespace: &gwNamespace, Name: gatewayName},
				},
			},
			Hostnames: []gatewayv1.Hostname{hostname},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: backendName,
									Port: &backendPort,
								},
							},
						},
					},
				},
			},
		},
	}

	if c.GatewayProvider == traefikProvider {
		addTraefikCORS(route)
	}
	return route
}

// addTraefikCORS attaches the CORS-headers middleware extension filter to every
// rule, matching the traefik HTTPRoute builder.
func addTraefikCORS(route *gatewayv1.HTTPRoute) {
	for i := range route.Spec.Rules {
		route.Spec.Rules[i].Filters = append(route.Spec.Rules[i].Filters, gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterExtensionRef,
			ExtensionRef: &gatewayv1.LocalObjectReference{
				Group: corsMiddlewareGroup,
				Kind:  corsMiddlewareKind,
				Name:  corsMiddlewareName,
			},
		})
	}
}
