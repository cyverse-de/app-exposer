package resourcing

import (
	"testing"

	"github.com/cyverse-de/model/v10"
	"github.com/stretchr/testify/assert"
	apiv1 "k8s.io/api/core/v1"
)

// testAnalysis builds a minimal Analysis with a single step.
// Fields default to zero values; callers mutate what they need.
func testAnalysis() *model.Analysis {
	return &model.Analysis{
		Steps: []model.Step{{}},
	}
}

// ---------------------------------------------------------------------------
// GPUEnabled
// ---------------------------------------------------------------------------

func TestGPUEnabled_MinGPUs(t *testing.T) {
	a := testAnalysis()
	a.Steps[0].Component.Container.MinGPUs = 1
	assert.True(t, GPUEnabled(a), "should be true when MinGPUs > 0")
}

func TestGPUEnabled_MaxGPUsOnly(t *testing.T) {
	a := testAnalysis()
	a.Steps[0].Component.Container.MaxGPUs = 2
	assert.True(t, GPUEnabled(a), "should be true when MaxGPUs > 0 even if MinGPUs is 0")
}

func TestGPUEnabled_LegacyNvidiaDevice(t *testing.T) {
	a := testAnalysis()
	a.Steps[0].Component.Container.Devices = []model.Device{
		{HostPath: "/dev/nvidia0", ContainerPath: "/dev/nvidia0"},
	}
	assert.True(t, GPUEnabled(a), "should be true when an nvidia device path is present")
}

func TestGPUEnabled_NonNvidiaDevice(t *testing.T) {
	a := testAnalysis()
	a.Steps[0].Component.Container.Devices = []model.Device{
		{HostPath: "/dev/sda", ContainerPath: "/dev/sda"},
	}
	assert.False(t, GPUEnabled(a), "should be false for non-nvidia device paths")
}

func TestGPUEnabled_NoGPUFields(t *testing.T) {
	a := testAnalysis()
	assert.False(t, GPUEnabled(a), "should be false when no GPU fields or nvidia devices")
}

// ---------------------------------------------------------------------------
// GPUModelsRequested
// ---------------------------------------------------------------------------

func TestGPUModelsRequested_ReturnsModels(t *testing.T) {
	a := testAnalysis()
	a.Steps[0].Component.Container.GPUModels = []string{"NVIDIA-A16", "NVIDIA-A40"}
	result := GPUModelsRequested(a)
	assert.Equal(t, []string{"NVIDIA-A16", "NVIDIA-A40"}, result)
}

func TestGPUModelsRequested_EmptyWhenNil(t *testing.T) {
	a := testAnalysis()
	// GPUModels is nil by default
	result := GPUModelsRequested(a)
	assert.NotNil(t, result, "should return empty slice, not nil")
	assert.Empty(t, result, "should be empty when no GPU models set")
}

// ---------------------------------------------------------------------------
// Requirements — GPU resource quantities
// ---------------------------------------------------------------------------

func TestRequirements_NoGPU(t *testing.T) {
	a := testAnalysis()
	reqs := Requirements(a)
	gpuName := apiv1.ResourceName("nvidia.com/gpu")
	_, inRequests := reqs.Requests[gpuName]
	_, inLimits := reqs.Limits[gpuName]
	assert.False(t, inRequests, "nvidia.com/gpu should not be in requests when GPU is disabled")
	assert.False(t, inLimits, "nvidia.com/gpu should not be in limits when GPU is disabled")
}

func TestRequirements_MinMaxGPU(t *testing.T) {
	a := testAnalysis()
	a.Steps[0].Component.Container.MinGPUs = 1
	a.Steps[0].Component.Container.MaxGPUs = 2
	reqs := Requirements(a)
	gpuName := apiv1.ResourceName("nvidia.com/gpu")

	reqGPU, ok := reqs.Requests[gpuName]
	assert.True(t, ok, "nvidia.com/gpu should be in requests")
	assert.Equal(t, int64(1), reqGPU.Value(), "GPU request should equal MinGPUs")

	limGPU, ok := reqs.Limits[gpuName]
	assert.True(t, ok, "nvidia.com/gpu should be in limits")
	assert.Equal(t, int64(2), limGPU.Value(), "GPU limit should equal MaxGPUs")
}

func TestRequirements_EqualMinMaxGPU(t *testing.T) {
	a := testAnalysis()
	a.Steps[0].Component.Container.MinGPUs = 3
	a.Steps[0].Component.Container.MaxGPUs = 3
	reqs := Requirements(a)
	gpuName := apiv1.ResourceName("nvidia.com/gpu")

	reqGPU := reqs.Requests[gpuName]
	limGPU := reqs.Limits[gpuName]
	assert.Equal(t, int64(3), reqGPU.Value(), "GPU request should be 3")
	assert.Equal(t, int64(3), limGPU.Value(), "GPU limit should be 3")
}

func TestRequirements_OnlyMaxGPUsSet(t *testing.T) {
	// When apps sends only max_gpus, the request should mirror it instead of
	// defaulting to 1 — otherwise the operator's equalize step would clamp
	// the multi-GPU ask back down.
	a := testAnalysis()
	a.Steps[0].Component.Container.MaxGPUs = 4
	reqs := Requirements(a)
	gpuName := apiv1.ResourceName("nvidia.com/gpu")

	reqGPU := reqs.Requests[gpuName]
	limGPU := reqs.Limits[gpuName]
	assert.Equal(t, int64(4), reqGPU.Value(),
		"GPU request should mirror MaxGPUs when MinGPUs is unset")
	assert.Equal(t, int64(4), limGPU.Value(),
		"GPU limit should equal MaxGPUs")
}

func TestRequirements_OnlyMinGPUsSet(t *testing.T) {
	// Symmetric to OnlyMaxGPUsSet: a min-only ask should set both sides.
	a := testAnalysis()
	a.Steps[0].Component.Container.MinGPUs = 2
	reqs := Requirements(a)
	gpuName := apiv1.ResourceName("nvidia.com/gpu")

	reqGPU := reqs.Requests[gpuName]
	limGPU := reqs.Limits[gpuName]
	assert.Equal(t, int64(2), reqGPU.Value(),
		"GPU request should equal MinGPUs")
	assert.Equal(t, int64(2), limGPU.Value(),
		"GPU limit should mirror MinGPUs when MaxGPUs is unset")
}

func TestRequirements_LegacyNvidiaDeviceDefaultsToOne(t *testing.T) {
	a := testAnalysis()
	a.Steps[0].Component.Container.Devices = []model.Device{
		{HostPath: "/dev/nvidia0", ContainerPath: "/dev/nvidia0"},
	}
	reqs := Requirements(a)
	gpuName := apiv1.ResourceName("nvidia.com/gpu")

	reqGPU := reqs.Requests[gpuName]
	limGPU := reqs.Limits[gpuName]
	assert.Equal(t, int64(1), reqGPU.Value(), "Legacy nvidia device should request 1 GPU")
	assert.Equal(t, int64(1), limGPU.Value(), "Legacy nvidia device should limit to 1 GPU")
}

func TestRequirements_ExplicitZeroGPU(t *testing.T) {
	// Explicit zeros with no nvidia device should NOT request GPUs
	a := testAnalysis()
	a.Steps[0].Component.Container.MinGPUs = 0
	a.Steps[0].Component.Container.MaxGPUs = 0
	reqs := Requirements(a)
	gpuName := apiv1.ResourceName("nvidia.com/gpu")
	_, inRequests := reqs.Requests[gpuName]
	_, inLimits := reqs.Limits[gpuName]
	assert.False(t, inRequests, "Explicit zero GPUs should not produce a GPU request")
	assert.False(t, inLimits, "Explicit zero GPUs should not produce a GPU limit")
}
