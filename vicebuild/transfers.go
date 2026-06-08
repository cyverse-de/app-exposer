package vicebuild

import (
	"path"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	apiv1 "k8s.io/api/core/v1"
)

// fileTransferCommand returns the command for the vice-file-transfers
// service/init container (non-CSI clusters). Metadata triples become repeated
// -m args, matching model.FileMetadata.Argument().
func fileTransferCommand(spec *operatorclient.VICESpec) []string {
	cmd := []string{
		"/vice-file-transfers",
		"--listen-port", "60001",
		"--user", spec.Submitter,
		"--excludes-file", path.Join(constants.ExcludesMountPath, constants.ExcludesFileName),
		"--path-list-file", path.Join(constants.InputPathListMountPath, constants.InputPathListFileName),
		"--upload-destination", spec.OutputDirectory,
		"--irods-config", constants.IRODSConfigFilePath,
		"--invocation-id", string(spec.ExternalID),
	}
	for _, m := range spec.FileMetadata {
		cmd = append(cmd, "-m", m.Attribute+","+m.Value+","+m.Unit)
	}
	return cmd
}

// fileTransfersVolumeMounts returns the mounts for the file-transfers and
// input-staging containers (non-CSI clusters).
func (c *Config) fileTransfersVolumeMounts(spec *operatorclient.VICESpec) []apiv1.VolumeMount {
	mounts := []apiv1.VolumeMount{
		{
			Name:      constants.PorklockConfigVolumeName,
			MountPath: constants.PorklockConfigMountPath,
			ReadOnly:  true,
		},
		{
			Name:      workingDirVolumeName(spec),
			MountPath: constants.FileTransfersInputsMountPath,
			ReadOnly:  false,
		},
		{
			Name:      constants.ExcludesVolumeName,
			MountPath: constants.ExcludesMountPath,
			ReadOnly:  true,
		},
	}
	if hasTicketlessInputs(spec) {
		mounts = append(mounts, apiv1.VolumeMount{
			Name:      constants.InputPathListVolumeName,
			MountPath: constants.InputPathListMountPath,
			ReadOnly:  true,
		})
	}
	return mounts
}
