package operatorclient

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validVICESpec() VICESpec {
	return VICESpec{
		SpecVersion: CurrentVICESpecVersion,
		AnalysisID:  "analysis-1",
		ExternalID:  "external-1",
		JobName:     "my analysis",
		AppID:       "app-1",
		AppName:     "JupyterLab",
		UserID:      "user-1",
		Submitter:   "someuser",
		UserLoginIP: "10.0.0.1",
		Container: ContainerSpec{
			Image:      "cyverse/jupyter",
			Tag:        "latest",
			UID:        1000,
			Ports:      []int{8888},
			WorkingDir: "/de-app-work",
		},
	}
}

func TestVICESpecValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*VICESpec)
		wantErr string
	}{
		{name: "valid", mutate: func(*VICESpec) {}},
		{name: "missing analysisID", mutate: func(s *VICESpec) { s.AnalysisID = "" }, wantErr: "analysisID is required"},
		{name: "missing externalID", mutate: func(s *VICESpec) { s.ExternalID = "" }, wantErr: "externalID is required"},
		{name: "missing jobName", mutate: func(s *VICESpec) { s.JobName = "" }, wantErr: "jobName is required"},
		{name: "missing submitter", mutate: func(s *VICESpec) { s.Submitter = "" }, wantErr: "submitter is required"},
		{name: "missing image", mutate: func(s *VICESpec) { s.Container.Image = "" }, wantErr: "container image is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVICESpec()
			tt.mutate(&spec)
			err := spec.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

// TestVICESpecRoundTrip guards the wire contract: a spec survives a JSON
// round-trip unchanged, including the nil-vs-set distinction on the optional
// GPU and shared-memory fields.
func TestVICESpecRoundTrip(t *testing.T) {
	shm := int64(1 << 30)
	spec := validVICESpec()
	spec.Environment = map[string]string{"FOO": "bar"}
	spec.Resources = ResourceSpec{
		MinCPUCores:       1,
		MaxCPUCores:       4,
		MinMemoryBytes:    1 << 30,
		MaxMemoryBytes:    8 << 30,
		MinDiskBytes:      16 << 30,
		SharedMemoryBytes: &shm,
	}
	spec.GPU = &GPUSpec{Vendor: GPUVendorNvidia, Count: 1, Models: []string{"NVIDIA-A10G"}}
	spec.Inputs = []InputSpec{{IRODSPath: "/zone/home/user/in.txt", Type: "fileinput"}}
	spec.InputPathListPaths = []string{"/zone/home/user/in.txt"}
	spec.FileMetadata = []MetadataAVU{{Attribute: "ipc-analysis-id", Value: "analysis-1", Unit: ""}}

	data, err := json.Marshal(spec)
	require.NoError(t, err)

	var got VICESpec
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, spec, got)
}

func TestVICESpecRequestedGPU(t *testing.T) {
	tests := []struct {
		name       string
		gpu        *GPUSpec
		wantVendor string
		wantModels []string
	}{
		{name: "no gpu", gpu: nil, wantVendor: "", wantModels: nil},
		{name: "unset vendor defaults to nvidia", gpu: &GPUSpec{Count: 1}, wantVendor: GPUVendorNvidia, wantModels: nil},
		{name: "explicit nvidia", gpu: &GPUSpec{Vendor: GPUVendorNvidia, Count: 1}, wantVendor: GPUVendorNvidia},
		{name: "explicit amd", gpu: &GPUSpec{Vendor: GPUVendorAMD, Count: 2}, wantVendor: GPUVendorAMD},
		{
			name:       "with models",
			gpu:        &GPUSpec{Vendor: GPUVendorNvidia, Count: 1, Models: []string{"NVIDIA-A10G", "NVIDIA-L4"}},
			wantVendor: GPUVendorNvidia,
			wantModels: []string{"NVIDIA-A10G", "NVIDIA-L4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVICESpec()
			spec.GPU = tt.gpu
			assert.Equal(t, tt.wantVendor, spec.RequestedGPUVendor())
			assert.Equal(t, tt.wantModels, spec.RequestedGPUModels())
		})
	}
}

// TestVICESpecGPUOmittedWhenNil confirms GPU and SharedMemoryBytes serialize
// away entirely when unset, so a no-GPU analysis carries no GPU object on the
// wire (the operator reads "absent" as "no GPU requested").
func TestVICESpecGPUOmittedWhenNil(t *testing.T) {
	spec := validVICESpec()
	data, err := json.Marshal(spec)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "\"gpu\"")
	assert.NotContains(t, string(data), "sharedMemoryBytes")
}
