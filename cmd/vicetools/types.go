package vicetools

import "time"

// VICEAppExport is the top-level structure for exporting/importing VICE app definitions.
type VICEAppExport struct {
	ExportVersion string        `json:"export_version"`
	ExportDate    time.Time     `json:"export_date"`
	SourceAppID   string        `json:"source_app_id"`
	App           AppDefinition `json:"app"`
	Tool          ToolDefinition `json:"tool"`
}

// AppDefinition contains the app-level metadata.
type AppDefinition struct {
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	WikiURL         string              `json:"wiki_url,omitempty"`
	Version         string              `json:"version"`
	IntegrationData IntegrationDataDef  `json:"integration_data"`
	ParameterGroups []ParameterGroupDef `json:"parameter_groups,omitempty"`
	References      []string            `json:"references,omitempty"`
}

// ToolDefinition contains the tool-level metadata.
type ToolDefinition struct {
	Name              string              `json:"name"`
	Description       string              `json:"description"`
	Version           string              `json:"version"`
	Type              string              `json:"type"`
	Interactive       bool                `json:"interactive"`
	TimeLimitSeconds  int                 `json:"time_limit_seconds"`
	Restricted        bool                `json:"restricted"`
	Location          string              `json:"location,omitempty"`
	Attribution       string              `json:"attribution,omitempty"`
	ContainerImage    ContainerImageDef   `json:"container_image"`
	ContainerSettings ContainerSettingsDef `json:"container_settings"`
	IntegrationData   IntegrationDataDef  `json:"integration_data"`
}

// IntegrationDataDef holds integrator information.
type IntegrationDataDef struct {
	IntegratorName  string `json:"integrator_name"`
	IntegratorEmail string `json:"integrator_email"`
}

// ContainerImageDef describes a container image.
type ContainerImageDef struct {
	Name         string `json:"name"`
	Tag          string `json:"tag"`
	URL          string `json:"url,omitempty"`
	OSGImagePath string `json:"osg_image_path,omitempty"`
}

// ContainerSettingsDef holds container configuration.
type ContainerSettingsDef struct {
	CPUShares       int64              `json:"cpu_shares"`
	MemoryLimit     int64              `json:"memory_limit"`
	MinMemoryLimit  int64              `json:"min_memory_limit"`
	MinCPUCores     float64            `json:"min_cpu_cores"`
	MaxCPUCores     float64            `json:"max_cpu_cores"`
	MinGPUs         int64              `json:"min_gpus"`
	MaxGPUs         int64              `json:"max_gpus"`
	MinDiskSpace    int64              `json:"min_disk_space"`
	NetworkMode     string             `json:"network_mode,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	EntryPoint      string             `json:"entrypoint,omitempty"`
	UID             int                `json:"uid"`
	SkipTmpMount    bool               `json:"skip_tmp_mount"`
	PIDsLimit       int64              `json:"pids_limit"`
	Ports           []PortDef          `json:"ports,omitempty"`
	Devices         []DeviceDef        `json:"devices,omitempty"`
	Volumes         []VolumeDef        `json:"volumes,omitempty"`
	VolumesFrom     []VolumesFromDef   `json:"volumes_from,omitempty"`
	ProxySettings   *ProxySettingsDef  `json:"proxy_settings,omitempty"`
}

// PortDef describes a port mapping.
type PortDef struct {
	HostPort      int  `json:"host_port"`
	ContainerPort int  `json:"container_port"`
	BindToHost    bool `json:"bind_to_host"`
}

// DeviceDef describes a device mapping.
type DeviceDef struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
}

// VolumeDef describes a volume mapping.
type VolumeDef struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
}

// VolumesFromDef describes a data container to mount volumes from.
type VolumesFromDef struct {
	Name         string `json:"name"`
	Tag          string `json:"tag"`
	URL          string `json:"url,omitempty"`
	NamePrefix   string `json:"name_prefix,omitempty"`
	ReadOnly     bool   `json:"read_only"`
	HostPath     string `json:"host_path,omitempty"`
	ContainerPath string `json:"container_path,omitempty"`
}

// ProxySettingsDef holds interactive apps proxy configuration.
type ProxySettingsDef struct {
	Image        string `json:"image,omitempty"`
	Name         string `json:"name,omitempty"`
	FrontendURL  string `json:"frontend_url,omitempty"`
	CASURL       string `json:"cas_url,omitempty"`
	CASValidate  string `json:"cas_validate,omitempty"`
	SSLCertPath  string `json:"ssl_cert_path,omitempty"`
	SSLKeyPath   string `json:"ssl_key_path,omitempty"`
}

// ParameterGroupDef defines a group of parameters.
type ParameterGroupDef struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	Label        string         `json:"label,omitempty"`
	DisplayOrder int            `json:"display_order"`
	IsVisible    bool           `json:"is_visible"`
	Parameters   []ParameterDef `json:"parameters,omitempty"`
}

// ParameterDef defines a single parameter.
type ParameterDef struct {
	Name         string              `json:"name"`
	Label        string              `json:"label,omitempty"`
	Description  string              `json:"description,omitempty"`
	Type         string              `json:"type"`
	Ordering     int                 `json:"ordering"`
	Required     bool                `json:"required"`
	IsVisible    bool                `json:"is_visible"`
	OmitIfBlank  bool                `json:"omit_if_blank"`
	DefaultValue string              `json:"default_value,omitempty"`
	Values       []ParameterValueDef `json:"values,omitempty"`
}

// ParameterValueDef defines a possible value for a parameter.
type ParameterValueDef struct {
	Name         string `json:"name,omitempty"`
	Value        string `json:"value,omitempty"`
	Description  string `json:"description,omitempty"`
	Label        string `json:"label,omitempty"`
	IsDefault    bool   `json:"is_default"`
	DisplayOrder int    `json:"display_order"`
}

// ImportResult holds the IDs created by an import operation.
type ImportResult struct {
	AppID      string `json:"app_id"`
	VersionID  string `json:"version_id"`
	ToolID     string `json:"tool_id"`
}
