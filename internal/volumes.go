package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cyverse-de/model"
	apiv1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	defaultStorageCapacity, _ = resourcev1.ParseQuantity("5Gi")
)

// IRODSFSPathMapping defines a single path mapping that can be used by the iRODS CSI driver to create a mount point.
type IRODSFSPathMapping struct {
	IRODSPath      string `yaml:"irods_path" json:"irods_path"`
	MappingPath    string `yaml:"mapping_path" json:"mapping_path"`
	ResourceType   string `yaml:"resource_type" json:"resource_type"` // file or dir
	ReadOnly       bool   `yaml:"read_only" json:"read_only"`
	CreateDir      bool   `yaml:"create_dir" json:"create_dir"`
	IgnoreNotExist bool   `yaml:"ignore_not_exist" json:"ignore_not_exist"`
}

func (i *Internal) getCSIInputOutputVolumeHandle(job *model.Job) string {
	return fmt.Sprintf("%s-handle-%s", csiDriverInputOutputVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIHomeVolumeHandle(job *model.Job) string {
	return fmt.Sprintf("%s-handle-%s", csiDriverHomeVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIInputOutputVolumeName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverInputOutputVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIHomeVolumeName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverHomeVolumeNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIInputOutputVolumeClaimName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverInputOutputVolumeClaimNamePrefix, job.InvocationID)
}

func (i *Internal) getCSIHomeVolumeClaimName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", csiDriverHomeVolumeClaimNamePrefix, job.InvocationID)
}

func (i *Internal) getInputPathMappings(job *model.Job) ([]IRODSFSPathMapping, error) {
	mappings := []IRODSFSPathMapping{}
	// mark if the mapping path is already occupied
	// key = mount path, val = irods path
	mappingMap := map[string]string{}

	// Mount the input and output files.
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

				mountPath := fmt.Sprintf("%s/%s", csiDriverInputVolumeMountPath, filepath.Base(irodsPath))
				// check if mountPath is already used by other input
				if existingIRODSPath, ok := mappingMap[mountPath]; ok {
					// exists - error
					return nil, fmt.Errorf("tried to mount an input file %s at %s already used by - %s", irodsPath, mountPath, existingIRODSPath)
				}
				mappingMap[mountPath] = irodsPath

				mapping := IRODSFSPathMapping{
					IRODSPath:      irodsPath,
					MappingPath:    mountPath,
					ResourceType:   resourceType,
					ReadOnly:       true,
					CreateDir:      false,
					IgnoreNotExist: true,
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
		IRODSPath:      job.OutputDirectory(),
		MappingPath:    csiDriverOutputVolumeMountPath,
		ResourceType:   "dir",
		ReadOnly:       false,
		CreateDir:      true,
		IgnoreNotExist: false,
	}
}

func (i *Internal) getHomePathMapping(job *model.Job) IRODSFSPathMapping {
	// mount a single collection for home
	userHome := strings.TrimPrefix(job.UserHome, fmt.Sprintf("/%s", i.IRODSZone))
	userHome = strings.TrimSuffix(userHome, "/")

	return IRODSFSPathMapping{
		IRODSPath:      job.UserHome,
		MappingPath:    userHome,
		ResourceType:   "dir",
		ReadOnly:       false,
		CreateDir:      false,
		IgnoreNotExist: false,
	}
}

func (i *Internal) getSharedPathMapping(job *model.Job) IRODSFSPathMapping {
	// mount a single collection for shared data
	sharedHomeFullPath := fmt.Sprintf("/%s/home/shared", i.IRODSZone)
	sharedHome := "/home/shared"

	return IRODSFSPathMapping{
		IRODSPath:      sharedHomeFullPath,
		MappingPath:    sharedHome,
		ResourceType:   "dir",
		ReadOnly:       false,
		CreateDir:      false,
		IgnoreNotExist: true,
	}
}

func (i *Internal) getCSIInputOutputVolumeLabels(ctx context.Context, job *model.Job) (map[string]string, error) {
	labels, err := i.labelsFromJob(ctx, job)
	if err != nil {
		return nil, err
	}

	labels["volume-name"] = i.getCSIInputOutputVolumeClaimName(job)
	return labels, nil
}

func (i *Internal) getCSIHomeVolumeLabels(ctx context.Context, job *model.Job) (map[string]string, error) {
	labels, err := i.labelsFromJob(ctx, job)
	if err != nil {
		return nil, err
	}

	labels["volume-name"] = i.getCSIHomeVolumeClaimName(job)
	return labels, nil
}

// getPersistentVolumes returns the PersistentVolumes for the VICE analysis. It does
// not call the k8s API.
func (i *Internal) getPersistentVolumes(ctx context.Context, job *model.Job) ([]*apiv1.PersistentVolume, error) {
	if i.UseCSIDriver {
		// input output path
		ioPathMappings := []IRODSFSPathMapping{}

		inputPathMappings, err := i.getInputPathMappings(job)
		if err != nil {
			return nil, err
		}
		ioPathMappings = append(ioPathMappings, inputPathMappings...)

		outputPathMapping := i.getOutputPathMapping(job)
		ioPathMappings = append(ioPathMappings, outputPathMapping)

		// convert pathMappings into json
		ioPathMappingsJSONBytes, err := json.Marshal(ioPathMappings)
		if err != nil {
			return nil, err
		}

		// home path
		homePathMappings := []IRODSFSPathMapping{}
		if job.UserHome != "" {
			homePathMapping := i.getHomePathMapping(job)
			homePathMappings = append(homePathMappings, homePathMapping)
		}

		// shared path
		sharedPathMapping := i.getSharedPathMapping(job)
		homePathMappings = append(homePathMappings, sharedPathMapping)

		homePathMappingsJSONBytes, err := json.Marshal(homePathMappings)
		if err != nil {
			return nil, err
		}

		volmode := apiv1.PersistentVolumeFilesystem
		persistentVolumes := []*apiv1.PersistentVolume{}

		ioVolumeLabels, err := i.getCSIInputOutputVolumeLabels(ctx, job)
		if err != nil {
			return nil, err
		}

		ioVolume := &apiv1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:   i.getCSIInputOutputVolumeName(job),
				Labels: ioVolumeLabels,
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
						VolumeHandle: i.getCSIInputOutputVolumeHandle(job),
						VolumeAttributes: map[string]string{
							"client":            "irodsfuse",
							"path_mapping_json": string(ioPathMappingsJSONBytes),
							// use proxy access
							"clientUser": job.Submitter,
							"uid":        fmt.Sprintf("%d", job.Steps[0].Component.Container.UID),
							"gid":        fmt.Sprintf("%d", job.Steps[0].Component.Container.UID),
						},
					},
				},
			},
		}

		persistentVolumes = append(persistentVolumes, ioVolume)

		if job.UserHome != "" {
			homeVolumeLabels, err := i.getCSIHomeVolumeLabels(ctx, job)
			if err != nil {
				return nil, err
			}

			homeVolume := &apiv1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:   i.getCSIHomeVolumeName(job),
					Labels: homeVolumeLabels,
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
							VolumeHandle: i.getCSIHomeVolumeHandle(job),
							VolumeAttributes: map[string]string{
								"client":            "irodsfuse",
								"path_mapping_json": string(homePathMappingsJSONBytes),
								// use proxy access
								"clientUser": job.Submitter,
								"uid":        fmt.Sprintf("%d", job.Steps[0].Component.Container.UID),
								"gid":        fmt.Sprintf("%d", job.Steps[0].Component.Container.UID),
							},
						},
					},
				},
			}

			persistentVolumes = append(persistentVolumes, homeVolume)
		}

		return persistentVolumes, nil
	}

	return nil, nil
}

// getPersistentVolumeClaims returns the PersistentVolumes for the VICE analysis. It does
// not call the k8s API.
func (i *Internal) getPersistentVolumeClaims(ctx context.Context, job *model.Job) ([]*apiv1.PersistentVolumeClaim, error) {
	if i.UseCSIDriver {
		labels, err := i.labelsFromJob(ctx, job)
		if err != nil {
			return nil, err
		}

		storageclassname := csiDriverStorageClassName
		volumeClaims := []*apiv1.PersistentVolumeClaim{}

		ioVolumeClaim := &apiv1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:   i.getCSIInputOutputVolumeClaimName(job),
				Labels: labels,
			},
			Spec: apiv1.PersistentVolumeClaimSpec{
				AccessModes: []apiv1.PersistentVolumeAccessMode{
					apiv1.ReadWriteMany,
				},
				StorageClassName: &storageclassname,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"volume-name": i.getCSIInputOutputVolumeClaimName(job),
					},
				},
				Resources: apiv1.ResourceRequirements{
					Requests: apiv1.ResourceList{
						apiv1.ResourceStorage: defaultStorageCapacity,
					},
				},
			},
		}

		volumeClaims = append(volumeClaims, ioVolumeClaim)

		if job.UserHome != "" {
			homeVolumeClaim := &apiv1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:   i.getCSIHomeVolumeClaimName(job),
					Labels: labels,
				},
				Spec: apiv1.PersistentVolumeClaimSpec{
					AccessModes: []apiv1.PersistentVolumeAccessMode{
						apiv1.ReadWriteMany,
					},
					StorageClassName: &storageclassname,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"volume-name": i.getCSIHomeVolumeClaimName(job),
						},
					},
					Resources: apiv1.ResourceRequirements{
						Requests: apiv1.ResourceList{
							apiv1.ResourceStorage: defaultStorageCapacity,
						},
					},
				},
			}

			volumeClaims = append(volumeClaims, homeVolumeClaim)
		}

		return volumeClaims, nil
	}

	return nil, nil
}

// getPersistentVolumeSources returns the volumes for the VICE analysis. It does
// not call the k8s API.
func (i *Internal) getPersistentVolumeSources(job *model.Job) ([]*apiv1.Volume, error) {
	if i.UseCSIDriver {
		volumes := []*apiv1.Volume{}

		ioVolume := &apiv1.Volume{
			Name: i.getCSIInputOutputVolumeClaimName(job),
			VolumeSource: apiv1.VolumeSource{
				PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
					ClaimName: i.getCSIInputOutputVolumeClaimName(job),
				},
			},
		}

		volumes = append(volumes, ioVolume)

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

		return volumes, nil
	}

	return nil, nil
}

// getPersistentVolumeMounts returns the volume mount for the VICE analysis. It does
// not call the k8s API.
func (i *Internal) getPersistentVolumeMounts(job *model.Job) []*apiv1.VolumeMount {
	if i.UseCSIDriver {
		volumeMounts := []*apiv1.VolumeMount{}

		ioVolumeMount := &apiv1.VolumeMount{
			Name:      i.getCSIInputOutputVolumeClaimName(job),
			MountPath: csiDriverLocalMountPath,
		}

		volumeMounts = append(volumeMounts, ioVolumeMount)

		homeVolumeMount := &apiv1.VolumeMount{
			Name:      i.getCSIHomeVolumeClaimName(job),
			MountPath: fmt.Sprintf("/%s", i.IRODSZone),
		}
		volumeMounts = append(volumeMounts, homeVolumeMount)

		return volumeMounts
	}

	return nil
}
