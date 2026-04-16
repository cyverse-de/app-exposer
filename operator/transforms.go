// Package operator implements the vice-operator, which receives pre-built K8s
// resource bundles from app-exposer and applies them to the local cluster,
// transforming routing resources as needed for the local environment.
package operator

import (
	"fmt"
	"strings"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GPU resource and affinity key constants used by TransformGPUVendor.
const (
	nvidiaGPUResource    apiv1.ResourceName = "nvidia.com/gpu"
	amdGPUResource       apiv1.ResourceName = "amd.com/gpu"
	nvidiaModelAffinityK                    = "nvidia.com/gpu.product"
	// amdModelAffinityK is the node label a bundle's nodeAffinity rules
	// target when TransformGPUVendor flips the vendor to AMD. If a cluster
	// uses a different key, that cluster's GPU-model-specific affinity
	// rules will fail to match and analyses will land on any AMD node
	// rather than a specific model — loud enough to notice in scheduling
	// events, so there's no need to defend against it here.
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
// to the AMD equivalents. For all vendors, GPU requests are equalized to match
// limits since K8s requires requests == limits for extended resources (GPUs
// cannot be overcommitted). Modifies the deployment in place.
func TransformGPUVendor(deployment *appsv1.Deployment, vendor GPUVendor) {
	if deployment == nil {
		return
	}

	gpuKey := nvidiaGPUResource

	// AMD clusters need NVIDIA resource names and affinity keys rewritten.
	if vendor == GPUVendorAMD {
		gpuKey = amdGPUResource

		forEachContainerResources(deployment, func(res *apiv1.ResourceRequirements) {
			renameGPUResource(res)
		})

		rewriteNodeAffinityKeys(deployment.Spec.Template.Spec.Affinity)
	}

	// K8s requires requests == limits for extended resources like GPUs
	// because they are discrete devices that cannot be overcommitted.
	// See https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources
	forEachContainerResources(deployment, func(res *apiv1.ResourceRequirements) {
		equalizeGPUResources(res, gpuKey)
	})
}

// forEachContainerResources calls fn on the ResourceRequirements of every
// container and init container in the deployment.
func forEachContainerResources(deployment *appsv1.Deployment, fn func(*apiv1.ResourceRequirements)) {
	for i := range deployment.Spec.Template.Spec.Containers {
		fn(&deployment.Spec.Template.Spec.Containers[i].Resources)
	}
	for i := range deployment.Spec.Template.Spec.InitContainers {
		fn(&deployment.Spec.Template.Spec.InitContainers[i].Resources)
	}
}

// rewriteNodeAffinityKeys rewrites NVIDIA affinity keys to AMD equivalents
// in both required and preferred node affinity terms.
func rewriteNodeAffinityKeys(affinity *apiv1.Affinity) {
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

// equalizeGPUResources ensures GPU requests == limits for the given resource
// key. Kubernetes requires requests to equal limits for extended resources
// (like GPUs) because they are discrete devices that cannot be overcommitted.
// If the analysis definition has mismatched values (e.g. requests=1, limits=2),
// both are set to the lower of the two, since the request represents the
// actual number of GPUs needed.
//
// See:
//   - https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources
//   - https://kubernetes.io/docs/tasks/manage-gpus/scheduling-gpus/#using-device-plugins
func equalizeGPUResources(res *apiv1.ResourceRequirements, gpuKey apiv1.ResourceName) {
	limQty, hasLim := res.Limits[gpuKey]
	reqQty, hasReq := res.Requests[gpuKey]

	switch {
	case hasLim && hasReq && !limQty.Equal(reqQty):
		// Both set but mismatched — use the lower value since the request
		// reflects the actual number of GPUs needed.
		minQty := reqQty.DeepCopy()
		if limQty.Cmp(reqQty) < 0 {
			minQty = limQty.DeepCopy()
		}
		res.Requests[gpuKey] = minQty
		res.Limits[gpuKey] = minQty

	case hasLim && !hasReq:
		// Only limits set — copy to requests so K8s accepts the pod.
		if res.Requests == nil {
			res.Requests = make(apiv1.ResourceList)
		}
		res.Requests[gpuKey] = limQty.DeepCopy()

	case hasReq && !hasLim:
		// Only requests set — copy to limits so K8s accepts the pod.
		if res.Limits == nil {
			res.Limits = make(apiv1.ResourceList)
		}
		res.Limits[gpuKey] = reqQty.DeepCopy()
	}
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

// TransformViceProxyArgs injects per-analysis command-line args, ensures the
// cluster-config-secret envFrom is present, and ensures the permissions volume
// mount exists on the vice-proxy container.
// The container's Command should already be ["vice-proxy"] from app-exposer;
// Args is replaced with operator-specific flags. The backend URL is derived
// from the first port of the analysis container.
func TransformViceProxyArgs(deployment *appsv1.Deployment, analysisID, clusterConfigSecret string) {
	if deployment == nil {
		return
	}
	for i, c := range deployment.Spec.Template.Spec.Containers {
		if c.Name != constants.VICEProxyContainerName {
			continue
		}
		backendURL := deriveBackendURL(deployment)
		deployment.Spec.Template.Spec.Containers[i].Args = []string{
			"--analysis-id", analysisID,
			"--backend-url", backendURL,
			"--ws-backend-url", backendURL,
			"--listen-addr", fmt.Sprintf("0.0.0.0:%d", constants.VICEProxyPort),
		}

		// Ensure the cluster config secret is referenced as envFrom so
		// vice-proxy gets cluster-level env vars (VICE_BASE_URL, Keycloak, etc.).
		if clusterConfigSecret != "" {
			ensureEnvFrom(&deployment.Spec.Template.Spec.Containers[i], clusterConfigSecret)
		}

		// Ensure the permissions volume and mount are present so vice-proxy
		// can read the allowed-users file from the ConfigMap. The volume
		// must exist in the pod spec for the mount to be valid.
		ensurePermissionsVolume(deployment)
		ensurePermissionsVolumeMount(&deployment.Spec.Template.Spec.Containers[i])
		return
	}
	log.Warnf("deployment %s: no container named %q found; vice-proxy args not injected",
		deployment.Name, constants.VICEProxyContainerName)
}

// EnsurePermissionsConfigMap adds a permissions ConfigMap to the bundle if one
// isn't already present, so every analysis has a consistent permissions
// surface regardless of how the bundle was assembled upstream. The
// ConfigMap is seeded with the owner username from the deployment's
// "username" label.
func EnsurePermissionsConfigMap(bundle *operatorclient.AnalysisBundle, userSuffix string) {
	if bundle == nil || bundle.Deployment == nil {
		return
	}

	prefix := constants.PermissionsConfigMapPrefix + "-"
	for _, cm := range bundle.ConfigMaps {
		if cm != nil && strings.HasPrefix(cm.Name, prefix) {
			return // already present
		}
	}

	// Derive the owner from the deployment labels. If the label is missing,
	// skip creating the ConfigMap to avoid locking everyone out of the analysis.
	// The label may arrive unsanitized (with @) or sanitized (with -); only
	// append the suffix if it's not already present.
	owner := bundle.Deployment.Labels["username"]
	if owner == "" {
		log.Warnf("deployment %s missing username label; skipping permissions ConfigMap creation", bundle.Deployment.Name)
		return
	}
	if !strings.HasSuffix(owner, userSuffix) {
		owner += userSuffix
	}

	cmName := fmt.Sprintf("%s-%s", constants.PermissionsConfigMapPrefix, bundle.Deployment.Name)
	cm := &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   cmName,
			Labels: bundle.Deployment.Labels,
		},
		Data: map[string]string{
			constants.PermissionsFileName: owner + "\n",
		},
	}
	bundle.ConfigMaps = append(bundle.ConfigMaps, cm)
}

// ensurePermissionsVolume adds the permissions ConfigMap volume to the pod
// spec if it isn't already present. The ConfigMap name is derived from the
// deployment name (which is the invocation ID). This keeps the volume
// attached even for bundles that arrive without one.
func ensurePermissionsVolume(deployment *appsv1.Deployment) {
	volumes := deployment.Spec.Template.Spec.Volumes
	for _, v := range volumes {
		if v.Name == constants.PermissionsVolumeName {
			return // already present
		}
	}
	cmName := fmt.Sprintf("%s-%s", constants.PermissionsConfigMapPrefix, deployment.Name)
	deployment.Spec.Template.Spec.Volumes = append(volumes, apiv1.Volume{
		Name: constants.PermissionsVolumeName,
		VolumeSource: apiv1.VolumeSource{
			ConfigMap: &apiv1.ConfigMapVolumeSource{
				LocalObjectReference: apiv1.LocalObjectReference{
					Name: cmName,
				},
			},
		},
	})
}

// ensurePermissionsVolumeMount adds the permissions volume mount to the
// container if it isn't already present, pairing with ensurePermissionsVolume
// to keep the permissions file visible inside the analysis container.
func ensurePermissionsVolumeMount(container *apiv1.Container) {
	for _, vm := range container.VolumeMounts {
		if vm.Name == constants.PermissionsVolumeName {
			return // already present
		}
	}
	container.VolumeMounts = append(container.VolumeMounts, apiv1.VolumeMount{
		Name:      constants.PermissionsVolumeName,
		MountPath: constants.PermissionsMountPath,
		ReadOnly:  true,
	})
}

// ensureEnvFrom adds an envFrom secretRef for the given secret name if it
// isn't already present on the container.
func ensureEnvFrom(container *apiv1.Container, secretName string) {
	for _, ref := range container.EnvFrom {
		if ref.SecretRef != nil && ref.SecretRef.Name == secretName {
			return // already present
		}
	}
	optional := true
	container.EnvFrom = append(container.EnvFrom, apiv1.EnvFromSource{
		SecretRef: &apiv1.SecretEnvSource{
			LocalObjectReference: apiv1.LocalObjectReference{Name: secretName},
			Optional:             &optional,
		},
	})
}

// deriveBackendURL finds the analysis container by name and returns
// http://localhost:<first-port>. Falls back to http://localhost:60000
// with a warning log so the misconfiguration is visible.
func deriveBackendURL(deployment *appsv1.Deployment) string {
	for _, c := range deployment.Spec.Template.Spec.Containers {
		if c.Name == constants.AnalysisContainerName && len(c.Ports) > 0 {
			return fmt.Sprintf("http://localhost:%d", c.Ports[0].ContainerPort)
		}
	}
	log.Warnf("deployment %s: analysis container %q not found or has no ports; falling back to localhost:60000",
		deployment.Name, constants.AnalysisContainerName)
	return "http://localhost:60000"
}

// TransformGatewayNamespace rewrites the parentRef namespace and name in an HTTPRoute
// to match the operator's configured gateway. No-op if route is nil.
func TransformGatewayNamespace(route *gatewayv1.HTTPRoute, namespace, name string) {
	if route == nil {
		return
	}
	ns := gatewayv1.Namespace(namespace)
	objName := gatewayv1.ObjectName(name)
	for i := range route.Spec.ParentRefs {
		if namespace != "" {
			route.Spec.ParentRefs[i].Namespace = &ns
		}
		if name != "" {
			route.Spec.ParentRefs[i].Name = objName
		}
	}
}
