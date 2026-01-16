// Package coordinator provides components for coordinating VICE deployments
// across multiple clusters.
package coordinator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/resourcing"
	"github.com/cyverse-de/app-exposer/vicetypes"
	"github.com/cyverse-de/model/v9"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	defaultStorageCapacity, _ = resourcev1.ParseQuantity("5Gi")
)

// SpecBuilderConfig contains configuration for building VICE deployment specs.
type SpecBuilderConfig struct {
	PorklockImage                 string
	PorklockTag                   string
	UseCSIDriver                  bool
	InputPathListIdentifier       string
	TicketInputPathListIdentifier string
	ImagePullSecretName           string
	ViceProxyImage                string
	FrontendBaseURL               string
	ViceDefaultBackendService     string
	ViceDefaultBackendServicePort int
	GetAnalysisIDService          string
	CheckResourceAccessService    string
	VICEBackendNamespace          string
	AppsServiceBaseURL            string
	ViceNamespace                 string
	UserSuffix                    string
	KeycloakBaseURL               string
	KeycloakRealm                 string
	KeycloakClientID              string
	KeycloakClientSecret          string
	IRODSZone                     string
	IngressClass                  string
	LocalStorageClass             string
	DisableViceProxyAuth          bool
}

// SpecBuilder builds VICEDeploymentSpec from model.Job.
type SpecBuilder struct {
	config SpecBuilderConfig
	apps   *apps.Apps
}

// NewSpecBuilder creates a new SpecBuilder.
func NewSpecBuilder(config SpecBuilderConfig, apps *apps.Apps) *SpecBuilder {
	return &SpecBuilder{
		config: config,
		apps:   apps,
	}
}

// IRODSFSPathMapping defines a single path mapping for the iRODS CSI driver.
type IRODSFSPathMapping struct {
	IRODSPath           string `yaml:"irods_path" json:"irods_path"`
	MappingPath         string `yaml:"mapping_path" json:"mapping_path"`
	ResourceType        string `yaml:"resource_type" json:"resource_type"`
	ReadOnly            bool   `yaml:"read_only" json:"read_only"`
	CreateDir           bool   `yaml:"create_dir" json:"create_dir"`
	IgnoreNotExistError bool   `yaml:"ignore_not_exist_error" json:"ignore_not_exist_error"`
}

// BuildSpec builds a complete VICEDeploymentSpec from a model.Job.
func (b *SpecBuilder) BuildSpec(ctx context.Context, job *model.Job) (*vicetypes.VICEDeploymentSpec, error) {
	labels, err := b.labelsFromJob(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("failed to build labels: %w", err)
	}

	// Build ConfigMaps
	configMaps, err := b.buildConfigMaps(ctx, job, labels)
	if err != nil {
		return nil, fmt.Errorf("failed to build configmaps: %w", err)
	}

	// Build PersistentVolumes
	pvs, err := b.getPersistentVolumes(ctx, job, labels)
	if err != nil {
		return nil, fmt.Errorf("failed to build persistent volumes: %w", err)
	}

	// Build PersistentVolumeClaims
	pvcs, err := b.getVolumeClaims(ctx, job, labels)
	if err != nil {
		return nil, fmt.Errorf("failed to build volume claims: %w", err)
	}

	// Build Deployment
	deployment, err := b.getDeployment(ctx, job, labels)
	if err != nil {
		return nil, fmt.Errorf("failed to build deployment: %w", err)
	}

	// Build Service
	service, err := b.getService(ctx, job, labels)
	if err != nil {
		return nil, fmt.Errorf("failed to build service: %w", err)
	}

	// Build Ingress
	ingress, err := b.getIngress(ctx, job, service, labels)
	if err != nil {
		return nil, fmt.Errorf("failed to build ingress: %w", err)
	}

	// Build PodDisruptionBudget
	pdb, err := b.getPodDisruptionBudget(ctx, job, labels)
	if err != nil {
		return nil, fmt.Errorf("failed to build pod disruption budget: %w", err)
	}

	subdomain := IngressName(job.UserID, job.InvocationID)

	return &vicetypes.VICEDeploymentSpec{
		Metadata: vicetypes.DeploymentMetadata{
			ExternalID:   job.InvocationID,
			AnalysisID:   job.InvocationID,
			UserID:       job.UserID,
			Username:     job.Submitter,
			AppID:        job.AppID,
			AppName:      job.AppName,
			AnalysisName: job.Name,
			Namespace:    b.config.ViceNamespace,
			Subdomain:    subdomain,
			LoginIP:      labels["login-ip"],
			Labels:       labels,
		},
		Deployment:             deployment,
		Service:                service,
		Ingress:                ingress,
		ConfigMaps:             configMaps,
		PersistentVolumes:      pvs,
		PersistentVolumeClaims: pvcs,
		PodDisruptionBudget:    pdb,
	}, nil
}

// IngressName returns the name of the ingress for a VICE analysis.
func IngressName(userID, invocationID string) string {
	return fmt.Sprintf("a%x", sha256.Sum256([]byte(fmt.Sprintf("%s%s", userID, invocationID))))[0:9]
}

// labelsFromJob returns labels for K8s resources.
func (b *SpecBuilder) labelsFromJob(ctx context.Context, job *model.Job) (map[string]string, error) {
	name := []rune(job.Name)

	var stringmax int
	if len(name) >= 63 {
		stringmax = 62
	} else {
		stringmax = len(name) - 1
	}

	ipAddr, err := b.apps.GetUserIP(ctx, job.UserID)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"external-id":   job.InvocationID,
		"app-name":      common.LabelValueString(job.AppName),
		"app-id":        job.AppID,
		"username":      common.LabelValueString(job.Submitter),
		"user-id":       job.UserID,
		"analysis-name": common.LabelValueString(string(name[:stringmax])),
		"app-type":      "interactive",
		"subdomain":     IngressName(job.UserID, job.InvocationID),
		"login-ip":      ipAddr,
	}, nil
}

// buildConfigMaps builds the ConfigMaps for a job.
func (b *SpecBuilder) buildConfigMaps(ctx context.Context, job *model.Job, labels map[string]string) ([]*apiv1.ConfigMap, error) {
	configMaps := []*apiv1.ConfigMap{}

	// Excludes ConfigMap
	excludesCM := b.excludesConfigMap(job, labels)
	configMaps = append(configMaps, excludesCM)

	// Input path list ConfigMap (only if there are inputs without tickets)
	if len(job.FilterInputsWithoutTickets()) > 0 {
		inputCM, err := b.inputPathListConfigMap(job, labels)
		if err != nil {
			return nil, err
		}
		configMaps = append(configMaps, inputCM)
	}

	return configMaps, nil
}

func excludesConfigMapName(job *model.Job) string {
	return fmt.Sprintf("excludes-file-%s", job.InvocationID)
}

func excludesFileContents(job *model.Job) *bytes.Buffer {
	var output bytes.Buffer
	for _, p := range job.ExcludeArguments() {
		output.WriteString(fmt.Sprintf("%s\n", p))
	}
	return &output
}

func (b *SpecBuilder) excludesConfigMap(job *model.Job, labels map[string]string) *apiv1.ConfigMap {
	return &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   excludesConfigMapName(job),
			Labels: labels,
		},
		Data: map[string]string{
			constants.ExcludesFileName: excludesFileContents(job).String(),
		},
	}
}

func inputPathListConfigMapName(job *model.Job) string {
	return fmt.Sprintf("input-path-list-%s", job.InvocationID)
}

func inputPathListContents(job *model.Job, pathListIdentifier string) (*bytes.Buffer, error) {
	buffer := bytes.NewBufferString("")

	_, err := fmt.Fprintf(buffer, "%s\n", pathListIdentifier)
	if err != nil {
		return nil, err
	}

	for _, input := range job.FilterInputsWithoutTickets() {
		_, err = fmt.Fprintf(buffer, "%s\n", input.IRODSPath())
		if err != nil {
			return nil, err
		}
	}

	return buffer, nil
}

func (b *SpecBuilder) inputPathListConfigMap(job *model.Job, labels map[string]string) (*apiv1.ConfigMap, error) {
	fileContents, err := inputPathListContents(job, b.config.InputPathListIdentifier)
	if err != nil {
		return nil, err
	}

	return &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   inputPathListConfigMapName(job),
			Labels: labels,
		},
		Data: map[string]string{
			constants.InputPathListFileName: fileContents.String(),
		},
	}, nil
}

// getDeployment builds the Deployment spec.
func (b *SpecBuilder) getDeployment(ctx context.Context, job *model.Job, labels map[string]string) (*appsv1.Deployment, error) {
	autoMount := false

	tolerations := []apiv1.Toleration{
		{
			Key:      "analysis",
			Operator: apiv1.TolerationOpExists,
		},
	}

	nodeSelectorRequirements := []apiv1.NodeSelectorRequirement{
		{
			Key:      constants.AnalysisAffinityKey,
			Operator: apiv1.NodeSelectorOperator(constants.AnalysisAffinityOperator),
		},
	}

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

	// Add GPU tolerations and node selector requirements if needed
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
					Volumes:                      b.deploymentVolumes(job),
					InitContainers:               b.initContainers(job),
					Containers:                   b.deploymentContainers(job),
					ImagePullSecrets:             b.imagePullSecrets(job),
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

func persistentVolumeName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", constants.WorkingDirVolumeName, job.InvocationID)
}

func (b *SpecBuilder) getFrontendURL(job *model.Job) *url.URL {
	frontURL, _ := url.Parse(b.config.FrontendBaseURL)
	frontURL.Host = fmt.Sprintf("%s.%s", IngressName(job.UserID, job.InvocationID), frontURL.Host)
	return frontURL
}

func (b *SpecBuilder) viceProxyCommand(job *model.Job) []string {
	frontURL := b.getFrontendURL(job)
	backendURL := fmt.Sprintf("http://localhost:%s", strconv.Itoa(job.Steps[0].Component.Container.Ports[0].ContainerPort))

	output := []string{
		"vice-proxy",
		"--listen-addr", fmt.Sprintf("0.0.0.0:%d", constants.VICEProxyPort),
		"--backend-url", backendURL,
		"--ws-backend-url", backendURL,
		"--frontend-url", frontURL.String(),
		"--external-id", job.InvocationID,
		"--get-analysis-id-base", fmt.Sprintf("http://%s.%s", b.config.GetAnalysisIDService, b.config.VICEBackendNamespace),
		"--check-resource-access-base", fmt.Sprintf("http://%s.%s", b.config.CheckResourceAccessService, b.config.VICEBackendNamespace),
		"--keycloak-base-url", b.config.KeycloakBaseURL,
		"--keycloak-realm", b.config.KeycloakRealm,
		"--keycloak-client-id", b.config.KeycloakClientID,
		"--keycloak-client-secret", b.config.KeycloakClientSecret,
	}

	if b.config.DisableViceProxyAuth {
		output = append(output, "--disable-auth")
	}

	return output
}

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

func fileTransferCommand(job *model.Job) []string {
	retval := []string{
		"/vice-file-transfers",
		"--listen-port", "60001",
		"--user", job.Submitter,
		"--excludes-file", path.Join(constants.ExcludesMountPath, constants.ExcludesFileName),
		"--path-list-file", path.Join(constants.InputPathListMountPath, constants.InputPathListFileName),
		"--upload-destination", job.OutputDirectory(),
		"--irods-config", constants.IRODSConfigFilePath,
		"--invocation-id", job.InvocationID,
	}
	for _, fm := range job.FileMetadata {
		retval = append(retval, fm.Argument()...)
	}
	return retval
}

func (b *SpecBuilder) fileTransfersVolumeMounts(job *model.Job) []apiv1.VolumeMount {
	retval := []apiv1.VolumeMount{
		{
			Name:      constants.PorklockConfigVolumeName,
			MountPath: constants.PorklockConfigMountPath,
			ReadOnly:  true,
		},
		{
			Name:      persistentVolumeName(job),
			MountPath: constants.FileTransfersInputsMountPath,
			ReadOnly:  false,
		},
		{
			Name:      constants.ExcludesVolumeName,
			MountPath: constants.ExcludesMountPath,
			ReadOnly:  true,
		},
	}

	if len(job.FilterInputsWithoutTickets()) > 0 {
		retval = append(retval, apiv1.VolumeMount{
			Name:      constants.InputPathListVolumeName,
			MountPath: constants.InputPathListMountPath,
			ReadOnly:  true,
		})
	}

	return retval
}

func (b *SpecBuilder) inputStagingContainer(job *model.Job) apiv1.Container {
	return apiv1.Container{
		Name:            constants.FileTransfersInitContainerName,
		Image:           fmt.Sprintf("%s:%s", b.config.PorklockImage, b.config.PorklockTag),
		Command:         append(fileTransferCommand(job), "--no-service"),
		ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
		WorkingDir:      constants.InputPathListMountPath,
		VolumeMounts:    b.fileTransfersVolumeMounts(job),
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
					"SETPCAP", "AUDIT_WRITE", "KILL", "SETGID", "SETUID",
					"NET_BIND_SERVICE", "SYS_CHROOT", "SETFCAP", "FSETID", "MKNOD",
				},
				Add: []apiv1.Capability{"NET_ADMIN", "NET_RAW"},
			},
		},
	}
}

func (b *SpecBuilder) getZoneMountPath() string {
	return fmt.Sprintf("%s/%s", constants.CSIDriverLocalMountPath, b.config.IRODSZone)
}

func (b *SpecBuilder) workingDirPrepContainer(job *model.Job) apiv1.Container {
	workingDirInitCommand := []string{
		"init_working_dir.sh",
		constants.CSIDriverLocalMountPath,
		b.getZoneMountPath(),
	}

	return apiv1.Container{
		Name:            constants.WorkingDirInitContainerName,
		Image:           fmt.Sprintf("%s:%s", b.config.PorklockImage, b.config.PorklockTag),
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
					"SETPCAP", "AUDIT_WRITE", "KILL", "SETGID", "SETUID",
					"NET_BIND_SERVICE", "SYS_CHROOT", "SETFCAP", "FSETID", "NET_RAW", "MKNOD",
				},
			},
		},
	}
}

func (b *SpecBuilder) initContainers(job *model.Job) []apiv1.Container {
	output := []apiv1.Container{}
	if !b.config.UseCSIDriver {
		output = append(output, b.inputStagingContainer(job))
	} else {
		output = append(output, b.workingDirPrepContainer(job))
	}
	return output
}

func workingDirMountPath(job *model.Job) string {
	return job.Steps[0].Component.Container.WorkingDirectory()
}

func (b *SpecBuilder) defineAnalysisContainer(job *model.Job) apiv1.Container {
	analysisEnvironment := []apiv1.EnvVar{}
	for envKey, envVal := range job.Steps[0].Environment {
		analysisEnvironment = append(analysisEnvironment, apiv1.EnvVar{
			Name:  envKey,
			Value: envVal,
		})
	}

	analysisEnvironment = append(analysisEnvironment,
		apiv1.EnvVar{Name: "REDIRECT_URL", Value: b.getFrontendURL(job).String()},
		apiv1.EnvVar{Name: "IPLANT_USER", Value: job.Submitter},
		apiv1.EnvVar{Name: "IPLANT_EXECUTION_ID", Value: job.InvocationID},
	)

	volumeMounts := b.deploymentVolumeMounts(job)

	analysisContainer := apiv1.Container{
		Name: constants.AnalysisContainerName,
		Image: fmt.Sprintf("%s:%s",
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
		analysisContainer.Command = []string{job.Steps[0].Component.Container.EntryPoint}
	}

	if job.Steps[0].Component.Container.WorkingDir != "" {
		analysisContainer.WorkingDir = job.Steps[0].Component.Container.WorkingDir
	}

	if len(job.Steps[0].Arguments()) != 0 {
		analysisContainer.Args = append(analysisContainer.Args, job.Steps[0].Arguments()...)
	}

	return analysisContainer
}

func (b *SpecBuilder) deploymentContainers(job *model.Job) []apiv1.Container {
	output := []apiv1.Container{}

	// VICE Proxy container
	output = append(output, apiv1.Container{
		Name:            constants.VICEProxyContainerName,
		Image:           b.config.ViceProxyImage,
		Command:         b.viceProxyCommand(job),
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
					"SETPCAP", "AUDIT_WRITE", "KILL", "SETGID", "SETUID",
					"SYS_CHROOT", "SETFCAP", "FSETID", "NET_RAW", "MKNOD",
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

	// File transfers container (only when not using CSI driver)
	if !b.config.UseCSIDriver {
		output = append(output, apiv1.Container{
			Name:            constants.FileTransfersContainerName,
			Image:           fmt.Sprintf("%s:%s", b.config.PorklockImage, b.config.PorklockTag),
			Command:         fileTransferCommand(job),
			ImagePullPolicy: apiv1.PullPolicy(apiv1.PullAlways),
			WorkingDir:      constants.InputPathListMountPath,
			VolumeMounts:    b.fileTransfersVolumeMounts(job),
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
						"SETPCAP", "AUDIT_WRITE", "KILL", "SETGID", "SETUID",
						"NET_BIND_SERVICE", "SYS_CHROOT", "SETFCAP", "FSETID", "NET_RAW", "MKNOD",
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

	output = append(output, b.defineAnalysisContainer(job))
	return output
}

func (b *SpecBuilder) imagePullSecrets(_ *model.Job) []apiv1.LocalObjectReference {
	if b.config.ImagePullSecretName != "" {
		return []apiv1.LocalObjectReference{{Name: b.config.ImagePullSecretName}}
	}
	return []apiv1.LocalObjectReference{}
}

// Volume-related functions

func (b *SpecBuilder) getCSIDataVolumeName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", constants.CSIDriverDataVolumeNamePrefix, job.InvocationID)
}

func (b *SpecBuilder) getCSIDataVolumeHandle(job *model.Job) string {
	return fmt.Sprintf("%s-handle-%s", constants.CSIDriverDataVolumeNamePrefix, job.InvocationID)
}

func (b *SpecBuilder) getCSIDataVolumeClaimName(job *model.Job) string {
	return fmt.Sprintf("%s-%s", constants.CSIDriverDataVolumeClaimNamePrefix, job.InvocationID)
}

func (b *SpecBuilder) getInputPathMappings(job *model.Job) ([]IRODSFSPathMapping, error) {
	mappings := []IRODSFSPathMapping{}
	mappingMap := map[string]string{}

	for _, step := range job.Steps {
		for _, stepInput := range step.Config.Inputs {
			irodsPath := stepInput.IRODSPath()
			if len(irodsPath) > 0 {
				var resourceType string
				switch strings.ToLower(stepInput.Type) {
				case "fileinput", "multifileselector":
					resourceType = "file"
				case "folderinput":
					resourceType = "dir"
				default:
					return nil, fmt.Errorf("unknown step input type - %s", stepInput.Type)
				}

				mountPath := fmt.Sprintf("%s/%s", constants.CSIDriverInputVolumeMountPath, filepath.Base(irodsPath))
				if existingIRODSPath, ok := mappingMap[mountPath]; ok {
					return nil, fmt.Errorf("tried to mount an input file %s at %s already used by - %s", irodsPath, mountPath, existingIRODSPath)
				}
				mappingMap[mountPath] = irodsPath

				mappings = append(mappings, IRODSFSPathMapping{
					IRODSPath:           irodsPath,
					MappingPath:         mountPath,
					ResourceType:        resourceType,
					ReadOnly:            true,
					CreateDir:           false,
					IgnoreNotExistError: true,
				})
			}
		}
	}
	return mappings, nil
}

func (b *SpecBuilder) getOutputPathMapping(job *model.Job) IRODSFSPathMapping {
	return IRODSFSPathMapping{
		IRODSPath:           job.OutputDirectory(),
		MappingPath:         constants.CSIDriverOutputVolumeMountPath,
		ResourceType:        "dir",
		ReadOnly:            false,
		CreateDir:           true,
		IgnoreNotExistError: true,
	}
}

func (b *SpecBuilder) getHomePathMapping(job *model.Job) IRODSFSPathMapping {
	return IRODSFSPathMapping{
		IRODSPath:           job.UserHome,
		MappingPath:         job.UserHome,
		ResourceType:        "dir",
		ReadOnly:            false,
		CreateDir:           false,
		IgnoreNotExistError: false,
	}
}

func (b *SpecBuilder) getSharedPathMapping() IRODSFSPathMapping {
	sharedHomeFullPath := fmt.Sprintf("/%s/home/shared", b.config.IRODSZone)
	return IRODSFSPathMapping{
		IRODSPath:           sharedHomeFullPath,
		MappingPath:         sharedHomeFullPath,
		ResourceType:        "dir",
		ReadOnly:            false,
		CreateDir:           false,
		IgnoreNotExistError: true,
	}
}

func (b *SpecBuilder) getPersistentVolumes(ctx context.Context, job *model.Job, labels map[string]string) ([]*apiv1.PersistentVolume, error) {
	if !b.config.UseCSIDriver {
		return nil, nil
	}

	dataPathMappings := []IRODSFSPathMapping{}

	inputPathMappings, err := b.getInputPathMappings(job)
	if err != nil {
		return nil, err
	}
	dataPathMappings = append(dataPathMappings, inputPathMappings...)
	dataPathMappings = append(dataPathMappings, b.getOutputPathMapping(job))

	if job.UserHome != "" {
		dataPathMappings = append(dataPathMappings, b.getHomePathMapping(job))
	}
	dataPathMappings = append(dataPathMappings, b.getSharedPathMapping())

	dataPathMappingsJSONBytes, err := json.Marshal(dataPathMappings)
	if err != nil {
		return nil, err
	}

	volmode := apiv1.PersistentVolumeFilesystem

	dataVolumeLabels := make(map[string]string)
	for k, v := range labels {
		dataVolumeLabels[k] = v
	}
	dataVolumeLabels["volume-name"] = b.getCSIDataVolumeName(job)

	dataVolume := &apiv1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.getCSIDataVolumeName(job),
			Labels: dataVolumeLabels,
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
			StorageClassName:              constants.CSIDriverStorageClassName,
			PersistentVolumeSource: apiv1.PersistentVolumeSource{
				CSI: &apiv1.CSIPersistentVolumeSource{
					Driver:       constants.CSIDriverName,
					VolumeHandle: b.getCSIDataVolumeHandle(job),
					VolumeAttributes: map[string]string{
						"client":              "irodsfuse",
						"path_mapping_json":   string(dataPathMappingsJSONBytes),
						"no_permission_check": "true",
						"clientUser":          job.Submitter,
						"uid":                 fmt.Sprintf("%d", job.Steps[0].Component.Container.UID),
						"gid":                 fmt.Sprintf("%d", job.Steps[0].Component.Container.UID),
					},
				},
			},
		},
	}

	return []*apiv1.PersistentVolume{dataVolume}, nil
}

func (b *SpecBuilder) getPersistentVolumeCapacity(job *model.Job) resourcev1.Quantity {
	var capacityToRequest = defaultStorageCapacity.Value()
	for _, step := range job.Steps {
		if step.Component.Container.MinDiskSpace > capacityToRequest {
			capacityToRequest = step.Component.Container.MinDiskSpace
		}
	}
	return *resourcev1.NewQuantity(capacityToRequest, resourcev1.BinarySI)
}

func (b *SpecBuilder) getVolumeClaims(ctx context.Context, job *model.Job, labels map[string]string) ([]*apiv1.PersistentVolumeClaim, error) {
	volumeClaims := []*apiv1.PersistentVolumeClaim{}

	// Local persistent volume claim
	persistentVolumeClaim := &apiv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:   persistentVolumeName(job),
			Labels: labels,
		},
		Spec: apiv1.PersistentVolumeClaimSpec{
			AccessModes: []apiv1.PersistentVolumeAccessMode{
				apiv1.ReadWriteOnce,
			},
			StorageClassName: &b.config.LocalStorageClass,
			Resources: apiv1.VolumeResourceRequirements{
				Requests: apiv1.ResourceList{
					apiv1.ResourceStorage: b.getPersistentVolumeCapacity(job),
				},
			},
		},
	}
	volumeClaims = append(volumeClaims, persistentVolumeClaim)

	if b.config.UseCSIDriver {
		storageclassname := constants.CSIDriverStorageClassName
		dataVolumeClaim := &apiv1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:   b.getCSIDataVolumeClaimName(job),
				Labels: labels,
			},
			Spec: apiv1.PersistentVolumeClaimSpec{
				AccessModes: []apiv1.PersistentVolumeAccessMode{
					apiv1.ReadWriteMany,
				},
				StorageClassName: &storageclassname,
				VolumeName:       b.getCSIDataVolumeName(job),
				Resources: apiv1.VolumeResourceRequirements{
					Requests: apiv1.ResourceList{
						apiv1.ResourceStorage: defaultStorageCapacity,
					},
				},
			},
		}
		volumeClaims = append(volumeClaims, dataVolumeClaim)
	}

	return volumeClaims, nil
}

func (b *SpecBuilder) getPersistentVolumeSources(job *model.Job) ([]*apiv1.Volume, error) {
	pVolName := persistentVolumeName(job)
	volumes := []*apiv1.Volume{
		{
			Name: pVolName,
			VolumeSource: apiv1.VolumeSource{
				PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
					ClaimName: pVolName,
				},
			},
		},
	}

	if b.config.UseCSIDriver {
		dataVolume := &apiv1.Volume{
			Name: b.getCSIDataVolumeClaimName(job),
			VolumeSource: apiv1.VolumeSource{
				PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
					ClaimName: b.getCSIDataVolumeClaimName(job),
				},
			},
		}
		volumes = append(volumes, dataVolume)
	}

	return volumes, nil
}

func (b *SpecBuilder) getPersistentVolumeMounts(job *model.Job) []*apiv1.VolumeMount {
	volumeMounts := []*apiv1.VolumeMount{}

	analysisDataVolumeMount := &apiv1.VolumeMount{
		Name:      persistentVolumeName(job),
		MountPath: workingDirMountPath(job),
		ReadOnly:  false,
	}
	volumeMounts = append(volumeMounts, analysisDataVolumeMount)

	if b.config.UseCSIDriver {
		dataVolumeMount := &apiv1.VolumeMount{
			Name:      b.getCSIDataVolumeClaimName(job),
			MountPath: constants.CSIDriverLocalMountPath,
		}
		volumeMounts = append(volumeMounts, dataVolumeMount)
	}

	return volumeMounts
}

func (b *SpecBuilder) deploymentVolumes(job *model.Job) []apiv1.Volume {
	output := []apiv1.Volume{}

	if len(job.FilterInputsWithoutTickets()) > 0 {
		output = append(output, apiv1.Volume{
			Name: constants.InputPathListVolumeName,
			VolumeSource: apiv1.VolumeSource{
				ConfigMap: &apiv1.ConfigMapVolumeSource{
					LocalObjectReference: apiv1.LocalObjectReference{
						Name: inputPathListConfigMapName(job),
					},
				},
			},
		})
	}

	if b.config.UseCSIDriver {
		volumeSources, err := b.getPersistentVolumeSources(job)
		if err != nil {
			log.Warn(err)
		} else {
			for _, volumeSource := range volumeSources {
				output = append(output, *volumeSource)
			}
		}
	} else {
		output = append(output, apiv1.Volume{
			Name: constants.PorklockConfigVolumeName,
			VolumeSource: apiv1.VolumeSource{
				Secret: &apiv1.SecretVolumeSource{
					SecretName: constants.PorklockConfigSecretName,
				},
			},
		})
	}

	output = append(output, apiv1.Volume{
		Name: constants.ExcludesVolumeName,
		VolumeSource: apiv1.VolumeSource{
			ConfigMap: &apiv1.ConfigMapVolumeSource{
				LocalObjectReference: apiv1.LocalObjectReference{
					Name: excludesConfigMapName(job),
				},
			},
		},
	})

	shmSize := resourcing.SharedMemoryAmount(job)
	if shmSize != nil {
		output = append(output, apiv1.Volume{
			Name: constants.SharedMemoryVolumeName,
			VolumeSource: apiv1.VolumeSource{
				EmptyDir: &apiv1.EmptyDirVolumeSource{
					Medium:    "Memory",
					SizeLimit: shmSize,
				},
			},
		})
	}

	return output
}

func (b *SpecBuilder) deploymentVolumeMounts(job *model.Job) []apiv1.VolumeMount {
	volumeMounts := []apiv1.VolumeMount{}

	persistentVolumeMounts := b.getPersistentVolumeMounts(job)
	for _, persistentVolumeMount := range persistentVolumeMounts {
		volumeMounts = append(volumeMounts, *persistentVolumeMount)
	}

	if resourcing.SharedMemoryAmount(job) != nil {
		volumeMounts = append(volumeMounts, apiv1.VolumeMount{
			Name:      constants.SharedMemoryVolumeName,
			MountPath: constants.ShmDevice,
			ReadOnly:  false,
		})
	}

	return volumeMounts
}

// Service-related functions

func (b *SpecBuilder) getService(ctx context.Context, job *model.Job, labels map[string]string) (*apiv1.Service, error) {
	svc := apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:   fmt.Sprintf("vice-%s", job.InvocationID),
			Labels: labels,
		},
		Spec: apiv1.ServiceSpec{
			Selector: map[string]string{
				"external-id": job.InvocationID,
			},
			Ports: []apiv1.ServicePort{
				{
					Name:       constants.FileTransfersPortName,
					Protocol:   apiv1.ProtocolTCP,
					Port:       constants.FileTransfersPort,
					TargetPort: intstr.FromString(constants.FileTransfersPortName),
				},
				{
					Name:       constants.VICEProxyPortName,
					Protocol:   apiv1.ProtocolTCP,
					Port:       constants.VICEProxyServicePort,
					TargetPort: intstr.FromString(constants.VICEProxyPortName),
				},
			},
		},
	}

	return &svc, nil
}

// Ingress-related functions

func (b *SpecBuilder) getIngress(ctx context.Context, job *model.Job, svc *apiv1.Service, labels map[string]string) (*netv1.Ingress, error) {
	var (
		rules       []netv1.IngressRule
		defaultPort int32
	)

	ingressName := IngressName(job.UserID, job.InvocationID)

	for _, port := range svc.Spec.Ports {
		if port.Name == constants.VICEProxyPortName {
			defaultPort = port.Port
		}
	}

	if defaultPort == 0 {
		return nil, fmt.Errorf("port %s was not found in the service", constants.VICEProxyPortName)
	}

	defaultBackend := &netv1.IngressBackend{
		Service: &netv1.IngressServiceBackend{
			Name: b.config.ViceDefaultBackendService,
			Port: netv1.ServiceBackendPort{
				Number: int32(b.config.ViceDefaultBackendServicePort),
			},
		},
	}

	backend := &netv1.IngressBackend{
		Service: &netv1.IngressServiceBackend{
			Name: svc.Name,
			Port: netv1.ServiceBackendPort{
				Number: defaultPort,
			},
		},
	}

	pathType := netv1.PathTypeImplementationSpecific
	rules = append(rules, netv1.IngressRule{
		Host: ingressName,
		IngressRuleValue: netv1.IngressRuleValue{
			HTTP: &netv1.HTTPIngressRuleValue{
				Paths: []netv1.HTTPIngressPath{
					{
						PathType: &pathType,
						Backend:  *backend,
					},
				},
			},
		},
	})

	annotations := map[string]string{
		"nginx.ingress.kubernetes.io/proxy-body-size":       "4096m",
		"nginx.ingress.kubernetes.io/proxy-read-timeout":    "172800",
		"nginx.ingress.kubernetes.io/proxy-send-timeout":    "172800",
		"nginx.ingress.kubernetes.io/proxy-connect-timeout": "5000",
		"nginx.ingress.kubernetes.io/server-snippets": `location / {
	proxy_set_header Upgrade $http_upgrade;
	proxy_http_version 1.1;
	proxy_set_header X-Forwarded-Host $http_host;
	proxy_set_header X-Forwarded-Proto $scheme;
	proxy_set_header X-Forwarded-For $remote_addr;
	proxy_set_header Host $host;
	proxy_set_header Connection "upgrade";
	proxy_cache_bypass $http_upgrade;
}`,
	}

	ingressClass := b.config.IngressClass

	return &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        job.InvocationID,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: netv1.IngressSpec{
			DefaultBackend:   defaultBackend,
			IngressClassName: &ingressClass,
			Rules:            rules,
		},
	}, nil
}

// PodDisruptionBudget-related functions

func (b *SpecBuilder) getPodDisruptionBudget(ctx context.Context, job *model.Job, labels map[string]string) (*policyv1.PodDisruptionBudget, error) {
	pdb := policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:   job.InvocationID,
			Labels: labels,
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"external-id": job.InvocationID,
				},
			},
			MaxUnavailable: &intstr.IntOrString{
				Type:   intstr.Int,
				IntVal: 0,
			},
		},
	}

	return &pdb, nil
}
