// Package operator implements the vice-operator, which receives pre-built K8s
// resource bundles from app-exposer and applies them to the local cluster,
// transforming routing resources as needed for the local environment.
package operator

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GPU resource and affinity key constants used by TransformGPUVendor.
const (
	nvidiaGPUResource    apiv1.ResourceName = "nvidia.com/gpu"
	amdGPUResource       apiv1.ResourceName = "amd.com/gpu"
	nvidiaModelAffinityK                    = "nvidia.com/gpu.product"
	// amdModelAffinityK mirrors the NVIDIA convention; verify against actual
	// AMD device plugin labels in the target cluster.
	amdModelAffinityK = "amd.com/gpu.product"
)

// GPUVendor describes which GPU vendor the operator's cluster uses.
type GPUVendor string

const (
	// GPUVendorNvidia represents NVIDIA GPU hardware (the default; bundles
	// arrive pre-configured for NVIDIA).
	GPUVendorNvidia GPUVendor = "nvidia"

	// GPUVendorAMD represents AMD GPU hardware.
	GPUVendorAMD GPUVendor = "amd"
)

// ParseGPUVendor validates and returns a GPUVendor from a string.
// Returns an error for unrecognized values.
func ParseGPUVendor(s string) (GPUVendor, error) {
	switch GPUVendor(s) {
	case GPUVendorNvidia, GPUVendorAMD:
		return GPUVendor(s), nil
	default:
		return "", fmt.Errorf("unknown GPU vendor %q (valid: nvidia, amd)", s)
	}
}

// TransformGPUVendor rewrites GPU-specific resource names in all containers
// (including init containers) and node affinity keys to match the target vendor.
// Bundles arrive with NVIDIA references; when vendor is AMD, those are rewritten
// to the AMD equivalents. Modifies the deployment in place.
func TransformGPUVendor(deployment *appsv1.Deployment, vendor GPUVendor) {
	if deployment == nil || vendor == GPUVendorNvidia {
		return
	}

	// Rewrite container resource requests and limits.
	for i := range deployment.Spec.Template.Spec.Containers {
		renameGPUResource(&deployment.Spec.Template.Spec.Containers[i].Resources)
	}
	for i := range deployment.Spec.Template.Spec.InitContainers {
		renameGPUResource(&deployment.Spec.Template.Spec.InitContainers[i].Resources)
	}

	// Rewrite node affinity keys in both required and preferred terms.
	affinity := deployment.Spec.Template.Spec.Affinity
	if affinity == nil || affinity.NodeAffinity == nil {
		return
	}
	if req := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution; req != nil {
		for i := range req.NodeSelectorTerms {
			rewriteMatchExpressions(req.NodeSelectorTerms[i].MatchExpressions)
		}
	}
	for i := range affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
		rewriteMatchExpressions(affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution[i].Preference.MatchExpressions)
	}
}

// rewriteMatchExpressions renames nvidia.com/gpu.product keys to the AMD
// equivalent in a slice of NodeSelectorRequirements.
func rewriteMatchExpressions(exprs []apiv1.NodeSelectorRequirement) {
	for i := range exprs {
		if exprs[i].Key == nvidiaModelAffinityK {
			exprs[i].Key = amdModelAffinityK
		}
	}
}

// renameGPUResource replaces nvidia.com/gpu with amd.com/gpu in both the
// Requests and Limits maps of a ResourceRequirements.
func renameGPUResource(res *apiv1.ResourceRequirements) {
	swapResource(res.Requests)
	swapResource(res.Limits)
}

// swapResource moves a resource quantity from the NVIDIA key to the AMD key.
func swapResource(rl apiv1.ResourceList) {
	if rl == nil {
		return
	}
	if qty, ok := rl[nvidiaGPUResource]; ok {
		rl[amdGPUResource] = qty.DeepCopy()
		delete(rl, nvidiaGPUResource)
	}
}

// RoutingType describes which networking approach the operator's cluster uses.
type RoutingType string

const (
	// RoutingGateway applies the HTTPRoute directly via the Gateway API.
	RoutingGateway RoutingType = "gateway"

	// RoutingNginx converts the HTTPRoute to an nginx Ingress.
	RoutingNginx RoutingType = "nginx"

	// RoutingTailscale converts the HTTPRoute to a Tailscale Ingress.
	RoutingTailscale RoutingType = "tailscale"
)

// ParseRoutingType validates and returns a RoutingType from a string.
// Returns an error for unrecognized values.
func ParseRoutingType(s string) (RoutingType, error) {
	switch RoutingType(s) {
	case RoutingGateway, RoutingNginx, RoutingTailscale:
		return RoutingType(s), nil
	default:
		return "", fmt.Errorf("unknown routing type %q (valid: gateway, nginx, tailscale)", s)
	}
}

// TransformRouting converts the bundle's HTTPRoute into the appropriate
// networking resource for the operator's cluster. Returns at most one non-nil
// resource: either an HTTPRoute (for gateway routing) or an Ingress (for
// nginx/tailscale routing).
func TransformRouting(
	route *gatewayv1.HTTPRoute,
	routing RoutingType,
	ingressClass string,
) (*gatewayv1.HTTPRoute, *netv1.Ingress) {
	if route == nil {
		return nil, nil
	}

	switch routing {
	case RoutingGateway:
		return route, nil

	case RoutingNginx:
		ingress := httpRouteToIngress(route, ingressClass)
		return nil, ingress

	case RoutingTailscale:
		ingress := httpRouteToIngress(route, ingressClass)
		// Remove nginx-specific annotations from the converted ingress.
		for key := range ingress.Annotations {
			if isNginxAnnotation(key) {
				delete(ingress.Annotations, key)
			}
		}
		return nil, ingress

	default:
		// Unknown routing type: treat as gateway passthrough.
		return route, nil
	}
}

// httpRouteToIngress converts a Gateway API HTTPRoute into a K8s Ingress.
// It extracts hostnames, backend service references, and ports from the
// HTTPRoute spec and maps them to Ingress rules and paths.
func httpRouteToIngress(route *gatewayv1.HTTPRoute, ingressClass string) *netv1.Ingress {
	pathType := netv1.PathTypePrefix

	var rules []netv1.IngressRule
	var defaultBackendName string
	var defaultBackendPort int32

	// Extract backend service info from the first rule's first backend ref.
	if len(route.Spec.Rules) > 0 && len(route.Spec.Rules[0].BackendRefs) > 0 {
		ref := route.Spec.Rules[0].BackendRefs[0]
		defaultBackendName = string(ref.Name)
		if ref.Port != nil {
			defaultBackendPort = int32(*ref.Port)
		}
	}

	// Create an ingress rule per hostname.
	for _, hostname := range route.Spec.Hostnames {
		rules = append(rules, netv1.IngressRule{
			Host: string(hostname),
			IngressRuleValue: netv1.IngressRuleValue{
				HTTP: &netv1.HTTPIngressRuleValue{
					Paths: []netv1.HTTPIngressPath{
						{
							Path:     "/",
							PathType: &pathType,
							Backend: netv1.IngressBackend{
								Service: &netv1.IngressServiceBackend{
									Name: defaultBackendName,
									Port: netv1.ServiceBackendPort{
										Number: defaultBackendPort,
									},
								},
							},
						},
					},
				},
			},
		})
	}

	// Build a default backend if we have a service name.
	var defaultBackend *netv1.IngressBackend
	if defaultBackendName != "" {
		defaultBackend = &netv1.IngressBackend{
			Service: &netv1.IngressServiceBackend{
				Name: defaultBackendName,
				Port: netv1.ServiceBackendPort{
					Number: defaultBackendPort,
				},
			},
		}
	}

	return &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      route.Name,
			Namespace: route.Namespace,
			Labels:    route.Labels,
			Annotations: map[string]string{
				"converted-from": fmt.Sprintf("httproute/%s", route.Name),
			},
		},
		Spec: netv1.IngressSpec{
			IngressClassName: &ingressClass,
			DefaultBackend:   defaultBackend,
			Rules:            rules,
		},
	}
}

// TransformIngress converts the bundle's Ingress to match the operator's local
// routing configuration. Retained for backward compatibility with bundles that
// still contain an Ingress instead of an HTTPRoute.
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
