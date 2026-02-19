package httproutes

import (
	"context"

	"github.com/cyverse-de/model/v9"
	apiv1 "k8s.io/api/core/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const CORSMiddlewareGroup = gatewayv1.Group("traefik.io")
const CORSMiddlewareKind = gatewayv1.Kind("Middleware")
const CORSMiddlewareName = gatewayv1.ObjectName("vice-cors-headers")

type TraefikHTTPRouteBuilder struct {
	CommonBuilder *CommonHTTPRouteBuilder
}

func (t TraefikHTTPRouteBuilder) BuildRoute(
	ctx context.Context,
	job *model.Job,
	svc *apiv1.Service,
) (*gatewayv1.HTTPRoute, error) {
	route, err := t.CommonBuilder.BuildRoute(ctx, job, svc)
	if err != nil {
		return nil, err
	}

	// Add the extension reference filters for CORS headers to all of the route rules.
	for _, rule := range route.Spec.Rules {
		filter := gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterExtensionRef,
			ExtensionRef: &gatewayv1.LocalObjectReference{
				Group: CORSMiddlewareGroup,
				Kind:  CORSMiddlewareKind,
				Name:  CORSMiddlewareName,
			},
		}
		rule.Filters = append(rule.Filters, filter)
	}

	return route, err
}

// NewTraefikHTTPRouteBuilder returns a route builder capable of building HTTPRoutes that are compatible with the
// traefik Gateway provider.
func NewTraefikHTTPRouteBuilder(commonBuilder *CommonHTTPRouteBuilder) HTTPRouteBuilder {
	return &TraefikHTTPRouteBuilder{CommonBuilder: commonBuilder}
}
