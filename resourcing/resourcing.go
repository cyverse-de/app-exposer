package resourcing

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/model/v10"
	apiv1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
)

var log = common.Log

// mustQuantity parses a hard-coded resource quantity literal; panics if the
// literal is malformed. Used for the package-level defaults below — the
// inputs are compile-time constants, so a parse error is a programmer bug
// and should fail the binary at startup rather than silently producing a
// zero-valued Quantity.
func mustQuantity(s string) resourcev1.Quantity {
	q, err := resourcev1.ParseQuantity(s)
	if err != nil {
		panic(fmt.Sprintf("resourcing: malformed quantity literal %q: %v", s, err))
	}
	return q
}

var (
	defaultCPUResourceRequest   = mustQuantity("1000m")
	defaultMemResourceRequest   = mustQuantity("2Gi")
	defaultStorageRequest       = mustQuantity("1Gi")
	defaultCPUResourceLimit     = mustQuantity("2000m")
	defaultMemResourceLimit     = mustQuantity("8Gi")
	viceProxyCPUResourceRequest = mustQuantity("100m")
	viceProxyMemResourceRequest = mustQuantity("100Mi")
	viceProxyStorageRequest     = mustQuantity("100Mi")
	viceProxyCPUResourceLimit   = mustQuantity("200m")
	viceProxyMemResourceLimit   = mustQuantity("200Mi")
	viceProxyStorageLimit       = mustQuantity("200Mi")

	doDefaultCPUResourceLimit   = true
	doDefaultMemResourceLimit   = true
	doVICEProxyCPUResourceLimit = true
	doVICEProxyMemResourceLimit = true
	doVICEProxyStorageLimit     = true
)

// SetDefaultCPUResourceRequest sets the default CPU resource request for analyses.
func SetDefaultCPUResourceRequest(value resourcev1.Quantity) {
	defaultCPUResourceRequest = value
}

// SetDefaultCPUResourceLimit sets the default CPU resource limit for analyses.
func SetDefaultCPUResourceLimit(value resourcev1.Quantity) {
	defaultCPUResourceLimit = value
}

// SetDoDefaultCPUResourceLimit controls whether a default CPU resource limit
// is applied to analyses.
func SetDoDefaultCPUResourceLimit(value bool) {
	doDefaultCPUResourceLimit = value
}

// SetDefaultMemResourceRequest sets the default memory resource request for analyses.
func SetDefaultMemResourceRequest(value resourcev1.Quantity) {
	defaultMemResourceRequest = value
}

// SetDefaultMemResourceLimit sets the default memory resource limit for analyses.
func SetDefaultMemResourceLimit(value resourcev1.Quantity) {
	defaultMemResourceLimit = value
}

// SetDoDefaultMemResourceLimit controls whether a default memory resource limit
// is applied to analyses.
func SetDoDefaultMemResourceLimit(value bool) {
	doDefaultMemResourceLimit = value
}

// SetDefaultStorageRequest sets the default ephemeral storage request for analyses.
func SetDefaultStorageRequest(value resourcev1.Quantity) {
	defaultStorageRequest = value
}

// SetVICEProxyCPUResourceRequest sets the CPU resource request for the VICE proxy sidecar.
func SetVICEProxyCPUResourceRequest(value resourcev1.Quantity) {
	viceProxyCPUResourceRequest = value
}

// SetVICEProxyCPUResourceLimit sets the CPU resource limit for the VICE proxy sidecar.
func SetVICEProxyCPUResourceLimit(value resourcev1.Quantity) {
	viceProxyCPUResourceLimit = value
}

// SetDoVICEProxyCPUResourceLimit controls whether a CPU resource limit is
// applied to the VICE proxy sidecar.
func SetDoVICEProxyCPUResourceLimit(value bool) {
	doVICEProxyCPUResourceLimit = value
}

// SetVICEProxyMemResourceRequest sets the memory resource request for the VICE proxy sidecar.
func SetVICEProxyMemResourceRequest(value resourcev1.Quantity) {
	viceProxyMemResourceRequest = value
}

// SetVICEProxyMemResourceLimit sets the memory resource limit for the VICE proxy sidecar.
func SetVICEProxyMemResourceLimit(value resourcev1.Quantity) {
	viceProxyMemResourceLimit = value
}

// SetDoVICEProxyMemResourceLimit controls whether a memory resource limit is
// applied to the VICE proxy sidecar.
func SetDoVICEProxyMemResourceLimit(value bool) {
	doVICEProxyMemResourceLimit = value
}

// SetVICEProxyStorageRequest sets the ephemeral storage request for the VICE proxy sidecar.
func SetVICEProxyStorageRequest(value resourcev1.Quantity) {
	viceProxyStorageRequest = value
}

// SetVICEProxyStorageLimit sets the ephemeral storage limit for the VICE proxy sidecar.
func SetVICEProxyStorageLimit(value resourcev1.Quantity) {
	viceProxyStorageLimit = value
}

// SetDoVICEProxyStorageLimit controls whether an ephemeral storage limit is
// applied to the VICE proxy sidecar.
func SetDoVICEProxyStorageLimit(value bool) {
	doVICEProxyStorageLimit = value
}

// DefaultCPUResourceRequest returns the default CPU resource request for analyses.
func DefaultCPUResourceRequest() resourcev1.Quantity {
	return defaultCPUResourceRequest
}

// DefaultCPUResourceLimit returns the default CPU resource limit for analyses.
func DefaultCPUResourceLimit() resourcev1.Quantity {
	return defaultCPUResourceLimit
}

// DefaultMemResourceRequest returns the default memory resource request for analyses.
func DefaultMemResourceRequest() resourcev1.Quantity {
	return defaultMemResourceRequest
}

// DefaultMemResourceLimit returns the default memory resource limit for analyses.
func DefaultMemResourceLimit() resourcev1.Quantity {
	return defaultMemResourceLimit
}

// DefaultStorageRequest returns the default ephemeral storage request for analyses.
func DefaultStorageRequest() resourcev1.Quantity {
	return defaultStorageRequest
}

// VICEProxyCPUResourceRequest returns the CPU resource request for the VICE proxy sidecar.
func VICEProxyCPUResourceRequest() resourcev1.Quantity {
	return viceProxyCPUResourceRequest
}

// VICEProxyCPUResourceLimit returns the CPU resource limit for the VICE proxy sidecar.
func VICEProxyCPUResourceLimit() resourcev1.Quantity {
	return viceProxyCPUResourceLimit
}

// VICEProxyMemResourceRequest returns the memory resource request for the VICE proxy sidecar.
func VICEProxyMemResourceRequest() resourcev1.Quantity {
	return viceProxyMemResourceRequest
}

// VICEProxyMemResourceLimit returns the memory resource limit for the VICE proxy sidecar.
func VICEProxyMemResourceLimit() resourcev1.Quantity {
	return viceProxyMemResourceLimit
}

// VICEProxyStorageRequest returns the ephemeral storage request for the VICE proxy sidecar.
func VICEProxyStorageRequest() resourcev1.Quantity {
	return viceProxyStorageRequest
}

// VICEProxyStorageLimit returns the ephemeral storage limit for the VICE proxy sidecar.
func VICEProxyStorageLimit() resourcev1.Quantity {
	return viceProxyStorageLimit
}

func GPUEnabled(analysis *model.Analysis) bool {
	// GPU is considered enabled if the container explicitly requests GPUs
	// via MinGPUs/MaxGPUs, or if a legacy device entry references an NVIDIA
	// device path. The device-path check is kept for backward compatibility
	// with older job payloads.
	c := analysis.Steps[0].Component.Container
	if minGPUs(c) > 0 || maxGPUs(c) > 0 {
		return true
	}
	for _, device := range c.Devices {
		if strings.HasPrefix(strings.ToLower(device.HostPath), "/dev/nvidia") {
			return true
		}
	}
	return false
}

// getIntField is a small reflection helper to safely read an (exported) integer
// field by name from a struct value. Returns 0 when the field doesn't exist or
// can't be converted to an int.
func getIntField(v any, name string) int {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return 0
	}
	f := val.FieldByName(name)
	if !f.IsValid() {
		return 0
	}
	switch f.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(f.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(f.Uint())
	case reflect.Float32, reflect.Float64:
		return int(f.Float())
	default:
		return 0
	}
}

func minGPUs(c model.Container) int { return getIntField(c, "MinGPUs") }
func maxGPUs(c model.Container) int { return getIntField(c, "MaxGPUs") }

// gpuCount returns the preferred GPU count, falling back to the alternate
// when the preferred field is unset. Returns 1 if both are zero so legacy
// device-path detection still yields a valid GPU resource quantity.
func gpuCount(preferred, fallback int) int {
	if preferred > 0 {
		return preferred
	}
	if fallback > 0 {
		return fallback
	}
	return 1
}

// getStringSliceField is a reflection helper to safely read a string slice field
// from a struct value. Returns empty slice when the field doesn't exist or
// can't be converted to a []string.
func getStringSliceField(v any, name string) []string {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return []string{}
	}
	f := val.FieldByName(name)
	if !f.IsValid() {
		return []string{}
	}
	if f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String {
		result := make([]string, f.Len())
		for i := 0; i < f.Len(); i++ {
			result[i] = f.Index(i).String()
		}
		return result
	}
	return []string{}
}

func gpuModels(c model.Container) []string { return getStringSliceField(c, "GPUModels") }

func cpuResourceRequest(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultCPUResourceRequest()

	if analysis.Steps[0].Component.Container.MinCPUCores != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%fm", analysis.Steps[0].Component.Container.MinCPUCores*1000))
		if err != nil {
			log.Warn(err)
			value = DefaultCPUResourceRequest()
		}
	}

	return value
}

func cpuResourceLimit(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultCPUResourceLimit()

	if analysis.Steps[0].Component.Container.MaxCPUCores != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%fm", analysis.Steps[0].Component.Container.MaxCPUCores*1000))
		if err != nil {
			log.Warn(err)
			value = DefaultCPUResourceLimit()
		}
	}
	return value
}

func memResourceRequest(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultMemResourceRequest()

	if analysis.Steps[0].Component.Container.MinMemoryLimit != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%d", analysis.Steps[0].Component.Container.MinMemoryLimit))
		if err != nil {
			log.Warn(err)
			value = DefaultMemResourceRequest()
		}
	}
	return value
}

func memResourceLimit(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultMemResourceLimit()

	if analysis.Steps[0].Component.Container.MemoryLimit != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%d", analysis.Steps[0].Component.Container.MemoryLimit))
		if err != nil {
			log.Warn(err)
			value = DefaultMemResourceLimit()
		}
	}
	return value
}

func storageRequest(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultStorageRequest()

	if analysis.Steps[0].Component.Container.MinDiskSpace != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%d", analysis.Steps[0].Component.Container.MinDiskSpace))
		if err != nil {
			log.Warn(err)
			value = DefaultStorageRequest()
		}
	}
	return value
}

func SharedMemoryAmount(analysis *model.Analysis) *resourcev1.Quantity {
	var shmAmount resourcev1.Quantity
	var err error
	for _, device := range analysis.Steps[0].Component.Container.Devices {
		if strings.HasPrefix(strings.ToLower(device.HostPath), constants.ShmDevice) {
			shmAmount, err = resourcev1.ParseQuantity(device.ContainerPath)
			if err != nil {
				log.Warn(err)
				return nil
			}
			return &shmAmount
		}
	}
	return nil
}

// GPUModelsRequested returns the list of acceptable GPU models for the analysis,
// or an empty slice if none are specified. This is used to create node affinity
// requirements to schedule the pod on nodes with compatible GPU models.
func GPUModelsRequested(analysis *model.Analysis) []string {
	return gpuModels(analysis.Steps[0].Component.Container)
}

func resourceRequests(analysis *model.Analysis) apiv1.ResourceList {
	reqs := apiv1.ResourceList{
		apiv1.ResourceCPU:              cpuResourceRequest(analysis), // analysis contains # cores
		apiv1.ResourceMemory:           memResourceRequest(analysis), // analysis contains # bytes mem
		apiv1.ResourceEphemeralStorage: storageRequest(analysis),     // analysis contains # bytes storage
	}

	// Add GPU request if specified. For extended resources like GPUs, requests
	// should be integers. When only one of MinGPUs/MaxGPUs is set we use that
	// value for the request so K8s' "request must equal limit for extended
	// resources" rule doesn't end up clamping a multi-GPU ask down to 1. When
	// neither is set (legacy /dev/nvidia* device path), default to 1.
	if GPUEnabled(analysis) {
		c := analysis.Steps[0].Component.Container
		requestedGPUs := gpuCount(minGPUs(c), maxGPUs(c))
		if q, err := resourcev1.ParseQuantity(fmt.Sprintf("%d", int64(requestedGPUs))); err == nil {
			reqs[apiv1.ResourceName("nvidia.com/gpu")] = q
		} else {
			log.Warn(err)
		}
	}

	return reqs
}

func resourceLimits(analysis *model.Analysis) apiv1.ResourceList {
	limits := apiv1.ResourceList{}

	if doDefaultCPUResourceLimit {
		limits[apiv1.ResourceCPU] = cpuResourceLimit(analysis)
	}

	if doDefaultMemResourceLimit {
		limits[apiv1.ResourceMemory] = memResourceLimit(analysis)
	}

	// If GPUs are requested, set the GPU limits appropriately. Prefer MaxGPUs;
	// fall back to MinGPUs when MaxGPUs is unset so a one-sided ask still emits
	// a non-default limit. When neither is set (legacy /dev/nvidia* device
	// path), default to 1.
	if GPUEnabled(analysis) {
		c := analysis.Steps[0].Component.Container
		limitGPUs := gpuCount(maxGPUs(c), minGPUs(c))
		if q, err := resourcev1.ParseQuantity(fmt.Sprintf("%d", int64(limitGPUs))); err == nil {
			limits[apiv1.ResourceName("nvidia.com/gpu")] = q
		} else {
			log.Warn(err)
		}
	}

	return limits
}

func VICEProxyRequirements(analysis *model.Analysis) *apiv1.ResourceRequirements {
	retval := &apiv1.ResourceRequirements{
		Requests: apiv1.ResourceList{
			apiv1.ResourceCPU:              VICEProxyCPUResourceRequest(),
			apiv1.ResourceMemory:           VICEProxyMemResourceRequest(),
			apiv1.ResourceEphemeralStorage: VICEProxyStorageRequest(),
		},
	}

	if !doVICEProxyStorageLimit && !doVICEProxyCPUResourceLimit && !doVICEProxyMemResourceLimit {
		return retval
	}

	limits := apiv1.ResourceList{}

	if doVICEProxyCPUResourceLimit {
		limits[apiv1.ResourceCPU] = VICEProxyCPUResourceLimit()
	}

	if doVICEProxyMemResourceLimit {
		limits[apiv1.ResourceMemory] = VICEProxyMemResourceLimit()
	}

	if doVICEProxyStorageLimit {
		limits[apiv1.ResourceEphemeralStorage] = VICEProxyStorageLimit()
	}

	retval.Limits = limits

	return retval
}

// Requirements returns the limits and requests needed for the analysis itself.
// The resourceLimits function already handles the doDefaultCPUResourceLimit,
// doDefaultMemResourceLimit, and GPU flags internally, returning an empty
// ResourceList when no limits are configured.
func Requirements(analysis *model.Analysis) *apiv1.ResourceRequirements {
	return &apiv1.ResourceRequirements{
		Limits:   resourceLimits(analysis),
		Requests: resourceRequests(analysis),
	}
}
