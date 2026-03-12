// Package operator implements the vice-operator, which receives pre-built K8s
// resource bundles from app-exposer and applies them to the local cluster,
// transforming routing resources as needed for the local environment.
package operator

import (
	"strings"

	netv1 "k8s.io/api/networking/v1"
)

// RoutingType describes which ingress controller the operator's cluster uses.
type RoutingType string

const (
	// RoutingNginx uses the standard nginx ingress controller.
	RoutingNginx RoutingType = "nginx"

	// RoutingTailscale uses the Tailscale ingress controller.
	RoutingTailscale RoutingType = "tailscale"
)

// TransformIngress converts the bundle's Ingress to match the operator's local
// routing configuration. This is a pure in-memory data transformation with no
// K8s API calls.
func TransformIngress(ingress *netv1.Ingress, targetRouting RoutingType, targetIngressClass string) *netv1.Ingress {
	if ingress == nil {
		return nil
	}

	// If the target is the same as what the ingress already has, no changes needed.
	if ingress.Spec.IngressClassName != nil && *ingress.Spec.IngressClassName == targetIngressClass {
		return ingress
	}

	switch targetRouting {
	case RoutingTailscale:
		return transformToTailscale(ingress, targetIngressClass)
	default:
		// For nginx or any other type, just update the ingress class name.
		ingress.Spec.IngressClassName = &targetIngressClass
		return ingress
	}
}

// transformToTailscale removes nginx-specific annotations and sets the
// Tailscale ingress class on the provided Ingress.
func transformToTailscale(ingress *netv1.Ingress, ingressClass string) *netv1.Ingress {
	// Remove all nginx-specific annotations.
	for key := range ingress.Annotations {
		if isNginxAnnotation(key) {
			delete(ingress.Annotations, key)
		}
	}

	ingress.Spec.IngressClassName = &ingressClass

	return ingress
}

// isNginxAnnotation returns true if the annotation key is nginx-specific.
func isNginxAnnotation(key string) bool {
	return strings.HasPrefix(key, "nginx.ingress.kubernetes.io/")
}
