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

// TransformViceProxyArgs injects per-analysis command-line args, ensures the
// cluster-config-secret envFrom is present, and ensures the permissions volume
// mount exists on the vice-proxy container.
// The container's Command should already be ["vice-proxy"] from app-exposer;
// Args appends to the entrypoint. The backend URL is derived from the first
// port of the analysis container.
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
}

// EnsurePermissionsConfigMap adds a permissions ConfigMap to the bundle if one
// isn't already present. This handles bundles created before app-exposer added
// the permissions ConfigMap at build time. The ConfigMap is seeded with the
// owner username from the deployment's "username" label.
func EnsurePermissionsConfigMap(bundle *operatorclient.AnalysisBundle) {
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
	owner := bundle.Deployment.Labels["username"]
	if owner == "" {
		log.Warnf("deployment %s missing username label; skipping permissions ConfigMap creation", bundle.Deployment.Name)
		return
	}
	owner += constants.UserSuffix

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
// deployment name (which is the invocation ID). This handles bundles that
// were created before the permissions volume was added at the app-exposer level.
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
// container if it isn't already present. This handles bundles created before
// the mount was added at the app-exposer level.
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
