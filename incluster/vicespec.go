package incluster

import (
	"context"
	"fmt"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/resourcing"
	"github.com/cyverse-de/model/v10"
)

// BuildVICESpec maps a model.Job onto the cluster-agnostic VICESpec that
// app-exposer sends to a spec-aware operator. It is the resolution half of the
// old incluster builders: every model.Job method that encodes DE semantics
// (OutputDirectory, ExcludeArguments, Step.Arguments, StepInput.IRODSPath, …) is
// evaluated here so the operator receives resolved primitives and needs no
// model dependency. The one DB-derived value — the user's login IP — is
// resolved here too (the only network call), keeping the operator DB-free.
func (i *Incluster) BuildVICESpec(ctx context.Context, job *model.Job, analysisID constants.AnalysisID) (*operatorclient.VICESpec, error) {
	loginIP, err := i.apps.GetUserIP(ctx, job.UserID)
	if err != nil {
		return nil, fmt.Errorf("resolving login IP for analysis %s: %w", analysisID, err)
	}
	return buildVICESpec(job, analysisID, loginIP)
}

// buildVICESpec is the pure model.Job → VICESpec mapping, with the one
// DB-derived value (loginIP) passed in. Kept separate from BuildVICESpec so the
// mapping is testable without the apps/DB dependency.
func buildVICESpec(job *model.Job, analysisID constants.AnalysisID, loginIP string) (*operatorclient.VICESpec, error) {
	if len(job.Steps) == 0 {
		return nil, fmt.Errorf("job %s has no steps", job.ID)
	}
	step := &job.Steps[0]
	container := step.Component.Container

	ports := make([]int, 0, len(container.Ports))
	for _, p := range container.Ports {
		ports = append(ports, p.ContainerPort)
	}

	spec := &operatorclient.VICESpec{
		SpecVersion: operatorclient.CurrentVICESpecVersion,

		AnalysisID:  analysisID,
		ExternalID:  constants.ExternalID(job.InvocationID),
		JobName:     job.Name,
		AppID:       job.AppID,
		AppName:     job.AppName,
		UserID:      job.UserID,
		Submitter:   job.Submitter,
		UserLoginIP: loginIP,

		Container: operatorclient.ContainerSpec{
			Image:      container.Image.Name,
			Tag:        container.Image.Tag,
			UID:        container.UID,
			Ports:      ports,
			EntryPoint: container.EntryPoint,
			WorkingDir: container.WorkingDir, // raw; vicebuild resolves the mount default
			Arguments:  step.Arguments(),
		},
		Environment: step.Environment,

		Resources: buildResourceSpec(job),
		GPU:       buildGPUSpec(job),

		MountDataStore:     job.MountDataStore,
		UserHome:           job.UserHome,
		OutputDirectory:    job.OutputDirectory(),
		ExcludeArguments:   job.ExcludeArguments(),
		Inputs:             buildInputSpecs(job),
		InputPathListPaths: ticketlessInputPaths(job),
		FileMetadata:       buildFileMetadata(job.FileMetadata),
	}

	return spec, nil
}

// buildResourceSpec extracts the raw resource asks. The cluster's
// default/clamp policy is applied operator-side, so only the asks ride here.
func buildResourceSpec(job *model.Job) operatorclient.ResourceSpec {
	container := job.Steps[0].Component.Container
	rs := operatorclient.ResourceSpec{
		MinCPUCores:    container.MinCPUCores,
		MaxCPUCores:    container.MaxCPUCores,
		MinMemoryBytes: container.MinMemoryLimit,
		MaxMemoryBytes: container.MemoryLimit,
		MinDiskBytes:   maxDiskSpace(job),
	}
	if shm := resourcing.SharedMemoryAmount(job); shm != nil {
		bytes := shm.Value()
		rs.SharedMemoryBytes = &bytes
	}
	return rs
}

// maxDiskSpace returns the largest MinDiskSpace across the job's steps, matching
// incluster.getPersistentVolumeCapacity. VICE is single-step, but the max keeps
// parity with the legacy builder.
func maxDiskSpace(job *model.Job) int64 {
	var maxDisk int64
	for _, step := range job.Steps {
		if d := step.Component.Container.MinDiskSpace; d > maxDisk {
			maxDisk = d
		}
	}
	return maxDisk
}

// buildGPUSpec resolves the analysis's GPU request into a canonical, vendor-
// neutral GPUSpec, or nil when no GPU is requested. The vendor is nvidia today
// (model.Job carries no vendor); the count folds the legacy /dev/nvidia and
// MinGPUs/MaxGPUs logic into the single effective count the operator emits with
// requests == limits.
func buildGPUSpec(job *model.Job) *operatorclient.GPUSpec {
	if !resourcing.GPUEnabled(job) {
		return nil
	}
	container := job.Steps[0].Component.Container
	return &operatorclient.GPUSpec{
		Vendor: operatorclient.GPUVendorNvidia,
		Count:  effectiveGPUCount(container.MinGPUs, container.MaxGPUs),
		Models: resourcing.GPUModelsRequested(job),
	}
}

// effectiveGPUCount mirrors the count the operator's equalizeGPUResources would
// settle on: the higher of the requested min/max, defaulting to 1 for a legacy
// device-path GPU with neither field set.
func effectiveGPUCount(minGPUs, maxGPUs int64) int64 {
	if maxGPUs > 0 {
		return maxGPUs
	}
	if minGPUs > 0 {
		return minGPUs
	}
	return 1
}

// buildInputSpecs resolves every input across all steps into the CSI mount
// view (full list, with resolved iRODS paths and types).
func buildInputSpecs(job *model.Job) []operatorclient.InputSpec {
	var inputs []operatorclient.InputSpec
	for s := range job.Steps {
		for in := range job.Steps[s].Config.Inputs {
			input := &job.Steps[s].Config.Inputs[in]
			inputs = append(inputs, operatorclient.InputSpec{
				IRODSPath: input.IRODSPath(),
				Type:      input.Type,
			})
		}
	}
	return inputs
}

// ticketlessInputPaths resolves the input-path-list view: the iRODS paths of
// inputs without download tickets (the subset porklock stages).
func ticketlessInputPaths(job *model.Job) []string {
	withoutTickets := job.FilterInputsWithoutTickets()
	if len(withoutTickets) == 0 {
		return nil
	}
	paths := make([]string, 0, len(withoutTickets))
	for i := range withoutTickets {
		paths = append(paths, withoutTickets[i].IRODSPath())
	}
	return paths
}

func buildFileMetadata(metadata []model.FileMetadata) []operatorclient.MetadataAVU {
	if len(metadata) == 0 {
		return nil
	}
	out := make([]operatorclient.MetadataAVU, 0, len(metadata))
	for _, m := range metadata {
		out = append(out, operatorclient.MetadataAVU{
			Attribute: m.Attribute,
			Value:     m.Value,
			Unit:      m.Unit,
		})
	}
	return out
}
