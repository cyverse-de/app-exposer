package operator

import (
	"context"
	"fmt"

	"github.com/cyverse-de/app-exposer/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// SwapRoute updates the HTTPRoute for the given analysis to point at the
// analysis Service instead of the loading page service. The operation is
// idempotent — calling it when the route already points at the analysis
// service is a no-op (the same values are written).
func (o *Operator) SwapRoute(ctx context.Context, analysisID string) error {
	opts := analysisLabelSelector(analysisID)

	// Find the analysis Service name.
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return fmt.Errorf("listing services for analysis %s: %w", analysisID, err)
	}
	if len(svcs.Items) == 0 {
		return fmt.Errorf("no service found for analysis %s", analysisID)
	}
	svc := svcs.Items[0]
	targetSvcName := svc.Name

	// Find the vice-proxy port by name rather than assuming port order,
	// since the service has multiple ports (file transfers and vice-proxy).
	var targetPort int32
	for _, p := range svc.Spec.Ports {
		if p.Name == constants.VICEProxyPortName {
			targetPort = p.Port
			break
		}
	}
	if targetPort == 0 {
		return fmt.Errorf("service %s has no port named %s", targetSvcName, constants.VICEProxyPortName)
	}

	log.Infof("swapping route for analysis %s to service %s:%d", analysisID, targetSvcName, targetPort)

	// Swap HTTPRoute backend refs to the analysis service.
	routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
	if err != nil {
		return fmt.Errorf("listing HTTPRoutes: %w", err)
	}
	if len(routes.Items) == 0 {
		return fmt.Errorf("no HTTPRoute found matching selector; cannot swap route")
	}

	port := gatewayv1.PortNumber(targetPort)
	name := gatewayv1.ObjectName(targetSvcName)
	for _, route := range routes.Items {
		for i := range route.Spec.Rules {
			for j := range route.Spec.Rules[i].BackendRefs {
				route.Spec.Rules[i].BackendRefs[j].Name = name
				route.Spec.Rules[i].BackendRefs[j].Port = &port
			}
		}
		if _, err := o.gatewayClient.HTTPRoutes(o.namespace).Update(ctx, &route, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("updating HTTPRoute %s: %w", route.Name, err)
		}
	}

	log.Infof("route swap complete for analysis %s", analysisID)
	return nil
}
