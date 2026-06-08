package vicebuild

import (
	"fmt"

	"github.com/cyverse-de/app-exposer/operatorclient"
	apiv1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
)

// GPU resource names by canonical vendor. vicebuild emits the cluster vendor's
// name directly, folding in the old TransformGPUVendor rename.
const (
	gpuResourceNvidia = "nvidia.com/gpu"
	gpuResourceAMD    = "amd.com/gpu"
)

// gpuResourceName returns the extended-resource name for the cluster's GPU
// vendor. Defaults to the NVIDIA name for an empty/unknown vendor, matching the
// legacy path where bundles always carried nvidia.com/gpu.
func gpuResourceName(vendor string) apiv1.ResourceName {
	if vendor == operatorclient.GPUVendorAMD {
		return apiv1.ResourceName(gpuResourceAMD)
	}
	return apiv1.ResourceName(gpuResourceNvidia)
}

// analysisRequirements builds the analysis container's ResourceRequirements from
// the raw asks in the spec and the cluster's default/clamp policy. It mirrors
// resourcing.Requirements but reads resolved primitives instead of a
// model.Analysis, and emits the cluster's GPU resource name with requests equal
// to limits (folding in the old equalizeGPUResources, since spec.GPU.Count is
// already the resolved effective count).
func (c *Config) analysisRequirements(spec *operatorclient.VICESpec) apiv1.ResourceRequirements {
	d := c.Resources
	reqs := apiv1.ResourceList{
		apiv1.ResourceCPU:              cpuQuantity(spec.Resources.MinCPUCores, d.DefaultCPURequest),
		apiv1.ResourceMemory:           bytesQuantity(spec.Resources.MinMemoryBytes, d.DefaultMemRequest),
		apiv1.ResourceEphemeralStorage: bytesQuantity(spec.Resources.MinDiskBytes, d.DefaultStorage),
	}
	limits := apiv1.ResourceList{}
	if d.DoCPULimit {
		limits[apiv1.ResourceCPU] = cpuQuantity(spec.Resources.MaxCPUCores, d.DefaultCPULimit)
	}
	if d.DoMemLimit {
		limits[apiv1.ResourceMemory] = bytesQuantity(spec.Resources.MaxMemoryBytes, d.DefaultMemLimit)
	}

	if spec.GPU != nil {
		name := gpuResourceName(c.GPUVendor)
		count := spec.GPU.Count
		if count <= 0 {
			count = 1
		}
		// Parse from the integer string (rather than NewQuantity) so the
		// quantity's cached string form matches what the analysis definition
		// path produces — keeps the emitted resource identical to the legacy
		// build, which the golden-equivalence test asserts.
		qty, err := resourcev1.ParseQuantity(fmt.Sprintf("%d", count))
		if err != nil {
			log.Warnf("malformed GPU quantity %d: %v; defaulting to 1", count, err)
			qty = resourcev1.MustParse("1")
		}
		reqs[name] = qty
		limits[name] = qty
	}

	return apiv1.ResourceRequirements{Requests: reqs, Limits: limits}
}

// viceProxyRequirements builds the vice-proxy sidecar's ResourceRequirements
// entirely from cluster config — the sidecar's resources never depend on the
// analysis (resourcing.VICEProxyRequirements ignored it too).
func (c *Config) viceProxyRequirements() apiv1.ResourceRequirements {
	d := c.Resources
	out := apiv1.ResourceRequirements{
		Requests: apiv1.ResourceList{
			apiv1.ResourceCPU:              d.ViceProxyCPURequest,
			apiv1.ResourceMemory:           d.ViceProxyMemRequest,
			apiv1.ResourceEphemeralStorage: d.ViceProxyStorage,
		},
	}
	limits := apiv1.ResourceList{}
	if d.DoViceProxyCPULimit {
		limits[apiv1.ResourceCPU] = d.ViceProxyCPULimit
	}
	if d.DoViceProxyMemLimit {
		limits[apiv1.ResourceMemory] = d.ViceProxyMemLimit
	}
	if d.DoViceProxyStorage {
		limits[apiv1.ResourceEphemeralStorage] = d.ViceProxyStorageLim
	}
	if len(limits) > 0 {
		out.Limits = limits
	}
	return out
}

// cpuQuantity converts a CPU-cores ask to a milli-core Quantity, matching the
// "%fm" formatting resourcing used. A zero ask falls back to the cluster default.
func cpuQuantity(cores float32, fallback resourcev1.Quantity) resourcev1.Quantity {
	if cores == 0 {
		return fallback
	}
	q, err := resourcev1.ParseQuantity(fmt.Sprintf("%fm", cores*1000))
	if err != nil {
		log.Warnf("malformed CPU quantity from %f cores: %v; using cluster default", cores, err)
		return fallback
	}
	return q
}

// bytesQuantity converts a byte ask to a Quantity, matching resourcing's "%d"
// formatting. A zero ask falls back to the cluster default.
func bytesQuantity(bytes int64, fallback resourcev1.Quantity) resourcev1.Quantity {
	if bytes == 0 {
		return fallback
	}
	q, err := resourcev1.ParseQuantity(fmt.Sprintf("%d", bytes))
	if err != nil {
		log.Warnf("malformed byte quantity from %d: %v; using cluster default", bytes, err)
		return fallback
	}
	return q
}
