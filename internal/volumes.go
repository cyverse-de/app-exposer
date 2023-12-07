package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/cyverse-de/model/v6"
	apiv1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	defaultStorageCapacity, _ = resourcev1.ParseQuantity("5Gi")
)

// IRODSFSPathMapping defines a single path mapping that can be used by the iRODS CSI driver to create a mount point.
type IRODSFSPathMapping struct {
	IRODSPath           string `yaml:"irods_path" json:"irods_path"`
	MappingPath         string `yaml:"mapping_path" json:"mapping_path"`
	ResourceType        string `yaml:"resource_type" json:"resource_type"` // file or dir
	ReadOnly            bool   `yaml:"read_only" json:"read_only"`
	CreateDir           bool   `yaml:"create_dir" json:"create_dir"`
	IgnoreNotExistError bool   `yaml:"ignore_not_exist_error" json:"ignore_not_exist_error"`
}

func (i *Internal) getZoneMountPath() string {
	return fmt.Sprintf("%s/%s", csiDriverLocalMountPath, i.IRODSZone)
}

func (i *Internal) getCSIInputVolumeHandle(job *model.Job) string {
	return fmt.Sprintf("%s-handle-%s", csiDriverInputVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIOutputVolumeHandle(job *model.Job) string {
	return fmt.Sprintf("%s-handle-%s", csiDriverOutputVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIHomeVolumeHandle(job *model.Job) string {
	return fmt.Sprintf("%s-handle-%s", csiDriverHomeVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSISharedVolumeHandle(job *model.Job) string {
	return fmt.Sprintf("%s-handle-%s", csiDriverSharedVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIInputVolumeName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverInputVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIInputVolumeClaimName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverInputVolumeClaimNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIOutputVolumeName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverOutputVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIOutputVolumeClaimName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverOutputVolumeClaimNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIHomeVolumeName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverHomeVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIHomeVolumeClaimName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverHomeVolumeClaimNamePrefix, job.InvocationID)
}

func (i *Internal) getCSISharedVolumeName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverSharedVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSISharedVolumeClaimName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverSharedVolumeClaimNamePrefix, job.InvocationID)
}

func (i *Internal) getInputPathMappings(job *model.Job) ([]IRODSFSPathMapping, error) {
	mappings := []IRODSFSPathMapping{}
	// mark if the mapping path is already occupied
	// key = mount path, val = irods path
	mappingMap := map[string]string{}

	// Mount the input files.
	for _, step := range job.Steps {
		for _, stepInput := range step.Config.Inputs {
			irodsPath := stepInput.IRODSPath()
			if len(irodsPath) > 0 {
				var resourceType string
				if strings.ToLower(stepInput.Type) == "fileinput" {
					resourceType = "file"
				} else if strings.ToLower(stepInput.Type) == "multifileselector" {
					resourceType = "file"
				} else if strings.ToLower(stepInput.Type) == "folderinput" {
					resourceType = "dir"
				} else {
					// unknown
					return nil, fmt.Errorf("unknown step input type - %s", stepInput.Type)
				}

				mountPath := fmt.Sprintf("/%s", filepath.Base(irodsPath))
				// check if mountPath is already used by other input
				if existingIRODSPath, ok := mappingMap[mountPath]; ok {
					// exists - error
					return nil, fmt.Errorf("tried to mount an input file %s at %s already used by - %s", irodsPath, mountPath, existingIRODSPath)
				}
				mappingMap[mountPath] = irodsPath

				mapping := IRODSFSPathMapping{
					IRODSPath:           irodsPath,
					MappingPath:         mountPath,
					ResourceType:        resourceType,
					ReadOnly:            true,
					CreateDir:           false,
					IgnoreNotExistError: true,
				}

				mappings = append(mappings, mapping)
			}
		}
	}
	return mappings, nil
}

func (i *Internal) getOutputPathMapping(job *model.Job) IRODSFSPathMapping {
	// mount a single collection for output
	return IRODSFSPathMapping{
		IRODSPath:           job.OutputDirectory(),
		MappingPath:         "/",
		ResourceType:        "dir",
		ReadOnly:            false,
		CreateDir:           true,
		IgnoreNotExistError: true,
	}
}

func (i *Internal) getHomePathMapping(job *model.Job) IRODSFSPathMapping {
	// mount a single collection for home
	return IRODSFSPathMapping{
		IRODSPath:           job.UserHome,
		MappingPath:         "/",
		ResourceType:        "dir",
		ReadOnly:            false,
		CreateDir:           false,
		IgnoreNotExistError: true,
	}
}

func (i *Internal) getSharedPathMapping(job *model.Job) IRODSFSPathMapping {
	// mount a single collection for shared data
	sharedHomeFullPath := fmt.Sprintf("/%s/home/shared", i.IRODSZone)

	return IRODSFSPathMapping{
		IRODSPath:           sharedHomeFullPath,
		MappingPath:         "/",
		ResourceType:        "dir",
		ReadOnly:            false,
		CreateDir:           false,
		IgnoreNotExistError: true,
	}
}

func (i *Internal) getCSIInputVolumeLabels(ctx context.Context, job *model.Job) (map[string]string, error) {
	labels, err := i.labelsFromJob(ctx, job)
	if err != nil {
		return nil, err
	}

	labels["volume-name"] = i.getCSIInputVolumeName(job)
	return labels, nil
}

func (i *Internal) getCSIOutputVolumeLabels(ctx context.Context, job *model.Job) (map[string]string, error) {
	labels, err := i.labelsFromJob(ctx, job)
	if err != nil {
		return nil, err
	}

	labels["volume-name"] = i.getCSIOutputVolumeName(job)
	return labels, nil
}

func (i *Internal) getCSIHomeVolumeLabels(ctx context.Context, job *model.Job) (map[string]string, error) {
	labels, err := i.labelsFromJob(ctx, job)
	if err != nil {
		return nil, err
	}

	labels["volume-name"] = i.getCSIHomeVolumeName(job)
	return labels, nil
}

func (i *Internal) getCSISharedVolumeLabels(ctx context.Context, job *model.Job) (map[string]string, error) {
	labels, err := i.labelsFromJob(ctx, job)
	if err != nil {
		return nil, err
	}

	labels["volume-name"] = i.getCSISharedVolumeName(job)
	return labels, nil
}

func (i *Internal) getCSIPersistentVolume(volumeName string, volumeHandle string, pathMappings []IRODSFSPathMapping, labels map[string]string, clientUser string, uid int, gid int, overlayfs bool) (*apiv1.PersistentVolume, error) {
	// convert path mappings into json
	pathMappingsJSONBytes, err := json.Marshal(pathMappings)
	if err != nil {
		return nil, err
	}

	volmode := apiv1.PersistentVolumeFilesystem

	overlayfsString := "false"
	if overlayfs {
		overlayfsString = "true"
	}

	volume := &apiv1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:   volumeName,
			Labels: labels,
		},
		Spec: apiv1.PersistentVolumeSpec{
			Capacity: apiv1.ResourceList{
				apiv1.ResourceStorage: defaultStorageCapacity,
			},
			VolumeMode: &volmode,
			AccessModes: []apiv1.PersistentVolumeAccessMode{
				apiv1.ReadWriteMany,
			},
			PersistentVolumeReclaimPolicy: apiv1.PersistentVolumeReclaimRetain,
			StorageClassName:              csiDriverStorageClassName,
			PersistentVolumeSource: apiv1.PersistentVolumeSource{
				CSI: &apiv1.CSIPersistentVolumeSource{
					Driver:       csiDriverName,
					VolumeHandle: volumeHandle,
					VolumeAttributes: map[string]string{
						"client":              "irodsfuse",
						"path_mapping_json":   string(pathMappingsJSONBytes),
						"no_permission_check": "true",
						// use proxy access
						"clientUser": clientUser,
						"uid":        fmt.Sprintf("%d", uid),
						"gid":        fmt.Sprintf("%d", gid),
						"overlayfs":  overlayfsString,
					},
				},
			},
		},
	}

	return volume, nil
}

func (i *Internal) getCSIPersistentVolumeClaim(volumeName string, volumeClaimName string, labels map[string]string) (*apiv1.PersistentVolumeClaim, error) {
	storageclassname := csiDriverStorageClassName

	volumeClaim := &apiv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:   volumeClaimName,
			Labels: labels,
		},
		Spec: apiv1.PersistentVolumeClaimSpec{
			AccessModes: []apiv1.PersistentVolumeAccessMode{
				apiv1.ReadWriteMany,
			},
			StorageClassName: &storageclassname,
			VolumeName:       volumeName,
			Resources: apiv1.ResourceRequirements{
				Requests: apiv1.ResourceList{
					apiv1.ResourceStorage: defaultStorageCapacity,
				},
			},
		},
	}

	return volumeClaim, nil
}

func (i *Internal) getInputPersistentVolume(ctx context.Context, job *model.Job) (*apiv1.PersistentVolume, error) {
	volumeName := i.getCSIInputVolumeName(job)
	volumeHandle := i.getCSIInputVolumeHandle(job)

	pathMappings, err := i.getInputPathMappings(job)
	if err != nil {
		return nil, err
	}

	labels, err := i.getCSIInputVolumeLabels(ctx, job)
	if err != nil {
		return nil, err
	}

	volume, err := i.getCSIPersistentVolume(volumeName, volumeHandle, pathMappings, labels, job.Submitter, job.Steps[0].Component.Container.UID, job.Steps[0].Component.Container.UID, false)
	if err != nil {
		return nil, err
	}

	return volume, nil
}

func (i *Internal) getInputPersistentVolumeClaim(ctx context.Context, job *model.Job) (*apiv1.PersistentVolumeClaim, error) {
	volumeName := i.getCSIInputVolumeName(job)
	volumeClaimName := i.getCSIInputVolumeClaimName(job)

	labels, err := i.getCSIInputVolumeLabels(ctx, job)
	if err != nil {
		return nil, err
	}

	claim, err := i.getCSIPersistentVolumeClaim(volumeName, volumeClaimName, labels)
	if err != nil {
		return nil, err
	}

	return claim, nil
}

func (i *Internal) getOutputPersistentVolume(ctx context.Context, job *model.Job) (*apiv1.PersistentVolume, error) {
	volumeName := i.getCSIOutputVolumeName(job)
	volumeHandle := i.getCSIOutputVolumeHandle(job)

	pathMappings := []IRODSFSPathMapping{}

	pathMapping := i.getOutputPathMapping(job)
	pathMappings = append(pathMappings, pathMapping)

	labels, err := i.getCSIOutputVolumeLabels(ctx, job)
	if err != nil {
		return nil, err
	}

	volume, err := i.getCSIPersistentVolume(volumeName, volumeHandle, pathMappings, labels, job.Submitter, job.Steps[0].Component.Container.UID, job.Steps[0].Component.Container.UID, true)
	if err != nil {
		return nil, err
	}

	return volume, nil
}

func (i *Internal) getOutputPersistentVolumeClaim(ctx context.Context, job *model.Job) (*apiv1.PersistentVolumeClaim, error) {
	volumeName := i.getCSIOutputVolumeName(job)
	volumeClaimName := i.getCSIOutputVolumeClaimName(job)

	labels, err := i.getCSIOutputVolumeLabels(ctx, job)
	if err != nil {
		return nil, err
	}

	claim, err := i.getCSIPersistentVolumeClaim(volumeName, volumeClaimName, labels)
	if err != nil {
		return nil, err
	}

	return claim, nil
}

func (i *Internal) getHomePersistentVolume(ctx context.Context, job *model.Job) (*apiv1.PersistentVolume, error) {
	volumeName := i.getCSIHomeVolumeName(job)
	volumeHandle := i.getCSIHomeVolumeHandle(job)

	pathMappings := []IRODSFSPathMapping{}

	pathMapping := i.getHomePathMapping(job)
	pathMappings = append(pathMappings, pathMapping)

	labels, err := i.getCSIHomeVolumeLabels(ctx, job)
	if err != nil {
		return nil, err
	}

	volume, err := i.getCSIPersistentVolume(volumeName, volumeHandle, pathMappings, labels, job.Submitter, job.Steps[0].Component.Container.UID, job.Steps[0].Component.Container.UID, true)
	if err != nil {
		return nil, err
	}

	return volume, nil
}

func (i *Internal) getHomePersistentVolumeClaim(ctx context.Context, job *model.Job) (*apiv1.PersistentVolumeClaim, error) {
	volumeName := i.getCSIHomeVolumeName(job)
	volumeClaimName := i.getCSIHomeVolumeClaimName(job)

	labels, err := i.getCSIHomeVolumeLabels(ctx, job)
	if err != nil {
		return nil, err
	}

	claim, err := i.getCSIPersistentVolumeClaim(volumeName, volumeClaimName, labels)
	if err != nil {
		return nil, err
	}

	return claim, nil
}

func (i *Internal) getSharedPersistentVolume(ctx context.Context, job *model.Job) (*apiv1.PersistentVolume, error) {
	volumeName := i.getCSISharedVolumeName(job)
	volumeHandle := i.getCSISharedVolumeHandle(job)

	pathMappings := []IRODSFSPathMapping{}

	pathMapping := i.getSharedPathMapping(job)
	pathMappings = append(pathMappings, pathMapping)

	labels, err := i.getCSISharedVolumeLabels(ctx, job)
	if err != nil {
		return nil, err
	}

	volume, err := i.getCSIPersistentVolume(volumeName, volumeHandle, pathMappings, labels, job.Submitter, job.Steps[0].Component.Container.UID, job.Steps[0].Component.Container.UID, false)
	if err != nil {
		return nil, err
	}

	return volume, nil
}

func (i *Internal) getSharedPersistentVolumeClaim(ctx context.Context, job *model.Job) (*apiv1.PersistentVolumeClaim, error) {
	volumeName := i.getCSISharedVolumeName(job)
	volumeClaimName := i.getCSISharedVolumeClaimName(job)

	labels, err := i.getCSISharedVolumeLabels(ctx, job)
	if err != nil {
		return nil, err
	}

	claim, err := i.getCSIPersistentVolumeClaim(volumeName, volumeClaimName, labels)
	if err != nil {
		return nil, err
	}

	return claim, nil
}

// getPersistentVolumes returns the PersistentVolumes for the VICE analysis. It does
// not call the k8s API.
func (i *Internal) getPersistentVolumes(ctx context.Context, job *model.Job) ([]*apiv1.PersistentVolume, error) {
	if i.UseCSIDriver {
		persistentVolumes := []*apiv1.PersistentVolume{}

		// input volume
		inputVolume, err := i.getInputPersistentVolume(ctx, job)
		if err != nil {
			return nil, err
		}

		persistentVolumes = append(persistentVolumes, inputVolume)

		// output volume
		outputVolume, err := i.getOutputPersistentVolume(ctx, job)
		if err != nil {
			return nil, err
		}

		persistentVolumes = append(persistentVolumes, outputVolume)

		// home path
		if job.UserHome != "" {
			homeVolume, err := i.getHomePersistentVolume(ctx, job)
			if err != nil {
				return nil, err
			}

			persistentVolumes = append(persistentVolumes, homeVolume)
		}

		// shared path
		sharedVolume, err := i.getSharedPersistentVolume(ctx, job)
		if err != nil {
			return nil, err
		}

		persistentVolumes = append(persistentVolumes, sharedVolume)
		return persistentVolumes, nil
	}

	return nil, nil
}

// getPersistentVolumeClaims returns the PersistentVolumes for the VICE analysis. It does
// not call the k8s API.
func (i *Internal) getPersistentVolumeClaims(ctx context.Context, job *model.Job) ([]*apiv1.PersistentVolumeClaim, error) {
	if i.UseCSIDriver {
		persistentVolumeClaims := []*apiv1.PersistentVolumeClaim{}

		// input volume
		inputVolumeClaim, err := i.getInputPersistentVolumeClaim(ctx, job)
		if err != nil {
			return nil, err
		}

		persistentVolumeClaims = append(persistentVolumeClaims, inputVolumeClaim)

		// output volume
		outputVolumeClaim, err := i.getOutputPersistentVolumeClaim(ctx, job)
		if err != nil {
			return nil, err
		}

		persistentVolumeClaims = append(persistentVolumeClaims, outputVolumeClaim)

		// home path
		if job.UserHome != "" {
			homeVolumeClaim, err := i.getHomePersistentVolumeClaim(ctx, job)
			if err != nil {
				return nil, err
			}

			persistentVolumeClaims = append(persistentVolumeClaims, homeVolumeClaim)
		}

		// shared path
		sharedVolumeClaim, err := i.getSharedPersistentVolumeClaim(ctx, job)
		if err != nil {
			return nil, err
		}

		persistentVolumeClaims = append(persistentVolumeClaims, sharedVolumeClaim)

		return persistentVolumeClaims, nil
	}

	return nil, nil
}

// getPersistentVolumeSources returns the volumes for the VICE analysis. It does
// not call the k8s API.
func (i *Internal) getPersistentVolumeSources(job *model.Job) ([]*apiv1.Volume, error) {
	if i.UseCSIDriver {
		volumes := []*apiv1.Volume{}

		inputVolume := &apiv1.Volume{
			Name: i.getCSIInputVolumeClaimName(job),
			VolumeSource: apiv1.VolumeSource{
				PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
					ClaimName: i.getCSIInputVolumeClaimName(job),
				},
			},
		}
		volumes = append(volumes, inputVolume)

		outputVolume := &apiv1.Volume{
			Name: i.getCSIOutputVolumeClaimName(job),
			VolumeSource: apiv1.VolumeSource{
				PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
					ClaimName: i.getCSIOutputVolumeClaimName(job),
				},
			},
		}
		volumes = append(volumes, outputVolume)

		if job.UserHome != "" {
			homeVolume := &apiv1.Volume{
				Name: i.getCSIHomeVolumeClaimName(job),
				VolumeSource: apiv1.VolumeSource{
					PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
						ClaimName: i.getCSIHomeVolumeClaimName(job),
					},
				},
			}
			volumes = append(volumes, homeVolume)
		}

		sharedVolume := &apiv1.Volume{
			Name: i.getCSISharedVolumeClaimName(job),
			VolumeSource: apiv1.VolumeSource{
				PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
					ClaimName: i.getCSISharedVolumeClaimName(job),
				},
			},
		}
		volumes = append(volumes, sharedVolume)

		return volumes, nil
	}

	return nil, nil
}

// getPersistentVolumeMounts returns the volume mount for the VICE analysis. It does
// not call the k8s API.
func (i *Internal) getPersistentVolumeMounts(job *model.Job) []*apiv1.VolumeMount {
	if i.UseCSIDriver {
		volumeMounts := []*apiv1.VolumeMount{}

		inputVolumeMount := &apiv1.VolumeMount{
			Name:      i.getCSIInputVolumeClaimName(job),
			MountPath: path.Join(csiDriverLocalMountPath, "input"),
		}
		volumeMounts = append(volumeMounts, inputVolumeMount)

		outputVolumeMount := &apiv1.VolumeMount{
			Name:      i.getCSIOutputVolumeClaimName(job),
			MountPath: path.Join(csiDriverLocalMountPath, "output"),
		}
		volumeMounts = append(volumeMounts, outputVolumeMount)

		if job.UserHome != "" {
			homeVolumeMount := &apiv1.VolumeMount{
				Name:      i.getCSIHomeVolumeClaimName(job),
				MountPath: path.Join(csiDriverLocalMountPath, job.UserHome),
			}
			volumeMounts = append(volumeMounts, homeVolumeMount)
		}

		sharedVolumeMount := &apiv1.VolumeMount{
			Name:      i.getCSISharedVolumeClaimName(job),
			MountPath: path.Join(csiDriverLocalMountPath, "home", "shared"),
		}
		volumeMounts = append(volumeMounts, sharedVolumeMount)
		return volumeMounts
	}

	return nil
}
