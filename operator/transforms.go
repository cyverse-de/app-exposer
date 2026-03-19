// Package operator implements the vice-operator, which receives pre-built K8s
// resource bundles from app-exposer and applies them to the local cluster,
// transforming routing resources as needed for the local environment.
package operator

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
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

// TransformBackendToLoadingService rewrites the HTTPRoute backend service
// references to point at the vice-operator loading page service. Called in
// HandleLaunch so initial traffic routes to the loading page instead of the
// (not-yet-ready) analysis.
func TransformBackendToLoadingService(route *gatewayv1.HTTPRoute, serviceName string, servicePort int32) {
	if route == nil {
		return
	}
	port := gatewayv1.PortNumber(servicePort)
	name := gatewayv1.ObjectName(serviceName)
	for i := range route.Spec.Rules {
		for j := range route.Spec.Rules[i].BackendRefs {
			route.Spec.Rules[i].BackendRefs[j].Name = name
			route.Spec.Rules[i].BackendRefs[j].Port = &port
		}
	}
}

// TransformHostnames rewrites the domain portion of hostnames in the HTTPRoute
// to match baseDomain. For example, if baseDomain is "localhost", then
// "a1234.cyverse.run" becomes "a1234.localhost". No-op if baseDomain is empty.
func TransformHostnames(route *gatewayv1.HTTPRoute, baseDomain string) {
	if route == nil || baseDomain == "" {
		return
	}
	for i, h := range route.Spec.Hostnames {
		route.Spec.Hostnames[i] = gatewayv1.Hostname(rewriteHostname(string(h), baseDomain))
	}
}

// rewriteHostname replaces the domain portion of a hostname (everything after
// the first dot) with newDomain. Hostnames with no dot are returned unchanged.
func rewriteHostname(hostname, newDomain string) string {
	dot := strings.IndexByte(hostname, '.')
	if dot < 0 {
		return hostname
	}
	return hostname[:dot+1] + newDomain
}

// TransformGatewayNamespace rewrites the parentRef namespace in an HTTPRoute
// to match the operator's namespace. No-op if namespace is empty or route is nil.
func TransformGatewayNamespace(route *gatewayv1.HTTPRoute, namespace string) {
	if route == nil || namespace == "" {
		return
	}
	ns := gatewayv1.Namespace(namespace)
	for i := range route.Spec.ParentRefs {
		route.Spec.ParentRefs[i].Namespace = &ns
	}
}
