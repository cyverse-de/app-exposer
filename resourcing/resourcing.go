package resourcing

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/model/v9"
	apiv1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
)

var log = common.Log

var (
	defaultCPUResourceRequest, _   = resourcev1.ParseQuantity("1000m")
	defaultMemResourceRequest, _   = resourcev1.ParseQuantity("2Gi")
	defaultStorageRequest, _       = resourcev1.ParseQuantity("1Gi")
	defaultCPUResourceLimit, _     = resourcev1.ParseQuantity("2000m")
	defaultMemResourceLimit, _     = resourcev1.ParseQuantity("8Gi")
	viceProxyCPUResourceRequest, _ = resourcev1.ParseQuantity("100m")
	viceProxyMemResourceRequest, _ = resourcev1.ParseQuantity("100Mi")
	viceProxyStorageRequest, _     = resourcev1.ParseQuantity("100Mi")
	viceProxyCPUResourceLimit, _   = resourcev1.ParseQuantity("200m")
	viceProxyMemResourceLimit, _   = resourcev1.ParseQuantity("200Mi")
	viceProxyStorageLimit, _       = resourcev1.ParseQuantity("200Mi")

	doDefaultCPUResourceLimit   = true
	doDefaultMemResourceLimit   = true
	doVICEProxyCPUResourceLimit = true
	doVICEProxyMemResourceLimit = true
	doVICEProxyStorageLimit     = true
)

const (
	ShmDevice = "/dev/shm"
)

func SetDefaultCPUResourceRequest(value resourcev1.Quantity) {
	defaultCPUResourceRequest = value
}

func SetDefaultCPUResourceLimit(value resourcev1.Quantity) {
	defaultCPUResourceLimit = value
}

func SetDoDefaultCPUResourceLimit(value bool) {
	doDefaultCPUResourceLimit = value
}

func SetDefaultMemResourceRequest(value resourcev1.Quantity) {
	defaultMemResourceRequest = value
}

func SetDefaultMemResourceLimit(value resourcev1.Quantity) {
	defaultMemResourceLimit = value
}

func SetDoDefaultMemResourceLimit(value bool) {
	doDefaultMemResourceLimit = value
}

func SetDefaultStorageRequest(value resourcev1.Quantity) {
	defaultStorageRequest = value
}

func SetVICEProxyCPUResourceRequest(value resourcev1.Quantity) {
	viceProxyCPUResourceRequest = value
}

func SetVICEProxyCPUResourceLimit(value resourcev1.Quantity) {
	viceProxyCPUResourceLimit = value
}

func SetDoVICEProxyCPUResourceLimit(value bool) {
	doVICEProxyCPUResourceLimit = value
}

func SetVICEProxyMemResourceRequest(value resourcev1.Quantity) {
	viceProxyMemResourceRequest = value
}

func SetVICEProxyMemResourceLimit(value resourcev1.Quantity) {
	viceProxyMemResourceLimit = value
}

func SetDoVICEProxyMemResourceLimit(value bool) {
	doVICEProxyMemResourceLimit = value
}

func SetVICEProxyStorageRequest(value resourcev1.Quantity) {
	viceProxyStorageRequest = value
}

func SetVICEProxyStorageLimit(value resourcev1.Quantity) {
	viceProxyStorageLimit = value
}

func SetDoVICEProxyStorageLimit(value bool) {
	doVICEProxyStorageLimit = value
}

func DefaultCPUResourceRequest() resourcev1.Quantity {
	return defaultCPUResourceRequest
}

func DefaultCPUResourceLimit() resourcev1.Quantity {
	return defaultCPUResourceLimit
}

func DefaultMemResourceRequest() resourcev1.Quantity {
	return defaultMemResourceRequest
}

func DefaultMemResourceLimit() resourcev1.Quantity {
	return defaultMemResourceLimit
}

func DefaultStorageRequest() resourcev1.Quantity {
	return defaultStorageRequest
}

func VICEProxyCPUResourceRequest() resourcev1.Quantity {
	return viceProxyCPUResourceRequest
}

func VICEProxyCPUResourceLimit() resourcev1.Quantity {
	return viceProxyCPUResourceLimit
}

func VICEProxyMemResourceRequest() resourcev1.Quantity {
	return viceProxyMemResourceRequest
}

func VICEProxyMemResourceLimit() resourcev1.Quantity {
	return viceProxyMemResourceLimit
}

func VICEProxyStorageRequest() resourcev1.Quantity {
	return viceProxyStorageRequest
}

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
func getIntField(v interface{}, name string) int {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
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
		if strings.HasPrefix(strings.ToLower(device.HostPath), ShmDevice) {
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

func resourceRequests(analysis *model.Analysis) apiv1.ResourceList {
	reqs := apiv1.ResourceList{
		apiv1.ResourceCPU:              cpuResourceRequest(analysis), // analysis contains # cores
		apiv1.ResourceMemory:           memResourceRequest(analysis), // analysis contains # bytes mem
		apiv1.ResourceEphemeralStorage: storageRequest(analysis),     // analysis contains # bytes storage
	}

	// Add GPU request if specified. For extended resources like GPUs, requests
	// should be integers. When only legacy device detection is present, default
	// the request to 1 to match previous implicit behavior.
	if GPUEnabled(analysis) {
		c := analysis.Steps[0].Component.Container
		requestedGPUs := 1
		if minGPUs(c) > 0 {
			requestedGPUs = minGPUs(c)
		}
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

	// If GPUs are requested, set the GPU limits appropriately. Prefer the
	// explicit MaxGPUs field when provided; otherwise, fall back to legacy
	// device-path detection and a single GPU.
	if GPUEnabled(analysis) {
		c := analysis.Steps[0].Component.Container
		limitGPUs := 1
		if maxGPUs(c) > 0 {
			limitGPUs = maxGPUs(c)
		}
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
func Requirements(analysis *model.Analysis) *apiv1.ResourceRequirements {
	retval := &apiv1.ResourceRequirements{
		Limits:   resourceLimits(analysis),
		Requests: resourceRequests(analysis),
	}

	if !doDefaultCPUResourceLimit && !doDefaultMemResourceLimit && !GPUEnabled(analysis) {
		return retval
	}

	retval.Limits = resourceLimits(analysis)

	return retval
}
