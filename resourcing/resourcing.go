package resourcing

import (
	"fmt"
	"strings"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/model/v7"
	apiv1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
)

var log = common.Log

var (
	DefaultCPUResourceRequest, _   = resourcev1.ParseQuantity("1000m")
	DefaultMemResourceRequest, _   = resourcev1.ParseQuantity("2Gi")
	DefaultStorageRequest, _       = resourcev1.ParseQuantity("1Gi")
	DefaultCPUResourceLimit, _     = resourcev1.ParseQuantity("2000m")
	DefaultMemResourceLimit, _     = resourcev1.ParseQuantity("8Gi")
	VICEProxyCPUResourceRequest, _ = resourcev1.ParseQuantity("100m")
	VICEProxyMemResourceRequest, _ = resourcev1.ParseQuantity("100Mi")
	VICEProxyStorageRequest, _     = resourcev1.ParseQuantity("100Mi")
	VICEProxyCPUResourceLimit, _   = resourcev1.ParseQuantity("200m")
	VICEProxyMemResourceLimit, _   = resourcev1.ParseQuantity("200Mi")
	VICEProxyStorageLimit, _       = resourcev1.ParseQuantity("200Mi")
)

const (
	ShmDevice = "/dev/shm"
)

func GPUEnabled(analysis *model.Analysis) bool {
	gpuEnabled := false
	for _, device := range analysis.Steps[0].Component.Container.Devices {
		if strings.HasPrefix(strings.ToLower(device.HostPath), "/dev/nvidia") {
			gpuEnabled = true
		}
	}
	return gpuEnabled
}

func cpuResourceRequest(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultCPUResourceRequest

	if analysis.Steps[0].Component.Container.MinCPUCores != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%fm", analysis.Steps[0].Component.Container.MinCPUCores*1000))
		if err != nil {
			log.Warn(err)
			value = DefaultCPUResourceRequest
		}
	}

	return value
}

func cpuResourceLimit(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultCPUResourceLimit

	if analysis.Steps[0].Component.Container.MaxCPUCores != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%fm", analysis.Steps[0].Component.Container.MaxCPUCores*1000))
		if err != nil {
			log.Warn(err)
			value = DefaultCPUResourceLimit
		}
	}
	return value
}

func memResourceRequest(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultMemResourceRequest

	if analysis.Steps[0].Component.Container.MinMemoryLimit != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%d", analysis.Steps[0].Component.Container.MinMemoryLimit))
		if err != nil {
			log.Warn(err)
			value = DefaultMemResourceRequest
		}
	}
	return value
}

func memResourceLimit(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultMemResourceLimit

	if analysis.Steps[0].Component.Container.MemoryLimit != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%d", analysis.Steps[0].Component.Container.MemoryLimit))
		if err != nil {
			log.Warn(err)
			value = DefaultMemResourceLimit
		}
	}
	return value
}

func storageRequest(analysis *model.Analysis) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = DefaultStorageRequest

	if analysis.Steps[0].Component.Container.MinDiskSpace != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%d", analysis.Steps[0].Component.Container.MinDiskSpace))
		if err != nil {
			log.Warn(err)
			value = DefaultStorageRequest
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
	return apiv1.ResourceList{
		apiv1.ResourceCPU:              cpuResourceRequest(analysis), // analysis contains # cores
		apiv1.ResourceMemory:           memResourceRequest(analysis), // analysis contains # bytes mem
		apiv1.ResourceEphemeralStorage: storageRequest(analysis),     // analysis contains # bytes storage
	}
}

func resourceLimits(analysis *model.Analysis) apiv1.ResourceList {
	limits := apiv1.ResourceList{
		apiv1.ResourceCPU:    cpuResourceLimit(analysis), //analysis contains # cores
		apiv1.ResourceMemory: memResourceLimit(analysis), // analysis contains # bytes mem
	}

	// If a GPU device is configured, then add it to the resource limits.
	if GPUEnabled(analysis) {
		gpuLimit, err := resourcev1.ParseQuantity("1")
		if err != nil {
			log.Warn(err)
		} else {
			limits[apiv1.ResourceName("nvidia.com/gpu")] = gpuLimit
		}
	}

	return limits
}

func Requirements(analysis *model.Analysis) *apiv1.ResourceRequirements {
	return &apiv1.ResourceRequirements{
		Limits:   resourceLimits(analysis),
		Requests: resourceRequests(analysis),
	}
}
