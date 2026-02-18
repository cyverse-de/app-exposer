package httproutes

import (
	"context"
	"fmt"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/incluster/jobinfo"
	"github.com/cyverse-de/model/v9"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const VICEGatewayName = gatewayv1.ObjectName("vice")

// HTTPRouteBuilder is an interface for building HTTPRoutes.
type HTTPRouteBuilder interface {

	// BuildRoute builds an HTTPRoute for the given job and service.
	BuildRoute(ctx context.Context, job *model.Job, svc *apiv1.Service) (*gatewayv1.HTTPRoute, error)
}

// CommonHTTPRouteBuilder is an HTTPRouteBuilder that adds no provider-specific settings.
type CommonHTTPRouteBuilder struct {
	Provider      string
	VICENamespace gatewayv1.Namespace
	VICEDomain    string
	JobInfo       jobinfo.JobInfo
}

func (c *CommonHTTPRouteBuilder) BuildRoute(
	ctx context.Context,
	job *model.Job,
	svc *apiv1.Service,
) (*gatewayv1.HTTPRoute, error) {
	var rules []gatewayv1.HTTPRouteRule
	var defaultPort gatewayv1.PortNumber

	// Get the route metadata.
	labels, err := c.JobInfo.JobLabels(ctx, job)
	if err != nil {
		return nil, err
	}
	subdomain := common.Subdomain(job.UserID, job.InvocationID)
	hostname := gatewayv1.Hostname(fmt.Sprintf("%s.%s", subdomain, c.VICEDomain))

	// Find the proxy port and set it as the default.
	for _, port := range svc.Spec.Ports {
		if port.Name == constants.VICEProxyPortName {
			defaultPort = gatewayv1.PortNumber(port.Port)
		}
	}

	// Verify that the default port was set.
	if defaultPort == 0 {
		return nil, fmt.Errorf("port %s was not found in the service", constants.VICEProxyPortName)
	}

	// Define the route rules.
	rules = []gatewayv1.HTTPRouteRule{
		gatewayv1.HTTPRouteRule{
			BackendRefs: []gatewayv1.HTTPBackendRef{
				gatewayv1.HTTPBackendRef{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: gatewayv1.ObjectName(svc.Name),
							Port: &defaultPort,
						},
					},
				},
			},
		},
	}

	// Define and return the route.
	route := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:   job.InvocationID,
			Labels: labels,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					gatewayv1.ParentReference{
						Namespace: &c.VICENamespace,
						Name:      VICEGatewayName,
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{hostname},
			Rules:     rules,
		},
	}

	return &route, nil
}

// NewHTTPRouteBuilder creates a new HTTPRouteBuilder for the given provider.
func NewHTTPRouteBuilder(provider, viceNamespace, viceDomain string, jobInfo jobinfo.JobInfo) HTTPRouteBuilder {
	// Fall back to CommonHTTPRouteBuilder if there's no custom HTTPRouteBuilder for the provider.
	return &CommonHTTPRouteBuilder{
		Provider:      provider,
		VICENamespace: gatewayv1.Namespace(viceNamespace),
		VICEDomain:    viceDomain,
		JobInfo:       jobInfo,
	}
}
