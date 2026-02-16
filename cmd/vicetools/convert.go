package vicetools

import (
	"fmt"

	"github.com/cyverse-de/model/v9"
	"github.com/google/uuid"
)

// LaunchParams contains the runtime parameters needed to convert an export to a launchable job.
type LaunchParams struct {
	User      string // username (without domain suffix)
	UserID    string // user UUID
	OutputDir string // iRODS output directory
	Email     string // user email (optional)
}

// ConvertToJob builds a model.Job from a VICEAppExport and runtime parameters.
func ConvertToJob(export *VICEAppExport, params LaunchParams) (*model.Job, error) {
	if params.User == "" {
		return nil, fmt.Errorf("user is required")
	}
	if params.UserID == "" {
		return nil, fmt.Errorf("user-id is required")
	}
	if params.OutputDir == "" {
		return nil, fmt.Errorf("output-dir is required")
	}

	tool := &export.Tool
	app := &export.App
	cs := &tool.ContainerSettings

	// Build container ports
	var ports []model.Ports
	for _, p := range cs.Ports {
		ports = append(ports, model.Ports{
			HostPort:      p.HostPort,
			ContainerPort: p.ContainerPort,
			BindToHost:    p.BindToHost,
		})
	}

	// Build container volumes
	var volumes []model.Volume
	for _, v := range cs.Volumes {
		volumes = append(volumes, model.Volume{
			HostPath:      v.HostPath,
			ContainerPath: v.ContainerPath,
		})
	}

	// Build container devices
	var devices []model.Device
	for _, d := range cs.Devices {
		devices = append(devices, model.Device{
			HostPath:      d.HostPath,
			ContainerPath: d.ContainerPath,
		})
	}

	// Build volumes from
	var volumesFrom []model.VolumesFrom
	for _, vf := range cs.VolumesFrom {
		volumesFrom = append(volumesFrom, model.VolumesFrom{
			Name:          vf.Name,
			Tag:           vf.Tag,
			URL:           vf.URL,
			NamePrefix:    vf.NamePrefix,
			HostPath:      vf.HostPath,
			ContainerPath: vf.ContainerPath,
			ReadOnly:      vf.ReadOnly,
		})
	}

	// Build interactive apps settings
	var interactiveApps model.InteractiveApps
	if cs.ProxySettings != nil {
		ps := cs.ProxySettings
		interactiveApps = model.InteractiveApps{
			ProxyImage:  ps.Image,
			ProxyName:   ps.Name,
			FrontendURL: ps.FrontendURL,
			CASURL:      ps.CASURL,
			CASValidate: ps.CASValidate,
			SSLCertPath: ps.SSLCertPath,
			SSLKeyPath:  ps.SSLKeyPath,
		}
	}

	// Build step params from parameter defaults
	var stepParams []model.StepParam
	var stepInputs []model.StepInput
	var stepOutputs []model.StepOutput

	for _, group := range app.ParameterGroups {
		for _, p := range group.Parameters {
			switch {
			case isInputType(p.Type):
				stepInputs = append(stepInputs, model.StepInput{
					ID:    uuid.New().String(),
					Name:  p.Name,
					Value: p.DefaultValue,
					Type:  p.Type,
				})
			case isOutputType(p.Type):
				stepOutputs = append(stepOutputs, model.StepOutput{
					Name: p.Name,
					Type: p.Type,
				})
			default:
				stepParams = append(stepParams, model.StepParam{
					ID:    uuid.New().String(),
					Name:  p.Name,
					Value: p.DefaultValue,
					Order: p.Ordering,
					Type:  p.Type,
				})
			}
		}
	}

	container := model.Container{
		Image: model.ContainerImage{
			Name: tool.ContainerImage.Name,
			Tag:  tool.ContainerImage.Tag,
			URL:  tool.ContainerImage.URL,
		},
		Ports:           ports,
		Volumes:         volumes,
		Devices:         devices,
		VolumesFrom:     volumesFrom,
		InteractiveApps: interactiveApps,
		CPUShares:       cs.CPUShares,
		MemoryLimit:     cs.MemoryLimit,
		MinMemoryLimit:  cs.MinMemoryLimit,
		MinCPUCores:     float32(cs.MinCPUCores),
		MaxCPUCores:     float32(cs.MaxCPUCores),
		MinGPUs:         cs.MinGPUs,
		MaxGPUs:         cs.MaxGPUs,
		MinDiskSpace:    cs.MinDiskSpace,
		PIDsLimit:       cs.PIDsLimit,
		NetworkMode:     cs.NetworkMode,
		WorkingDir:      cs.WorkingDirectory,
		EntryPoint:      cs.EntryPoint,
		SkipTmpMount:    cs.SkipTmpMount,
		UID:             cs.UID,
	}

	step := model.Step{
		Component: model.StepComponent{
			Container:     container,
			Name:          tool.Name,
			Description:   tool.Description,
			Location:      tool.Location,
			Type:          tool.Type,
			TimeLimit:     tool.TimeLimitSeconds,
			Restricted:    tool.Restricted,
			IsInteractive: tool.Interactive,
		},
		Config: model.StepConfig{
			Params:  stepParams,
			Inputs:  stepInputs,
			Outputs: stepOutputs,
		},
	}

	job := &model.Job{
		AppID:           export.SourceAppID,
		AppName:         app.Name,
		AppDescription:  app.Description,
		WikiURL:         app.WikiURL,
		InvocationID:    uuid.New().String(),
		Submitter:       params.User,
		UserID:          params.UserID,
		Email:           params.Email,
		OutputDir:       params.OutputDir,
		ExecutionTarget: "interapps",
		Type:            "analysis",
		Steps:           []model.Step{step},
	}

	return job, nil
}

func isInputType(typeName string) bool {
	switch typeName {
	case "FileInput", "FolderInput", "MultiFileSelector", "FileFolderInput":
		return true
	}
	return false
}

func isOutputType(typeName string) bool {
	switch typeName {
	case "FileOutput", "FolderOutput", "MultiFileOutput":
		return true
	}
	return false
}
