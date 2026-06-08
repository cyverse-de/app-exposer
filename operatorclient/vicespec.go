package operatorclient

import "fmt"

// CurrentVICESpecVersion is the VICESpec wire-contract version that this build
// of app-exposer emits and this build of vice-operator understands. The
// operator rejects specs whose SpecVersion it cannot handle, and the scheduler
// treats "operator too old for this spec version" as "skip this operator,"
// the same way it treats a capacity miss. Bump this whenever VICESpec changes
// in a way an older operator could not faithfully build.
const CurrentVICESpecVersion = 1

// VICESpec is the cluster-agnostic description of a VICE analysis that
// app-exposer sends to a vice-operator. It carries *what the analysis is*; the
// operator decides *how to realize it on this cluster* and builds the concrete
// k8s objects (Deployment, Service, HTTPRoute, ConfigMaps, PVs, PVCs, PDB)
// itself, injecting cluster-specific values at build time.
//
// The fields are resolved values, not raw model.Job internals: app-exposer
// (where the model.Job methods live) computes iRODS paths, sorted arguments,
// output directory, excludes, etc., so the operator never needs a model.Job
// dependency. The single deliberate exception is resource asks (see
// ResourceSpec), which ride raw because clamping to cluster limits is a policy
// the operator owns. The authoritative field-by-field mapping is in
// plans/operator-side-bundle-construction-field-audit.md.
type VICESpec struct {
	// SpecVersion identifies the wire-contract version. See
	// CurrentVICESpecVersion. The operator rejects versions it cannot build.
	SpecVersion int `json:"specVersion"`

	// Identity & labels. These populate the fixed label set the operator
	// stamps on every resource (the old jobInfo.JobLabels assembly). The
	// "subdomain" label is not carried — the operator recomputes it from
	// UserID + ExternalID via the shared common.Subdomain.
	AnalysisID  AnalysisID `json:"analysisID"`  // job.ID; canonical id, cleanup key
	ExternalID  ExternalID `json:"externalID"`  // job.InvocationID; basis for resource names
	JobName     string     `json:"jobName"`     // job.Name; label "analysis-name"; required, non-empty
	AppID       string     `json:"appID"`       //
	AppName     string     `json:"appName"`     //
	UserID      string     `json:"userID"`      //
	Submitter   string     `json:"submitter"`   // raw username; operator applies UserSuffix
	UserLoginIP string     `json:"userLoginIP"` // resolved via Apps.GetUserIP — the only DB-derived input

	// The interactive analysis container. VICE is single-step by invariant,
	// so this is one container, not a list.
	Container ContainerSpec `json:"container"`

	// Environment for the analysis container (model.Step.Environment).
	Environment map[string]string `json:"environment,omitempty"`

	// Resource asks (raw; the operator applies its own default/clamp policy)
	// and GPU request. GPU is nil when the analysis requests no GPU.
	Resources ResourceSpec `json:"resources"`
	GPU       *GPUSpec     `json:"gpu,omitempty"`

	// Data movement. Inputs is the full input list used to build CSI input
	// volume mappings; InputPathListPaths is the resolved ticketless subset
	// used to build the input-path-list ConfigMap. Ticket status never crosses
	// the wire — it is an iRODS access-control concern handled by porklock/CSI
	// at transfer time, not in any k8s object.
	UserHome           string        `json:"userHome"`
	OutputDirectory    string        `json:"outputDirectory"`              // resolved model.Job.OutputDirectory()
	ExcludeArguments   []string      `json:"excludeArguments,omitempty"`   // resolved model.Job.ExcludeArguments()
	Inputs             []InputSpec   `json:"inputs,omitempty"`             // all inputs — CSI volume mappings
	InputPathListPaths []string      `json:"inputPathListPaths,omitempty"` // resolved ticketless subset
	FileMetadata       []MetadataAVU `json:"fileMetadata,omitempty"`       // porklock upload metadata triples
}

// ContainerSpec describes the interactive analysis container. All values are
// resolved app-exposer-side from model.Job.Steps[0].Component.Container; in
// particular Arguments is the output of model.Step.Arguments() (executable plus
// sorted parameters, with backwards-compat image detection already applied), so
// the operator never re-derives DE argument semantics.
type ContainerSpec struct {
	Image      string   `json:"image"`
	Tag        string   `json:"tag"`
	UID        int      `json:"uid"`
	Ports      []int    `json:"ports,omitempty"`
	EntryPoint string   `json:"entryPoint,omitempty"`
	WorkingDir string   `json:"workingDir"` // resolved; default /de-app-work
	Arguments  []string `json:"arguments,omitempty"`
}

// ResourceSpec carries the analysis's raw resource asks. Unlike the rest of
// VICESpec these are not pre-clamped: the default/limit policy ("what this
// cluster grants") is cluster configuration the operator owns, so the operator
// runs the clamp at build time. The vice-proxy sidecar's resources come
// entirely from operator config and are not represented here.
type ResourceSpec struct {
	MinCPUCores       float32 `json:"minCPUCores"`                 // model MinCPUCores (request)
	MaxCPUCores       float32 `json:"maxCPUCores"`                 // model MaxCPUCores (limit)
	MinMemoryBytes    int64   `json:"minMemoryBytes"`              // model MinMemoryLimit (request)
	MaxMemoryBytes    int64   `json:"maxMemoryBytes"`              // model MemoryLimit (limit)
	MinDiskBytes      int64   `json:"minDiskBytes"`                // model MinDiskSpace; sizes the working-dir PVC
	SharedMemoryBytes *int64  `json:"sharedMemoryBytes,omitempty"` // resolved /dev/shm device; nil when none
}

// GPUSpec is the canonical GPU request. The operator maps it onto its own
// resource names (nvidia.com/gpu vs amd.com/gpu) and node-label scheme — the
// work the TransformGPUVendor / TransformGPUModels transforms do today.
// VICESpec.GPU is nil when the analysis requests no GPU.
//
// Vendor is canonical (GPUVendorNvidia / GPUVendorAMD). The model.Job has no
// vendor field today, so app-exposer defaults it to nvidia — but carrying it
// explicitly (rather than hardcoding nvidia in the scheduler) is the plumbing
// for genuinely multi-vendor scheduling: once an analysis can express AMD, only
// this field changes. An empty Vendor is read as nvidia for backwards
// compatibility (see VICESpec.RequestedGPUVendor).
type GPUSpec struct {
	Vendor string   `json:"vendor"`           // canonical GPUVendorNvidia | GPUVendorAMD; empty defaults to nvidia
	Count  int64    `json:"count"`            // resolved from MinGPUs/MaxGPUs (or legacy /dev/nvidia*)
	Models []string `json:"models,omitempty"` // canonical GFD names, e.g. "NVIDIA-A10G"; empty = any
}

// InputSpec is one resolved analysis input, used to build the CSI input volume
// mappings. IRODSPath is the resolved model.StepInput.IRODSPath() — a trailing
// "/" denotes a collection, which is why no separate multiplicity field is
// needed. Type selects the CSI resource-type branch.
type InputSpec struct {
	IRODSPath string `json:"irodsPath"`
	Type      string `json:"type"` // fileinput | multifileselector | folderinput
}

// MetadataAVU is an attribute/value/unit metadata triple applied to the
// analysis's output files. It mirrors model.FileMetadata but is defined here so
// VICESpec — and therefore the operator — carries no model.Job dependency.
type MetadataAVU struct {
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
	Unit      string `json:"unit"`
}

// Validate checks the spec-shape invariants the operator needs before it can
// build k8s objects. Unlike AnalysisBundle.Validate — which re-walked already-
// built objects to confirm their analysis-id labels — the operator guarantees
// that label invariant by construction (it stamps the labels it builds), so
// validation here is reduced to the required scalar fields. SpecVersion
// compatibility is checked separately by the receiver against the versions it
// supports, not here.
func (s *VICESpec) Validate() error {
	if s.AnalysisID == "" {
		return fmt.Errorf("analysisID is required")
	}
	if s.ExternalID == "" {
		return fmt.Errorf("externalID is required")
	}
	// JobName becomes the "analysis-name" label, which must not be empty.
	if s.JobName == "" {
		return fmt.Errorf("jobName is required")
	}
	if s.Submitter == "" {
		return fmt.Errorf("submitter is required")
	}
	if s.Container.Image == "" {
		return fmt.Errorf("container image is required")
	}
	return nil
}

// RequestedGPUVendor returns the canonical GPU vendor the analysis requires, or
// "" when it requests no GPU. An unset GPU.Vendor defaults to nvidia. This is
// the VICESpec counterpart to AnalysisBundle.RequestedGPUVendor — the scheduler
// reads it to skip operators whose cluster GPU vendor does not match, but reads
// it directly from the spec rather than reverse-engineering it from a built
// Deployment.
func (s *VICESpec) RequestedGPUVendor() string {
	if s.GPU == nil {
		return ""
	}
	if s.GPU.Vendor == "" {
		return GPUVendorNvidia
	}
	return s.GPU.Vendor
}

// RequestedGPUModels returns the canonical GPU model names the analysis
// requires, or nil when it has no model constraint. VICESpec counterpart to
// AnalysisBundle.RequestedGPUModels.
func (s *VICESpec) RequestedGPUModels() []string {
	if s.GPU == nil {
		return nil
	}
	return s.GPU.Models
}
