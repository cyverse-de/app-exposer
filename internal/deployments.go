package internal

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/cyverse-de/model"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// One gibibyte.
//const gibibyte = 1024 * 1024 * 1024

// analysisPorts returns a list of container ports needed by the VICE analysis.
func analysisPorts(step *model.Step) []apiv1.ContainerPort {
	ports := []apiv1.ContainerPort{}

	for i, p := range step.Component.Container.Ports {
		ports = append(ports, apiv1.ContainerPort{
			ContainerPort: int32(p.ContainerPort),
			Name:          fmt.Sprintf("tcp-a-%d", i),
			Protocol:      apiv1.ProtocolTCP,
		})
	}

	return ports
}

// deploymentVolumes returns the Volume objects needed for the VICE analyis
// Deployment. This does NOT call the k8s API to actually create the Volumes,
// it returns the objects that can be included in the Deployment object that
// will get passed to the k8s API later. Also not that these are the Volumes,
// not the container-specific VolumeMounts.
func (i *Internal) deploymentVolumes(job *model.Job) []apiv1.Volume {
	output := []apiv1.Volume{}

	if len(job.FilterInputsWithoutTickets()) > 0 {
		output = append(output, apiv1.Volume{
			Name: inputPathListVolumeName,
			VolumeSource: apiv1.VolumeSource{
				ConfigMap: &apiv1.ConfigMapVolumeSource{
					LocalObjectReference: apiv1.LocalObjectReference{
						Name: inputPathListConfigMapName(job),
					},
				},
			},
		})
	}

	if i.UseCSIDriver {
		volumeSources, err := i.getPersistentVolumeSources(job)
		if err != nil {
			log.Warn(err)
		} else {
			for _, volumeSource := range volumeSources {
				output = append(output, *volumeSource)
			}
		}
	} else {
		output = append(output,
			apiv1.Volume{
				Name: fileTransfersVolumeName,
				VolumeSource: apiv1.VolumeSource{
					EmptyDir: &apiv1.EmptyDirVolumeSource{},
				},
			},
			apiv1.Volume{
				Name: porklockConfigVolumeName,
				VolumeSource: apiv1.VolumeSource{
					Secret: &apiv1.SecretVolumeSource{
						SecretName: porklockConfigSecretName,
					},
				},
			},
		)
	}

	output = append(output,
		apiv1.Volume{
			Name: excludesVolumeName,
			VolumeSource: apiv1.VolumeSource{
				ConfigMap: &apiv1.ConfigMapVolumeSource{
					LocalObjectReference: apiv1.LocalObjectReference{
						Name: excludesConfigMapName(job),
					},
				},
			},
		},
	)

	return output
}

func (i *Internal) getFrontendURL(job *model.Job) *url.URL {
	// This should be parsed in main(), so we shouldn't worry about it here.
	frontURL, _ := url.Parse(i.FrontendBaseURL)
	frontURL.Host = fmt.Sprintf("%s.%s", IngressName(job.UserID, job.InvocationID), frontURL.Host)
	return frontURL
}

func (i *Internal) viceProxyCommand(job *model.Job) []string {
	frontURL := i.getFrontendURL(job)
	backendURL := fmt.Sprintf("http://localhost:%s", strconv.Itoa(job.Steps[0].Component.Container.Ports[0].ContainerPort))

	// websocketURL := fmt.Sprintf("ws://localhost:%s", strconv.Itoa(job.Steps[0].Component.Container.Ports[0].ContainerPort))

	output := []string{
		"vice-proxy",
		"--listen-addr", fmt.Sprintf("0.0.0.0:%d", viceProxyPort),
		"--backend-url", backendURL,
		"--ws-backend-url", backendURL,
		"--cas-base-url", i.CASBaseURL,
		"--cas-validate", "validate",
		"--frontend-url", frontURL.String(),
		"--external-id", job.InvocationID,
		"--get-analysis-id-base", fmt.Sprintf("http://%s.%s", i.GetAnalysisIDService, i.VICEBackendNamespace),
		"--check-resource-access-base", fmt.Sprintf("http://%s.%s", i.CheckResourceAccessService, i.VICEBackendNamespace),
		"--keycloak-base-url", i.KeycloakBaseURL,
		"--keycloak-realm", i.KeycloakRealm,
		"--keycloak-client-id", i.KeycloakClientID,
		"--keycloak-client-secret", i.KeycloakClientSecret,
	}

	return output
}

var (
	defaultCPUResourceRequest, _ = resourcev1.ParseQuantity("1000m")
	defaultMemResourceRequest, _ = resourcev1.ParseQuantity("2Gi")
	defaultStorageRequest, _     = resourcev1.ParseQuantity("16Gi")
	defaultCPUResourceLimit, _   = resourcev1.ParseQuantity("4000m")
	defaultMemResourceLimit, _   = resourcev1.ParseQuantity("8Gi")
)

func cpuResourceRequest(job *model.Job) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = defaultCPUResourceRequest

	if job.Steps[0].Component.Container.MinCPUCores != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%fm", job.Steps[0].Component.Container.MinCPUCores*1000))
		if err != nil {
			log.Warn(err)
			value = defaultCPUResourceRequest
		}
	}

	return value
}

func cpuResourceLimit(job *model.Job) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = defaultCPUResourceLimit

	if job.Steps[0].Component.Container.MaxCPUCores != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%fm", job.Steps[0].Component.Container.MaxCPUCores*1000))
		if err != nil {
			log.Warn(err)
			value = defaultCPUResourceLimit
		}
	}
	return value
}

func memResourceRequest(job *model.Job) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = defaultMemResourceRequest

	if job.Steps[0].Component.Container.MinMemoryLimit != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%d", job.Steps[0].Component.Container.MinMemoryLimit))
		if err != nil {
			log.Warn(err)
			value = defaultMemResourceRequest
		}
	}
	return value
}

func memResourceLimit(job *model.Job) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = defaultMemResourceLimit

	if job.Steps[0].Component.Container.MemoryLimit != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%d", job.Steps[0].Component.Container.MemoryLimit))
		if err != nil {
			log.Warn(err)
			value = defaultMemResourceLimit
		}
	}
	return value
}

func storageRequest(job *model.Job) resourcev1.Quantity {
	var (
		value resourcev1.Quantity
		err   error
	)

	value = defaultStorageRequest

	if job.Steps[0].Component.Container.MinDiskSpace != 0 {
		value, err = resourcev1.ParseQuantity(fmt.Sprintf("%d", job.Steps[0].Component.Container.MinDiskSpace))
		if err != nil {
			log.Warn(err)
			value = defaultStorageRequest
		}
	}
	return value
}

// initContainers returns a []apiv1.Container used for the InitContainers in
// the VICE app Deployment resource.
func (i *Internal) initContainers(job *model.Job) []apiv1.Container {
	output := []apiv1.Container{}

	if !i.UseCSIDriver {
		output = append(output, apiv1.Container{
			Name:            fileTransfersInitContainerName,
			Image:           fmt.Sprintf("%s:%s", i.PorklockImage, i.PorklockTag),
			Command:         append(fileTransferCommand(job), "--no-service"),
			ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
			WorkingDir:      inputPathListMountPath,
			VolumeMounts:    i.fileTransfersVolumeMounts(job),
			Ports: []apiv1.ContainerPort{
				{
					Name:          fileTransfersPortName,
					ContainerPort: fileTransfersPort,
					Protocol:      apiv1.Protocol("TCP"),
				},
			},
			SecurityContext: &apiv1.SecurityContext{
				RunAsUser:  int64Ptr(int64(job.Steps[0].Component.Container.UID)),
				RunAsGroup: int64Ptr(int64(job.Steps[0].Component.Container.UID)),
				Capabilities: &apiv1.Capabilities{
					Drop: []apiv1.Capability{
						"SETPCAP",
						"AUDIT_WRITE",
						"KILL",
						"SETGID",
						"SETUID",
						"NET_BIND_SERVICE",
						"SYS_CHROOT",
						"SETFCAP",
						"FSETID",
						"NET_RAW",
						"MKNOD",
					},
				},
			},
		})
	}

	return output
}

func gpuEnabled(job *model.Job) bool {
	gpuEnabled := false
	for _, device := range job.Steps[0].Component.Container.Devices {
		if strings.HasPrefix(strings.ToLower(device.HostPath), "/dev/nvidia") {
			gpuEnabled = true
		}
	}
	return gpuEnabled
}

func (i *Internal) defineAnalysisContainer(job *model.Job) apiv1.Container {
	analysisEnvironment := []apiv1.EnvVar{}
	for envKey, envVal := range job.Steps[0].Environment {
		analysisEnvironment = append(
			analysisEnvironment,
			apiv1.EnvVar{
				Name:  envKey,
				Value: envVal,
			},
		)
	}

	analysisEnvironment = append(
		analysisEnvironment,
		apiv1.EnvVar{
			Name:  "REDIRECT_URL",
			Value: i.getFrontendURL(job).String(),
		},
		apiv1.EnvVar{
			Name:  "IPLANT_USER",
			Value: job.Submitter,
		},
		apiv1.EnvVar{
			Name:  "IPLANT_EXECUTION_ID",
			Value: job.InvocationID,
		},
	)

	cpuRequest := cpuResourceRequest(job)
	memRequest := memResourceRequest(job)
	storageRequest := storageRequest(job)

	requests := apiv1.ResourceList{
		apiv1.ResourceCPU:              cpuRequest,     // job contains # cores
		apiv1.ResourceMemory:           memRequest,     // job contains # bytes mem
		apiv1.ResourceEphemeralStorage: storageRequest, // job contains # bytes storage
	}

	cpuLimit := cpuResourceLimit(job)
	memLimit := memResourceLimit(job)

	limits := apiv1.ResourceList{
		apiv1.ResourceCPU:    cpuLimit, //job contains # cores
		apiv1.ResourceMemory: memLimit, // job contains # bytes mem
	}

	// If a GPU device is configured, then add it to the resource limits.
	if gpuEnabled(job) {
		gpuLimit, err := resourcev1.ParseQuantity("1")
		if err != nil {
			log.Warn(err)
		} else {
			limits[apiv1.ResourceName("nvidia.com/gpu")] = gpuLimit
		}
	}

	volumeMounts := []apiv1.VolumeMount{}
	if i.UseCSIDriver {
		persistentVolumeMounts, err := i.getPersistentVolumeMounts(job)
		if err != nil {
			log.Warn(err)
		} else {
			for _, persistentVolumeMount := range persistentVolumeMounts {
				volumeMounts = append(volumeMounts, *persistentVolumeMount)
			}
		}
	} else {
		volumeMounts = append(volumeMounts, apiv1.VolumeMount{
			Name:      fileTransfersVolumeName,
			MountPath: fileTransfersMountPath(job),
			ReadOnly:  false,
		})
	}

	analysisContainer := apiv1.Container{
		Name: analysisContainerName,
		Image: fmt.Sprintf(
			"%s:%s",
			job.Steps[0].Component.Container.Image.Name,
			job.Steps[0].Component.Container.Image.Tag,
		),
		ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
		Env:             analysisEnvironment,
		Resources: apiv1.ResourceRequirements{
			Limits:   limits,
			Requests: requests,
		},
		VolumeMounts: volumeMounts,
		Ports:        analysisPorts(&job.Steps[0]),
		SecurityContext: &apiv1.SecurityContext{
			RunAsUser:  int64Ptr(int64(job.Steps[0].Component.Container.UID)),
			RunAsGroup: int64Ptr(int64(job.Steps[0].Component.Container.UID)),
			// Capabilities: &apiv1.Capabilities{
			// 	Drop: []apiv1.Capability{
			// 		"SETPCAP",
			// 		"AUDIT_WRITE",
			// 		"KILL",
			// 		//"SETGID",
			// 		//"SETUID",
			// 		"SYS_CHROOT",
			// 		"SETFCAP",
			// 		"FSETID",
			// 		//"MKNOD",
			// 	},
			// },
		},
		ReadinessProbe: &apiv1.Probe{
			InitialDelaySeconds: 0,
			TimeoutSeconds:      30,
			SuccessThreshold:    1,
			FailureThreshold:    10,
			PeriodSeconds:       31,
			Handler: apiv1.Handler{
				HTTPGet: &apiv1.HTTPGetAction{
					Port:   intstr.FromInt(job.Steps[0].Component.Container.Ports[0].ContainerPort),
					Scheme: apiv1.URISchemeHTTP,
					Path:   "/",
				},
			},
		},
	}

	if job.Steps[0].Component.Container.EntryPoint != "" {
		analysisContainer.Command = []string{
			job.Steps[0].Component.Container.EntryPoint,
		}
	}

	// Default to the container working directory if it isn't set.
	if job.Steps[0].Component.Container.WorkingDir != "" {
		analysisContainer.WorkingDir = job.Steps[0].Component.Container.WorkingDir
	}

	if len(job.Steps[0].Arguments()) != 0 {
		analysisContainer.Args = append(analysisContainer.Args, job.Steps[0].Arguments()...)
	}

	return analysisContainer

}

// deploymentContainers returns the Containers needed for the VICE analysis
// Deployment. It does not call the k8s API.
func (i *Internal) deploymentContainers(job *model.Job) []apiv1.Container {
	output := []apiv1.Container{}

	output = append(output, apiv1.Container{
		Name:            viceProxyContainerName,
		Image:           i.ViceProxyImage,
		Command:         i.viceProxyCommand(job),
		ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
		Ports: []apiv1.ContainerPort{
			{
				Name:          viceProxyPortName,
				ContainerPort: viceProxyPort,
				Protocol:      apiv1.Protocol("TCP"),
			},
		},
		SecurityContext: &apiv1.SecurityContext{
			RunAsUser:  int64Ptr(int64(job.Steps[0].Component.Container.UID)),
			RunAsGroup: int64Ptr(int64(job.Steps[0].Component.Container.UID)),
			Capabilities: &apiv1.Capabilities{
				Drop: []apiv1.Capability{
					"SETPCAP",
					"AUDIT_WRITE",
					"KILL",
					"SETGID",
					"SETUID",
					"SYS_CHROOT",
					"SETFCAP",
					"FSETID",
					"NET_RAW",
					"MKNOD",
				},
			},
		},
		ReadinessProbe: &apiv1.Probe{
			Handler: apiv1.Handler{
				HTTPGet: &apiv1.HTTPGetAction{
					Port:   intstr.FromInt(int(viceProxyPort)),
					Scheme: apiv1.URISchemeHTTP,
					Path:   "/",
				},
			},
		},
	})

	if !i.UseCSIDriver {
		output = append(output, apiv1.Container{
			Name:            fileTransfersContainerName,
			Image:           fmt.Sprintf("%s:%s", i.PorklockImage, i.PorklockTag),
			Command:         fileTransferCommand(job),
			ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
			WorkingDir:      inputPathListMountPath,
			VolumeMounts:    i.fileTransfersVolumeMounts(job),
			Ports: []apiv1.ContainerPort{
				{
					Name:          fileTransfersPortName,
					ContainerPort: fileTransfersPort,
					Protocol:      apiv1.Protocol("TCP"),
				},
			},
			SecurityContext: &apiv1.SecurityContext{
				RunAsUser:  int64Ptr(int64(job.Steps[0].Component.Container.UID)),
				RunAsGroup: int64Ptr(int64(job.Steps[0].Component.Container.UID)),
				Capabilities: &apiv1.Capabilities{
					Drop: []apiv1.Capability{
						"SETPCAP",
						"AUDIT_WRITE",
						"KILL",
						"SETGID",
						"SETUID",
						"NET_BIND_SERVICE",
						"SYS_CHROOT",
						"SETFCAP",
						"FSETID",
						"NET_RAW",
						"MKNOD",
					},
				},
			},
			ReadinessProbe: &apiv1.Probe{
				Handler: apiv1.Handler{
					HTTPGet: &apiv1.HTTPGetAction{
						Port:   intstr.FromInt(int(fileTransfersPort)),
						Scheme: apiv1.URISchemeHTTP,
						Path:   "/",
					},
				},
			},
		})
	}

	output = append(output, i.defineAnalysisContainer(job))
	return output
}

// imagePullSecrets creates an array of LocalObjectReference that refer to any
// configured secrets to use for pulling images This is passed the job because
// it may be advantageous, in the future, to add secrets depending on the
// images actually needed by the job, but at present this uses a static value
func (i *Internal) imagePullSecrets(_ *model.Job) []apiv1.LocalObjectReference {
	if i.ImagePullSecretName != "" {
		return []apiv1.LocalObjectReference{
			{Name: i.ImagePullSecretName},
		}
	}
	return []apiv1.LocalObjectReference{}
}

// getDeployment assembles and returns the Deployment for the VICE analysis. It does
// not call the k8s API.
func (i *Internal) getDeployment(job *model.Job) (*appsv1.Deployment, error) {
	labels, err := i.labelsFromJob(job)
	if err != nil {
		return nil, err
	}

	autoMount := false

	tolerations := []apiv1.Toleration{
		{
			Key:      viceTolerationKey,
			Operator: apiv1.TolerationOperator(viceTolerationOperator),
			Value:    viceTolerationValue,
			Effect:   apiv1.TaintEffect(viceTolerationEffect),
		},
	}

	nodeSelectorRequirements := []apiv1.NodeSelectorRequirement{
		{
			Key:      viceAffinityKey,
			Operator: apiv1.NodeSelectorOperator(viceAffinityOperator),
			Values: []string{
				viceAffinityValue,
			},
		},
	}

	if gpuEnabled(job) {
		tolerations = append(tolerations, apiv1.Toleration{
			Key:      gpuTolerationKey,
			Operator: apiv1.TolerationOperator(gpuTolerationOperator),
			Value:    gpuTolerationValue,
			Effect:   apiv1.TaintEffect(gpuTolerationEffect),
		})

		nodeSelectorRequirements = append(nodeSelectorRequirements, apiv1.NodeSelectorRequirement{
			Key:      gpuAffinityKey,
			Operator: apiv1.NodeSelectorOperator(gpuAffinityOperator),
			Values: []string{
				gpuAffinityValue,
			},
		})
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:   job.InvocationID,
			Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"external-id": job.InvocationID,
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: apiv1.PodSpec{
					Hostname:                     IngressName(job.UserID, job.InvocationID),
					RestartPolicy:                apiv1.RestartPolicy("Always"),
					Volumes:                      i.deploymentVolumes(job),
					InitContainers:               i.initContainers(job),
					Containers:                   i.deploymentContainers(job),
					ImagePullSecrets:             i.imagePullSecrets(job),
					AutomountServiceAccountToken: &autoMount,
					SecurityContext: &apiv1.PodSecurityContext{
						RunAsUser:  int64Ptr(int64(job.Steps[0].Component.Container.UID)),
						RunAsGroup: int64Ptr(int64(job.Steps[0].Component.Container.UID)),
						FSGroup:    int64Ptr(int64(job.Steps[0].Component.Container.UID)),
					},
					Tolerations: tolerations,
					Affinity: &apiv1.Affinity{
						NodeAffinity: &apiv1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &apiv1.NodeSelector{
								NodeSelectorTerms: []apiv1.NodeSelectorTerm{
									{
										MatchExpressions: nodeSelectorRequirements,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return deployment, nil
}
