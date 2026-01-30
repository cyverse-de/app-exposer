package incluster

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/resourcing"
	"github.com/cyverse-de/model/v9"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

func (i *Incluster) getFrontendURL(job *model.Job) *url.URL {
	// This should be parsed in main(), so we shouldn't worry about it here.
	frontURL, _ := url.Parse(i.FrontendBaseURL)
	frontURL.Host = fmt.Sprintf("%s.%s", IngressName(job.UserID, job.InvocationID), frontURL.Host)
	return frontURL
}

func (i *Incluster) viceProxyCommand(job *model.Job) []string {
	frontURL := i.getFrontendURL(job)
	backendURL := fmt.Sprintf("http://localhost:%s", strconv.Itoa(job.Steps[0].Component.Container.Ports[0].ContainerPort))

	// websocketURL := fmt.Sprintf("ws://localhost:%s", strconv.Itoa(job.Steps[0].Component.Container.Ports[0].ContainerPort))

	output := []string{
		"vice-proxy",
		"--listen-addr", fmt.Sprintf("0.0.0.0:%d", constants.VICEProxyPort),
		"--backend-url", backendURL,
		"--ws-backend-url", backendURL,
		"--frontend-url", frontURL.String(),
		"--analysis-id", job.ID,
		"--keycloak-base-url", i.KeycloakBaseURL,
		"--keycloak-realm", i.KeycloakRealm,
		"--keycloak-client-id", i.KeycloakClientID,
		"--keycloak-client-secret", i.KeycloakClientSecret,
	}

	// Conditionally add --disable-auth flag when authentication is disabled
	if i.DisableViceProxyAuth {
		output = append(output, "--disable-auth")
	}

	return output
}

// inputStagingContainer returns the init container to be used for staging input files. This init container
// is only used when iRODS CSI driver integration is disabled.
func (i *Incluster) inputStagingContainer(job *model.Job) apiv1.Container {
	return apiv1.Container{
		Name:            constants.FileTransfersInitContainerName,
		Image:           fmt.Sprintf("%s:%s", i.PorklockImage, i.PorklockTag),
		Command:         append(fileTransferCommand(job), "--no-service"),
		ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
		WorkingDir:      constants.InputPathListMountPath,
		VolumeMounts:    i.fileTransfersVolumeMounts(job),
		Ports: []apiv1.ContainerPort{
			{
				Name:          constants.FileTransfersPortName,
				ContainerPort: constants.FileTransfersPort,
				Protocol:      apiv1.Protocol("TCP"),
			},
		},
		SecurityContext: &apiv1.SecurityContext{
			RunAsUser:  constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
			RunAsGroup: constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
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
					"MKNOD",
				},
				Add: []apiv1.Capability{
					"NET_ADMIN",
					"NET_RAW",
				},
			},
		},
	}
}

// workingDirPrepContainer returns the init container to be used for preparing the working directory volume
// for use within the VICE analysis. This init container is only used when iRODS CSI driver integration is
// enabled.
//
// It may seem odd to use the file transfer image to initialize the working directory when no files are actually
// being transferred, but it works. We use it for a couple of different reasons. First, we need a Unix shell and
// it has one. Second, it's already set up so that we can configure it in a way that avoids image pull rate limits.
func (i *Incluster) workingDirPrepContainer(job *model.Job) apiv1.Container {

	// Build the command used to initialize the working directory.
	workingDirInitCommand := []string{
		"init_working_dir.sh",
		constants.CSIDriverLocalMountPath,
		i.getZoneMountPath(),
	}

	// Build the init container spec.
	initContainer := apiv1.Container{
		Name:            constants.WorkingDirInitContainerName,
		Image:           fmt.Sprintf("%s:%s", i.PorklockImage, i.PorklockTag),
		Command:         workingDirInitCommand,
		ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
		WorkingDir:      constants.WorkingDirInitContainerMountPath,
		VolumeMounts: []apiv1.VolumeMount{
			{
				Name:      persistentVolumeName(job),
				MountPath: constants.WorkingDirInitContainerMountPath,
				ReadOnly:  false,
			},
		},
		SecurityContext: &apiv1.SecurityContext{
			RunAsUser:  constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
			RunAsGroup: constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
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
	}

	return initContainer
}

// workingDirMountPath returns the path to the directory containing file inputs.
func workingDirMountPath(job *model.Job) string {
	return job.Steps[0].Component.Container.WorkingDirectory()
}

// initContainers returns a []apiv1.Container used for the InitContainers in
// the VICE app Deployment resource.
func (i *Incluster) initContainers(job *model.Job) []apiv1.Container {
	output := []apiv1.Container{}

	if !i.UseCSIDriver {
		output = append(output, i.inputStagingContainer(job))
	} else {
		output = append(output, i.workingDirPrepContainer(job))
	}

	return output
}

func (i *Incluster) defineAnalysisContainer(job *model.Job) apiv1.Container {
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

	volumeMounts := i.deploymentVolumeMounts(job)

	analysisContainer := apiv1.Container{
		Name: constants.AnalysisContainerName,
		Image: fmt.Sprintf(
			"%s:%s",
			job.Steps[0].Component.Container.Image.Name,
			job.Steps[0].Component.Container.Image.Tag,
		),
		ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
		Env:             analysisEnvironment,
		Resources:       *resourcing.Requirements(job),
		VolumeMounts:    volumeMounts,
		Ports:           analysisPorts(&job.Steps[0]),
		SecurityContext: &apiv1.SecurityContext{
			RunAsUser:  constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
			RunAsGroup: constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
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
			ProbeHandler: apiv1.ProbeHandler{
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
func (i *Incluster) deploymentContainers(job *model.Job) []apiv1.Container {
	output := []apiv1.Container{}

	output = append(output, apiv1.Container{
		Name:            constants.VICEProxyContainerName,
		Image:           i.ViceProxyImage,
		Command:         i.viceProxyCommand(job),
		ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
		Ports: []apiv1.ContainerPort{
			{
				Name:          constants.VICEProxyPortName,
				ContainerPort: constants.VICEProxyPort,
				Protocol:      apiv1.Protocol("TCP"),
			},
		},
		SecurityContext: &apiv1.SecurityContext{
			RunAsUser:  constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
			RunAsGroup: constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
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
		Resources: *resourcing.VICEProxyRequirements(job),
		ReadinessProbe: &apiv1.Probe{
			ProbeHandler: apiv1.ProbeHandler{
				HTTPGet: &apiv1.HTTPGetAction{
					Port:   intstr.FromInt(int(constants.VICEProxyPort)),
					Scheme: apiv1.URISchemeHTTP,
					Path:   "/",
				},
			},
		},
	})

	if !i.UseCSIDriver {
		output = append(output, apiv1.Container{
			Name:            constants.FileTransfersContainerName,
			Image:           fmt.Sprintf("%s:%s", i.PorklockImage, i.PorklockTag),
			Command:         fileTransferCommand(job),
			ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
			WorkingDir:      constants.InputPathListMountPath,
			VolumeMounts:    i.fileTransfersVolumeMounts(job),
			Ports: []apiv1.ContainerPort{
				{
					Name:          constants.FileTransfersPortName,
					ContainerPort: constants.FileTransfersPort,
					Protocol:      apiv1.Protocol("TCP"),
				},
			},
			SecurityContext: &apiv1.SecurityContext{
				RunAsUser:  constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
				RunAsGroup: constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
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
				ProbeHandler: apiv1.ProbeHandler{
					HTTPGet: &apiv1.HTTPGetAction{
						Port:   intstr.FromInt(int(constants.FileTransfersPort)),
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
func (i *Incluster) imagePullSecrets(_ *model.Job) []apiv1.LocalObjectReference {
	if i.ImagePullSecretName != "" {
		return []apiv1.LocalObjectReference{
			{Name: i.ImagePullSecretName},
		}
	}
	return []apiv1.LocalObjectReference{}
}

// GetDeployment assembles and returns the Deployment for the VICE analysis. It does
// not call the k8s API.
func (i *Incluster) GetDeployment(ctx context.Context, job *model.Job) (*appsv1.Deployment, error) {
	labels, err := i.LabelsFromJob(ctx, job)
	if err != nil {
		return nil, err
	}

	autoMount := false

	// Add the tolerations to use by default.
	tolerations := []apiv1.Toleration{
		{
			Key:      "analysis",
			Operator: apiv1.TolerationOpExists,
		},
	}

	// Add the node selector requirements to use by default.
	nodeSelectorRequirements := []apiv1.NodeSelectorRequirement{
		{
			Key:      constants.AnalysisAffinityKey,
			Operator: apiv1.NodeSelectorOperator(constants.AnalysisAffinityOperator),
		},
	}

	// Add the preferred node scheduling terms.
	preferredSchedTerms := []apiv1.PreferredSchedulingTerm{
		{
			Weight: 1,
			Preference: apiv1.NodeSelectorTerm{
				MatchExpressions: []apiv1.NodeSelectorRequirement{
					{
						Key:      "vice",
						Operator: apiv1.NodeSelectorOpExists,
					},
				},
			},
		},
	}

	// Add the tolerations and node selector requirements for jobs that require a GPU.
	if resourcing.GPUEnabled(job) {
		tolerations = append(tolerations, apiv1.Toleration{
			Key:      constants.GPUTolerationKey,
			Operator: apiv1.TolerationOperator(constants.GPUTolerationOperator),
			Value:    constants.GPUTolerationValue,
			Effect:   apiv1.TaintEffect(constants.GPUTolerationEffect),
		})

		nodeSelectorRequirements = append(nodeSelectorRequirements, apiv1.NodeSelectorRequirement{
			Key:      constants.GPUAffinityKey,
			Operator: apiv1.NodeSelectorOperator(constants.GPUAffinityOperator),
			Values: []string{
				constants.GPUAffinityValue,
			},
		})
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:   job.InvocationID,
			Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: constants.Int32Ptr(1),
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
						RunAsUser:  constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
						RunAsGroup: constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
						FSGroup:    constants.Int64Ptr(int64(job.Steps[0].Component.Container.UID)),
					},
					Tolerations: tolerations,
					Affinity: &apiv1.Affinity{
						PodAntiAffinity: &apiv1.PodAntiAffinity{
							PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.WeightedPodAffinityTerm{
								{
									Weight: 100,
									PodAffinityTerm: apiv1.PodAffinityTerm{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"app-type": "interactive",
											},
										},
										TopologyKey: "kubernetes.io/hostname",
									},
								},
							},
						},
						NodeAffinity: &apiv1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &apiv1.NodeSelector{
								NodeSelectorTerms: []apiv1.NodeSelectorTerm{
									{
										MatchExpressions: nodeSelectorRequirements,
									},
								},
							},
							PreferredDuringSchedulingIgnoredDuringExecution: preferredSchedTerms,
						},
					},
				},
			},
		},
	}

	return deployment, nil
}

// IsAnalysisInClsuter returns true when the provided analysis already has a deplyoment
// configured in the cluster. It may return true if the deployment is in the process of
// being Terminated, Pending, or in a CrashLoop, so don't depend on this to tell you if
// it's in a good state.
func (i *Incluster) IsAnalysisInCluster(ctx context.Context, externalID string) (bool, error) {
	var (
		found bool
		err   error
	)
	lbls := labels.Set(map[string]string{
		"app-type":    "interactive",
		"external-id": externalID,
	})
	opts := metav1.ListOptions{
		LabelSelector: lbls.String(),
	}
	list, err := i.clientset.CoreV1().Pods(i.ViceNamespace).List(ctx, opts)
	if err != nil {
		return found, err
	}
	found = len(list.Items) > 0
	return found, nil
}
