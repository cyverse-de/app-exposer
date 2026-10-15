// Package gpus detects the number of GPUs reserved by a job by
// inspecting the model's container fields. It mirrors the role of the
// millicores package but for GPU resources.
package gpus

import (
	"github.com/cyverse-de/model/v10"
)

// Detector reads GPU reservation counts from job submissions.
type Detector struct{}

// New creates a GPU Detector. Unlike the millicores detector, no
// database handle or default value is needed — GPUs default to 0 when
// a job does not request any.
func New() *Detector {
	return &Detector{}
}

// NumberReserved returns the effective number of GPUs reserved by the
// job. It iterates all steps and returns the maximum non-zero value of
// either MaxGPUs or MinGPUs encountered. If no step requests GPUs, the
// result is 0.
//
// Both MaxGPUs and MinGPUs are checked because either field may be set
// depending on the tool integrator's configuration. The higher value is
// used for reservation tracking since it represents the upper bound of
// what the scheduler may allocate.
//
// This is intentionally simpler than the millicores detector: there is
// no default GPU allocation for steps that don't request them.
func (d *Detector) NumberReserved(job *model.Job) int64 {
	if job == nil {
		return 0
	}
	var maxSeen int64
	for _, step := range job.Steps {
		c := step.Component.Container
		if c.MaxGPUs > maxSeen {
			maxSeen = c.MaxGPUs
		}
		if c.MinGPUs > maxSeen {
			maxSeen = c.MinGPUs
		}
	}
	return maxSeen
}

// GPUModels returns the list of GPU model names requested by the job.
// Returns nil when no models are specified (meaning any model is
// acceptable). Only the first step's models are consulted, matching
// the existing GPU spec construction in incluster/vicespec.go.
func (d *Detector) GPUModels(job *model.Job) []string {
	if job == nil || len(job.Steps) == 0 {
		return nil
	}
	models := job.Steps[0].Component.Container.GPUModels
	if len(models) == 0 {
		return nil
	}
	// Return a copy to avoid callers mutating the job's slice.
	out := make([]string, len(models))
	copy(out, models)
	return out
}
