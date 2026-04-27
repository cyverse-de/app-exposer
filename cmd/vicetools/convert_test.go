package vicetools

import (
	"testing"

	"github.com/cyverse-de/model/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validParams is a reusable LaunchParams that satisfies ConvertToJob's
// required-field validation. Individual tests clone and mutate it.
func validParams() LaunchParams {
	return LaunchParams{
		User:      "tester",
		UserID:    "00000000-0000-0000-0000-000000000001",
		OutputDir: "/iplant/home/tester/out",
		Email:     "tester@example.org",
	}
}

// minimalExport is a reusable VICEAppExport that exercises the straight-line
// conversion path (no parameter groups, no volumes, no proxy settings).
func minimalExport() *VICEAppExport {
	return &VICEAppExport{
		ExportVersion: "1.0",
		SourceAppID:   "src-app-id",
		App: AppDefinition{
			Name:        "example-app",
			Description: "example description",
			WikiURL:     "https://example.org/wiki",
			Version:     "1.0.0",
		},
		Tool: ToolDefinition{
			Name:             "example-tool",
			Description:      "example tool description",
			Version:          "2.0.0",
			Type:             "interactive",
			Interactive:      true,
			TimeLimitSeconds: 3600,
			Restricted:       false,
			Location:         "/opt/example",
			ContainerImage: ContainerImageDef{
				Name: "example/image",
				Tag:  "latest",
				URL:  "https://harbor.example.org/example/image",
			},
			ContainerSettings: ContainerSettingsDef{
				CPUShares:      2048,
				MemoryLimit:    8 * 1024 * 1024 * 1024,
				MinMemoryLimit: 2 * 1024 * 1024 * 1024,
				MinCPUCores:    1,
				MaxCPUCores:    4,
				MinDiskSpace:   10 * 1024 * 1024 * 1024,
				NetworkMode:    "bridge",
				UID:            1000,
			},
		},
	}
}

func TestConvertToJob_RequiredParams(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *LaunchParams)
		errSub string
	}{
		{"missing user", func(p *LaunchParams) { p.User = "" }, "user"},
		{"missing user-id", func(p *LaunchParams) { p.UserID = "" }, "user-id"},
		{"missing output-dir", func(p *LaunchParams) { p.OutputDir = "" }, "output-dir"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validParams()
			tt.mutate(&p)
			job, err := ConvertToJob(minimalExport(), p)
			require.Error(t, err)
			assert.Nil(t, job)
			assert.Contains(t, err.Error(), tt.errSub)
		})
	}
}

func TestConvertToJob_MinimalHappyPath(t *testing.T) {
	job, err := ConvertToJob(minimalExport(), validParams())
	require.NoError(t, err)
	require.NotNil(t, job)

	assert.Equal(t, "src-app-id", job.AppID)
	assert.Equal(t, "example-app", job.AppName)
	assert.Equal(t, "example description", job.AppDescription)
	assert.Equal(t, "example-app-analysis", job.Name)
	assert.Equal(t, "https://example.org/wiki", job.WikiURL)
	assert.Equal(t, "tester", job.Submitter)
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", job.UserID)
	assert.Equal(t, "tester@example.org", job.Email)
	assert.Equal(t, "/iplant/home/tester/out", job.OutputDir)
	assert.Equal(t, "interapps", job.ExecutionTarget)
	assert.Equal(t, "analysis", job.Type)
	assert.NotEmpty(t, job.ID)
	assert.NotEmpty(t, job.InvocationID)
	require.Len(t, job.Steps, 1)

	step := job.Steps[0]
	assert.Equal(t, "example-tool", step.Component.Name)
	assert.Equal(t, "interactive", step.Component.Type)
	assert.Equal(t, 3600, step.Component.TimeLimit)
	assert.True(t, step.Component.IsInteractive)

	container := step.Component.Container
	assert.Equal(t, "example/image", container.Image.Name)
	assert.Equal(t, "latest", container.Image.Tag)
	assert.Equal(t, int64(2048), container.CPUShares)
	assert.Equal(t, int64(8*1024*1024*1024), container.MemoryLimit)
	assert.Equal(t, "bridge", container.NetworkMode)
	assert.Equal(t, 1000, container.UID)
	assert.Empty(t, container.GPUModels, "minimal export has no GPU models")
}

func TestConvertToJob_PropagatesGPUModels(t *testing.T) {
	// Regression test for the bug fixed alongside this file's introduction:
	// GPUModels from ContainerSettingsDef must reach model.Container so the
	// launch path can emit nvidia.com/gpu.product node affinity.
	export := minimalExport()
	export.Tool.ContainerSettings.MinGPUs = 1
	export.Tool.ContainerSettings.MaxGPUs = 2
	export.Tool.ContainerSettings.GPUModels = []string{"NVIDIA-A16", "NVIDIA-A100"}

	job, err := ConvertToJob(export, validParams())
	require.NoError(t, err)
	require.Len(t, job.Steps, 1)

	container := job.Steps[0].Component.Container
	assert.Equal(t, int64(1), container.MinGPUs)
	assert.Equal(t, int64(2), container.MaxGPUs)
	assert.Equal(t, []string{"NVIDIA-A16", "NVIDIA-A100"}, container.GPUModels)
}

func TestConvertToJob_ParameterClassification(t *testing.T) {
	// Parameters should be split into inputs, outputs, and generic params
	// based on their Type. Default values flow through on each bucket.
	export := minimalExport()
	export.App.ParameterGroups = []ParameterGroupDef{{
		Name:         "main",
		DisplayOrder: 0,
		IsVisible:    true,
		Parameters: []ParameterDef{
			{Name: "in-file", Type: "FileInput", DefaultValue: "/in/a.txt", Ordering: 1},
			{Name: "out-file", Type: "FileOutput", Ordering: 2},
			{Name: "flag", Type: "Text", DefaultValue: "--verbose", Ordering: 3},
		},
	}}

	job, err := ConvertToJob(export, validParams())
	require.NoError(t, err)
	require.Len(t, job.Steps, 1)
	cfg := job.Steps[0].Config

	require.Len(t, cfg.Inputs, 1)
	assert.Equal(t, "in-file", cfg.Inputs[0].Name)
	assert.Equal(t, "/in/a.txt", cfg.Inputs[0].Value)
	assert.NotEmpty(t, cfg.Inputs[0].ID)

	require.Len(t, cfg.Outputs, 1)
	assert.Equal(t, "out-file", cfg.Outputs[0].Name)

	require.Len(t, cfg.Params, 1)
	assert.Equal(t, "flag", cfg.Params[0].Name)
	assert.Equal(t, "--verbose", cfg.Params[0].Value)
	assert.Equal(t, 3, cfg.Params[0].Order)
	assert.NotEmpty(t, cfg.Params[0].ID)
}

func TestConvertToJob_PropagatesContainerCollections(t *testing.T) {
	// Ports, Devices, Volumes, VolumesFrom, and the interactive-apps proxy
	// settings should round-trip into the model.Container.
	export := minimalExport()
	export.Tool.ContainerSettings.Ports = []PortDef{{HostPort: 8443, ContainerPort: 8443, BindToHost: true}}
	export.Tool.ContainerSettings.Devices = []DeviceDef{{HostPath: "/dev/fuse", ContainerPath: "/dev/fuse"}}
	export.Tool.ContainerSettings.Volumes = []VolumeDef{{HostPath: "/mnt/scratch", ContainerPath: "/scratch"}}
	export.Tool.ContainerSettings.VolumesFrom = []VolumesFromDef{{
		Name:          "sidecar-image",
		Tag:           "v1",
		NamePrefix:    "sidecar",
		ReadOnly:      true,
		HostPath:      "/var/lib/sidecar",
		ContainerPath: "/sidecar",
	}}
	export.Tool.ContainerSettings.ProxySettings = &ProxySettingsDef{
		Image:       "proxy-image",
		Name:        "proxy-name",
		FrontendURL: "https://frontend.example.org",
	}

	job, err := ConvertToJob(export, validParams())
	require.NoError(t, err)
	require.Len(t, job.Steps, 1)

	container := job.Steps[0].Component.Container
	require.Len(t, container.Ports, 1)
	assert.Equal(t, model.Ports{HostPort: 8443, ContainerPort: 8443, BindToHost: true}, container.Ports[0])

	require.Len(t, container.Devices, 1)
	assert.Equal(t, model.Device{HostPath: "/dev/fuse", ContainerPath: "/dev/fuse"}, container.Devices[0])

	require.Len(t, container.Volumes, 1)
	assert.Equal(t, model.Volume{HostPath: "/mnt/scratch", ContainerPath: "/scratch"}, container.Volumes[0])

	require.Len(t, container.VolumesFrom, 1)
	assert.Equal(t, "sidecar-image", container.VolumesFrom[0].Name)
	assert.True(t, container.VolumesFrom[0].ReadOnly)

	assert.Equal(t, "proxy-image", container.InteractiveApps.ProxyImage)
	assert.Equal(t, "https://frontend.example.org", container.InteractiveApps.FrontendURL)
}

func TestIsInputType(t *testing.T) {
	tests := []struct {
		typeName string
		want     bool
	}{
		{"FileInput", true},
		{"FolderInput", true},
		{"MultiFileSelector", true},
		{"FileFolderInput", true},
		{"FileOutput", false},
		{"Text", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			assert.Equal(t, tt.want, isInputType(tt.typeName))
		})
	}
}

func TestIsOutputType(t *testing.T) {
	tests := []struct {
		typeName string
		want     bool
	}{
		{"FileOutput", true},
		{"FolderOutput", true},
		{"MultiFileOutput", true},
		{"FileInput", false},
		{"Text", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			assert.Equal(t, tt.want, isOutputType(tt.typeName))
		})
	}
}
