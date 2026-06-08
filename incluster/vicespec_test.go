package incluster

import (
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/model/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildVICESpecMapping checks the resolved field mapping that
// buildVICESpec performs — the values the operator would otherwise have to
// re-derive from model.Job.
func TestBuildVICESpecMapping(t *testing.T) {
	job := goldenJob(true) // nvidia GPU, min 1 / max 2, model NVIDIA-A10G
	job.FileMetadata = []model.FileMetadata{{Attribute: "ipc-batch", Value: "true", Unit: ""}}

	spec, err := buildVICESpec(job, constants.AnalysisID(job.ID), "10.0.0.1")
	require.NoError(t, err)

	assert.Equal(t, operatorclient.CurrentVICESpecVersion, spec.SpecVersion)
	assert.Equal(t, constants.AnalysisID("analysis-1"), spec.AnalysisID)
	assert.Equal(t, constants.ExternalID("external-1"), spec.ExternalID)
	assert.Equal(t, "10.0.0.1", spec.UserLoginIP)
	assert.Equal(t, "cyverse/jupyter", spec.Container.Image)
	assert.Equal(t, []int{8888}, spec.Container.Ports)
	assert.Equal(t, map[string]string{"FOO": "bar"}, spec.Environment)

	// GPU resolves to a vendor-neutral spec; count folds min/max to the higher.
	require.NotNil(t, spec.GPU)
	assert.Equal(t, operatorclient.GPUVendorNvidia, spec.GPU.Vendor)
	assert.Equal(t, int64(2), spec.GPU.Count)
	assert.Equal(t, []string{"NVIDIA-A10G"}, spec.GPU.Models)

	// Resource asks ride raw (no clamp applied here).
	assert.Equal(t, float32(1), spec.Resources.MinCPUCores)
	assert.Equal(t, float32(4), spec.Resources.MaxCPUCores)

	// File metadata maps across without the model dependency.
	require.Len(t, spec.FileMetadata, 1)
	assert.Equal(t, "ipc-batch", spec.FileMetadata[0].Attribute)
}

func TestBuildVICESpecNoGPU(t *testing.T) {
	spec, err := buildVICESpec(goldenJob(false), "analysis-1", "10.0.0.1")
	require.NoError(t, err)
	assert.Nil(t, spec.GPU, "no GPU requested → nil GPU spec")
}

func TestBuildVICESpecNoSteps(t *testing.T) {
	_, err := buildVICESpec(&model.Job{ID: "j-1"}, "analysis-1", "10.0.0.1")
	require.Error(t, err)
}

// TestEffectiveGPUCount documents the min/max → single-count folding that
// matches the operator's request==limit equalization.
func TestEffectiveGPUCount(t *testing.T) {
	tests := []struct {
		min, max, want int64
	}{
		{min: 1, max: 2, want: 2},
		{min: 2, max: 0, want: 2},
		{min: 0, max: 3, want: 3},
		{min: 0, max: 0, want: 1}, // legacy device-path GPU
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, effectiveGPUCount(tt.min, tt.max))
	}
}
