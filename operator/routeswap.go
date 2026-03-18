package operator

import (
	"context"
	"fmt"

	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// SwapRoute updates the HTTPRoute or Ingress for the given analysis to point
// at the analysis Service instead of the loading page service. The operation
// is idempotent — calling it when the route already points at the analysis
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
	targetSvcName := svcs.Items[0].Name
	var targetPort int32 = 80
	if len(svcs.Items[0].Spec.Ports) > 0 {
		targetPort = svcs.Items[0].Spec.Ports[0].Port
	}

	log.Infof("swapping route for analysis %s to service %s:%d", analysisID, targetSvcName, targetPort)

	// Swap based on routing type to avoid touching resources that don't apply.
	switch o.routingType {
	case RoutingGateway:
		if !o.hasGatewayClient() {
			return fmt.Errorf("routing type is gateway but no gateway API client is configured")
		}
		if err := o.swapHTTPRouteBackend(ctx, opts, targetSvcName, targetPort); err != nil {
			return err
		}
	case RoutingNginx, RoutingTailscale:
		if err := o.swapIngressBackend(ctx, opts, targetSvcName, targetPort); err != nil {
			return err
		}
	}

	log.Infof("route swap complete for analysis %s", analysisID)
	return nil
}

// swapHTTPRouteBackend rewrites all BackendRef entries in HTTPRoutes matching
// opts to point at svcName:svcPort.
func (o *Operator) swapHTTPRouteBackend(ctx context.Context, opts metav1.ListOptions, svcName string, svcPort int32) error {
	routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
	if err != nil {
		return fmt.Errorf("listing HTTPRoutes: %w", err)
	}
	if len(routes.Items) == 0 {
		return fmt.Errorf("no HTTPRoute found matching selector; cannot swap route")
	}

	port := gatewayv1.PortNumber(svcPort)
	name := gatewayv1.ObjectName(svcName)
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
	return nil
}

// swapIngressBackend rewrites DefaultBackend and all HTTP path backends in
// Ingresses matching opts to point at svcName:svcPort.
func (o *Operator) swapIngressBackend(ctx context.Context, opts metav1.ListOptions, svcName string, svcPort int32) error {
	ings, err := o.clientset.NetworkingV1().Ingresses(o.namespace).List(ctx, opts)
	if err != nil {
		return fmt.Errorf("listing Ingresses: %w", err)
	}
	if len(ings.Items) == 0 {
		return fmt.Errorf("no Ingress found matching selector; cannot swap route")
	}

	for _, ing := range ings.Items {
		if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
			ing.Spec.DefaultBackend.Service.Name = svcName
			ing.Spec.DefaultBackend.Service.Port = netv1.ServiceBackendPort{Number: svcPort}
		}
		for i := range ing.Spec.Rules {
			if ing.Spec.Rules[i].HTTP == nil {
				continue
			}
			for j := range ing.Spec.Rules[i].HTTP.Paths {
				if ing.Spec.Rules[i].HTTP.Paths[j].Backend.Service != nil {
					ing.Spec.Rules[i].HTTP.Paths[j].Backend.Service.Name = svcName
					ing.Spec.Rules[i].HTTP.Paths[j].Backend.Service.Port = netv1.ServiceBackendPort{Number: svcPort}
				}
			}
		}
		if _, err := o.clientset.NetworkingV1().Ingresses(o.namespace).Update(ctx, &ing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("updating Ingress %s: %w", ing.Name, err)
		}
	}
	return nil
}
