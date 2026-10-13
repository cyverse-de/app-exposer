package gpus

import (
	"testing"

	"github.com/cyverse-de/model/v10"
	"github.com/stretchr/testify/assert"
)

func makeJob(steps ...model.Step) *model.Job {
	return &model.Job{Steps: steps}
}

func stepWithGPUs(maxGPUs, minGPUs int64, models []string) model.Step {
	return model.Step{
		Component: model.StepComponent{
			Container: model.Container{
				MaxGPUs:   maxGPUs,
				MinGPUs:   minGPUs,
				GPUModels: models,
			},
		},
	}
}

func TestNumberReserved(t *testing.T) {
	d := New()

	tests := []struct {
		name string
		job  *model.Job
		want int64
	}{
		{
			name: "no steps",
			job:  makeJob(),
			want: 0,
		},
		{
			name: "no GPUs",
			job:  makeJob(stepWithGPUs(0, 0, nil)),
			want: 0,
		},
		{
			name: "MaxGPUs only",
			job:  makeJob(stepWithGPUs(2, 0, nil)),
			want: 2,
		},
		{
			name: "MinGPUs only",
			job:  makeJob(stepWithGPUs(0, 3, nil)),
			want: 3,
		},
		{
			name: "MaxGPUs wins over MinGPUs",
			job:  makeJob(stepWithGPUs(4, 2, nil)),
			want: 4,
		},
		{
			name: "MinGPUs higher than MaxGPUs returns the higher of the two",
			job:  makeJob(stepWithGPUs(1, 5, nil)),
			want: 5,
		},
		{
			name: "multiple steps takes the max across all",
			job: makeJob(
				stepWithGPUs(1, 0, nil),
				stepWithGPUs(4, 0, nil),
				stepWithGPUs(2, 0, nil),
			),
			want: 4,
		},
		{
			name: "mixed steps with and without GPUs",
			job: makeJob(
				stepWithGPUs(0, 0, nil), // no GPUs
				stepWithGPUs(2, 0, nil), // 2 GPUs
			),
			want: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, d.NumberReserved(tc.job))
		})
	}
}

func TestGPUModels(t *testing.T) {
	d := New()

	tests := []struct {
		name string
		job  *model.Job
		want []string
	}{
		{
			name: "returns models from first step",
			job:  makeJob(stepWithGPUs(1, 0, []string{"A100", "V100"})),
			want: []string{"A100", "V100"},
		},
		{
			name: "returns nil when no models specified",
			job:  makeJob(stepWithGPUs(1, 0, nil)),
			want: nil,
		},
		{
			name: "returns nil when no steps",
			job:  makeJob(),
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want == nil {
				assert.Nil(t, d.GPUModels(tc.job))
			} else {
				assert.Equal(t, tc.want, d.GPUModels(tc.job))
			}
		})
	}
}

func TestGPUModels_ReturnsCopy(t *testing.T) {
	d := New()
	job := makeJob(stepWithGPUs(1, 0, []string{"A100"}))
	models := d.GPUModels(job)
	models[0] = "MUTATED"
	// Original should be unchanged.
	assert.Equal(t, "A100", job.Steps[0].Component.Container.GPUModels[0])
}

func TestNumberReserved_NilJob(t *testing.T) {
	d := New()
	assert.Equal(t, int64(0), d.NumberReserved(nil))
}

func TestGPUModels_NilJob(t *testing.T) {
	d := New()
	assert.Nil(t, d.GPUModels(nil))
}
