package constants

const (
	AnalysisContainerName = "analysis"

	PorklockConfigVolumeName = "porklock-config"
	PorklockConfigSecretName = "porklock-config"
	PorklockConfigMountPath  = "/etc/porklock"

	CSIDriverName                      = "irods.csi.cyverse.org"
	CSIDriverStorageClassName          = "irods-sc"
	CSIDriverDataVolumeNamePrefix      = "csi-data-volume"
	CSIDriverDataVolumeClaimNamePrefix = "csi-data-volume-claim"
	CSIDriverInputVolumeMountPath      = "/input"
	CSIDriverOutputVolumeMountPath     = "/output"
	CSIDriverLocalMountPath            = "/data-store"

	// The file transfers volume serves as the working directory when IRODS CSI Driver integration is disabled.
	FileTransfersContainerName     = "input-files"
	FileTransfersInitContainerName = "input-files-init"
	FileTransfersInputsMountPath   = "/input-files"

	// Constants for shared memory volumes.
	SharedMemoryVolumeName = "shared-memory"

	// The working directory volume serves as the working directory when IRODS CSI Driver integration is enabled.
	WorkingDirVolumeName             = "working-dir"
	WorkingDirInitContainerName      = "working-dir-init"
	WorkingDirInitContainerMountPath = "/working-dir"

	// The persistent data volume name. The volume is used to persist data locally across restarts.
	LocalDirVolumeName = "analysis-data"

	VICEProxyContainerName = "vice-proxy"
	VICEProxyPort          = int32(60002)
	VICEProxyPortName      = "tcp-proxy"
	VICEProxyServicePort   = int32(60000)

	ExcludesMountPath  = "/excludes"
	ExcludesFileName   = "excludes-file"
	ExcludesVolumeName = "excludes-file"

	InputPathListMountPath  = "/input-paths"
	InputPathListFileName   = "input-path-list"
	InputPathListVolumeName = "input-path-list"

	IRODSConfigFilePath = "/etc/porklock/irods-config.properties"

	FileTransfersPortName = "tcp-input"
	FileTransfersPort     = int32(60001)

	DownloadBasePath = "/download"
	UploadBasePath   = "/upload"
	DownloadKind     = "download"
	UploadKind       = "upload"

	GPUTolerationKey      = "gpu"
	GPUTolerationOperator = "Equal"
	GPUTolerationValue    = "true"
	GPUTolerationEffect   = "NoSchedule"

	AnalysisAffinityKey      = "analysis"
	AnalysisAffinityOperator = "Exists"

	GPUAffinityKey      = "gpu"
	GPUAffinityOperator = "In"
	GPUAffinityValue    = "true"

	UserSuffix = "@iplantcollaborative.org"

	ShmDevice = "/dev/shm"
)

type AnalysisStatus string

const (
	Running   AnalysisStatus = "Running"
	Completed AnalysisStatus = "Completed"
	Failed    AnalysisStatus = "Failed"
)

// AnalysisKind corresponds to the job types in the database. We've started using the term
// "analysis" instead of job and type is a reserved word in Go.
type AnalysisKind string

const (
	// Interactive analyses are VICE analyses.
	Interactive AnalysisKind = "interactive"

	// Internal analyses are tools like URL import that aren't accessible directly by the user.
	Internal AnalysisKind = "internal"

	// Executable analyses are batch analyses.
	Executable AnalysisKind = "executable"
)

func Int32Ptr(i int32) *int32 { return &i }
func Int64Ptr(i int64) *int64 { return &i }
