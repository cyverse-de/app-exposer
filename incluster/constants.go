package incluster

const (
	analysisContainerName = "analysis"

	porklockConfigVolumeName = "porklock-config"
	porklockConfigSecretName = "porklock-config"
	porklockConfigMountPath  = "/etc/porklock"

	csiDriverName                      = "irods.csi.cyverse.org"
	csiDriverStorageClassName          = "irods-sc"
	csiDriverDataVolumeNamePrefix      = "csi-data-volume"
	csiDriverDataVolumeClaimNamePrefix = "csi-data-volume-claim"
	csiDriverInputVolumeMountPath      = "/input"
	csiDriverOutputVolumeMountPath     = "/output"
	csiDriverLocalMountPath            = "/data-store"

	// The file transfers volume serves as the working directory when IRODS CSI Driver integration is disabled.
	fileTransfersVolumeName        = "input-files"
	fileTransfersContainerName     = "input-files"
	fileTransfersInitContainerName = "input-files-init"
	fileTransfersInputsMountPath   = "/input-files"

	// Constants for shared memory volumes.
	sharedMemoryVolumeName = "shared-memory"

	// The working directory volume serves as the working directory when IRODS CSI Driver integration is enabled.
	workingDirVolumeName             = "working-dir"
	workingDirInitContainerName      = "working-dir-init"
	workingDirInitContainerMountPath = "/working-dir"

	viceProxyContainerName = "vice-proxy"
	viceProxyPort          = int32(60002)
	viceProxyPortName      = "tcp-proxy"
	viceProxyServicePort   = int32(60000)

	excludesMountPath  = "/excludes"
	excludesFileName   = "excludes-file"
	excludesVolumeName = "excludes-file"

	inputPathListMountPath  = "/input-paths"
	inputPathListFileName   = "input-path-list"
	inputPathListVolumeName = "input-path-list"

	irodsConfigFilePath = "/etc/porklock/irods-config.properties"

	fileTransfersPortName = "tcp-input"
	fileTransfersPort     = int32(60001)

	downloadBasePath = "/download"
	uploadBasePath   = "/upload"
	downloadKind     = "download"
	uploadKind       = "upload"

	gpuTolerationKey      = "gpu"
	gpuTolerationOperator = "Equal"
	gpuTolerationValue    = "true"
	gpuTolerationEffect   = "NoSchedule"

	analysisAffinityKey      = "analysis"
	analysisAffinityOperator = "Exists"

	gpuAffinityKey      = "gpu"
	gpuAffinityOperator = "In"
	gpuAffinityValue    = "true"

	userSuffix = "@iplantcollaborative.org"

	shmDevice = "/dev/shm"
)

func int32Ptr(i int32) *int32 { return &i }
func int64Ptr(i int64) *int64 { return &i }
