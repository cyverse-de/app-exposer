package incluster

import (
	"context"
	"maps"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/incluster/jobinfo"
	"github.com/cyverse-de/app-exposer/operator"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/vicebuild"
	"github.com/cyverse-de/model/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// fakeJobInfo returns a fixed label set so BuildAnalysisBundle can run without
// the apps/DB dependency. It returns exactly what vicebuild.BuildLabels would
// produce for the equivalent spec, so the golden comparison isolates the
// structural/transform equivalence rather than re-checking label assembly
// (already covered by vicebuild's TestBuildLabelsMatchesJobInfo).
type fakeJobInfo struct{ labels map[string]string }

func (f fakeJobInfo) JobLabels(_ context.Context, _ *model.Job) (map[string]string, error) {
	// Return a fresh copy per call, matching the real JobLabels — some callers
	// (the CSI data-volume labels) mutate the returned map.
	return maps.Clone(f.labels), nil
}

var _ jobinfo.JobInfo = fakeJobInfo{}

// resourcingDefaults mirrors the resourcing package's compile-time defaults
// (no setters are called in tests), so vicebuild produces the same resource
// requirements the legacy resourcing path does.
func resourcingDefaults() vicebuild.ResourceDefaults {
	return vicebuild.ResourceDefaults{
		DefaultCPURequest:   resource.MustParse("1000m"),
		DefaultCPULimit:     resource.MustParse("2000m"),
		DefaultMemRequest:   resource.MustParse("2Gi"),
		DefaultMemLimit:     resource.MustParse("8Gi"),
		DefaultStorage:      resource.MustParse("1Gi"),
		DoCPULimit:          true,
		DoMemLimit:          true,
		ViceProxyCPURequest: resource.MustParse("100m"),
		ViceProxyCPULimit:   resource.MustParse("200m"),
		ViceProxyMemRequest: resource.MustParse("100Mi"),
		ViceProxyMemLimit:   resource.MustParse("200Mi"),
		ViceProxyStorage:    resource.MustParse("100Mi"),
		ViceProxyStorageLim: resource.MustParse("200Mi"),
		DoViceProxyCPULimit: true,
		DoViceProxyMemLimit: true,
		DoViceProxyStorage:  true,
	}
}

// goldenParams captures the cluster knobs varied across golden cases.
type goldenParams struct {
	useCSI       bool
	vendor       string
	modelKey     string
	modelMapping map[string]string
}

// goldenConfig is the vicebuild.Config whose values correspond to goldenInit()
// plus the operator transform parameters the golden test simulates.
func goldenConfig(p goldenParams) *vicebuild.Config {
	return &vicebuild.Config{
		PorklockImage:           "harbor.cyverse.org/de/porklock",
		PorklockTag:             "latest",
		ViceProxyImage:          "harbor.cyverse.org/de/vice-proxy",
		UseCSIDriver:            p.useCSI,
		IRODSZone:               "example",
		LocalStorageClass:       "", // matches legacy (PVC class left unset, operator default)
		FrontendBaseURL:         "https://de.example.org",
		BaseDomain:              "cyverse.run",
		Namespace:               "vice-apps",
		GatewayNamespace:        "prod",
		GatewayName:             "vice",
		GatewayProvider:         "traefik",
		ImagePullSecretName:     "imanimagepullsecret",
		ClusterConfigSecretName: "cluster-config-secret",
		UserSuffix:              "@example.org",
		InputPathListIdentifier: "imapathlist",
		GPUVendor:               p.vendor,
		GPUModelAffinityKey:     p.modelKey,
		GPUModelMapping:         p.modelMapping,
		LoadingServiceName:      "vice-operator-loading",
		LoadingServicePort:      80,
		Resources:               resourcingDefaults(),
	}
}

// applyOperatorTransforms reproduces HandleLaunch's transform sequence on a
// legacy bundle so it can be compared against the vicebuild output.
func applyOperatorTransforms(b *operatorclient.AnalysisBundle, cfg *vicebuild.Config) {
	if b.HTTPRoute != nil {
		operator.TransformHostnames(b.HTTPRoute, cfg.BaseDomain)
		operator.TransformGatewayNamespace(b.HTTPRoute, cfg.GatewayNamespace, cfg.GatewayName)
		operator.TransformBackendToLoadingService(b.HTTPRoute, cfg.LoadingServiceName, cfg.LoadingServicePort)
	}
	operator.EnsurePermissionsConfigMap(b, cfg.UserSuffix)
	operator.TransformViceProxyArgs(b.Deployment, string(b.AnalysisID), cfg.ClusterConfigSecretName)
	operator.TransformGPUModels(b.Deployment, cfg.GPUModelAffinityKey, cfg.GPUModelMapping)
	operator.TransformGPUVendor(b.Deployment, operator.GPUVendor(cfg.GPUVendor))
}

func goldenJob(withGPU bool) *model.Job {
	c := model.Container{
		Image:       model.ContainerImage{Name: "cyverse/jupyter", Tag: "latest"},
		UID:         1000,
		MinCPUCores: 1,
		MaxCPUCores: 4,
		Ports:       []model.Ports{{ContainerPort: 8888}},
	}
	if withGPU {
		c.MinGPUs = 1
		c.MaxGPUs = 2
		c.GPUModels = []string{"NVIDIA-A10G"}
	}
	return &model.Job{
		ID:           "analysis-1",
		InvocationID: "external-1",
		Name:         "My Analysis",
		AppID:        "app-1",
		AppName:      "JupyterLab",
		UserID:       "user-1",
		Submitter:    "someuser",
		UserHome:     "/example/home/someuser",
		// Exercises the porklock file-transfer command's -m metadata args
		// (non-CSI cases) and the FileMetadata mapping in the spec.
		FileMetadata: []model.FileMetadata{{Attribute: "ipc-analysis-id", Value: "analysis-1", Unit: ""}},
		Steps: []model.Step{{
			Component:   model.StepComponent{Container: c},
			Environment: map[string]string{"FOO": "bar"},
		}},
	}
}

// TestGoldenEquivalence is the migration safety net: for representative jobs,
// the operator-side spec build (BuildVICESpec → vicebuild) produces objects
// equivalent to the legacy app-exposer build (BuildAnalysisBundle) plus the
// operator's transform pass. It compares the transform-sensitive surfaces — the
// exact places the two paths could diverge.
func TestGoldenEquivalence(t *testing.T) {
	tests := []struct {
		name    string
		withGPU bool
		params  goldenParams
	}{
		{name: "csi, no gpu", withGPU: false, params: goldenParams{useCSI: true, vendor: operatorclient.GPUVendorNvidia}},
		{name: "non-csi, no gpu", withGPU: false, params: goldenParams{useCSI: false, vendor: operatorclient.GPUVendorNvidia}},
		{name: "nvidia gpu with model", withGPU: true, params: goldenParams{useCSI: true, vendor: operatorclient.GPUVendorNvidia}},
		{
			name: "eks gpu model mapping", withGPU: true,
			params: goldenParams{useCSI: true, vendor: operatorclient.GPUVendorNvidia,
				modelKey: "eks.amazonaws.com/instance-gpu-name", modelMapping: map[string]string{"NVIDIA-A10G": "a10g"}},
		},
		{name: "amd gpu", withGPU: true, params: goldenParams{useCSI: true, vendor: operatorclient.GPUVendorAMD}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := goldenJob(tt.withGPU)
			analysisID := constants.AnalysisID(job.ID)
			cfg := goldenConfig(tt.params)

			// Spec path.
			spec, err := buildVICESpec(job, analysisID, "10.0.0.1")
			require.NoError(t, err)
			specBundle, err := cfg.BuildBundle(spec)
			require.NoError(t, err)

			// Legacy path: build with a struct-literal Incluster (fake jobInfo
			// returns the same labels vicebuild produces), then transform.
			i := &Incluster{Init: *goldenInit(tt.params), jobInfo: fakeJobInfo{labels: vicebuild.BuildLabels(spec)}}
			legacy, err := i.BuildAnalysisBundle(context.Background(), job, analysisID)
			require.NoError(t, err)
			applyOperatorTransforms(legacy, cfg)

			assertBundlesEquivalent(t, legacy, specBundle)
		})
	}
}

// goldenInit returns the legacy Init matching goldenConfig.
func goldenInit(p goldenParams) *Init {
	return &Init{
		PorklockImage:           "harbor.cyverse.org/de/porklock",
		PorklockTag:             "latest",
		UseCSIDriver:            p.useCSI,
		InputPathListIdentifier: "imapathlist",
		ImagePullSecretName:     "imanimagepullsecret",
		ViceProxyImage:          "harbor.cyverse.org/de/vice-proxy",
		FrontendBaseURL:         "https://de.example.org",
		ViceDomain:              "cyverse.run",
		VICEBackendNamespace:    "prod",
		ViceNamespace:           "vice-apps",
		UserSuffix:              "@example.org",
		IRODSZone:               "example",
		GatewayProvider:         "traefik",
		ClusterConfigSecretName: "cluster-config-secret",
	}
}

func assertBundlesEquivalent(t *testing.T, legacy, spec *operatorclient.AnalysisBundle) {
	t.Helper()

	// Top-level identity.
	assert.Equal(t, legacy.AnalysisID, spec.AnalysisID)

	// Deployment: name, labels, pod hostname, image-pull secrets.
	require.NotNil(t, spec.Deployment)
	assert.Equal(t, legacy.Deployment.Name, spec.Deployment.Name, "deployment name")
	assert.Equal(t, legacy.Deployment.Labels, spec.Deployment.Labels, "deployment labels")
	lp, sp := legacy.Deployment.Spec.Template.Spec, spec.Deployment.Spec.Template.Spec
	assert.Equal(t, lp.Hostname, sp.Hostname, "pod hostname")
	assert.Equal(t, lp.ImagePullSecrets, sp.ImagePullSecrets, "image pull secrets")

	// Containers and init containers compared by name + image + key fields.
	assertContainersEquivalent(t, lp.Containers, sp.Containers)
	assert.Equal(t, containerNames(lp.InitContainers), containerNames(sp.InitContainers), "init container names")
	assert.Equal(t, volumeNames(lp.Volumes), volumeNames(sp.Volumes), "pod volume names")

	// Node affinity (GPU model key/values) and tolerations.
	assert.Equal(t, lp.Affinity, sp.Affinity, "affinity (incl. GPU node selectors)")
	assert.Equal(t, lp.Tolerations, sp.Tolerations, "tolerations")

	// Service.
	require.NotNil(t, spec.Service)
	assert.Equal(t, legacy.Service.Name, spec.Service.Name, "service name")
	assert.Equal(t, legacy.Service.Spec.Ports, spec.Service.Spec.Ports, "service ports")
	assert.Equal(t, legacy.Service.Spec.Selector, spec.Service.Spec.Selector, "service selector")

	// HTTPRoute: hostname, parentRefs, backend, namespace.
	require.NotNil(t, spec.HTTPRoute)
	assert.Equal(t, legacy.HTTPRoute.Name, spec.HTTPRoute.Name, "route name")
	assert.Equal(t, legacy.HTTPRoute.Namespace, spec.HTTPRoute.Namespace, "route namespace")
	assert.Equal(t, legacy.HTTPRoute.Spec.Hostnames, spec.HTTPRoute.Spec.Hostnames, "route hostnames")
	assert.Equal(t, legacy.HTTPRoute.Spec.ParentRefs, spec.HTTPRoute.Spec.ParentRefs, "route parentRefs")
	assert.Equal(t, legacy.HTTPRoute.Spec.Rules, spec.HTTPRoute.Spec.Rules, "route rules (incl. backend)")

	// ConfigMaps compared as name->data maps (order-independent).
	assert.Equal(t, configMapData(legacy.ConfigMaps), configMapData(spec.ConfigMaps), "configmap data")

	// PVCs: name -> storage class.
	assert.Equal(t, pvcClasses(legacy.PersistentVolumeClaims), pvcClasses(spec.PersistentVolumeClaims), "pvc storage classes")

	// PVs (CSI): name -> CSI volume attributes.
	assert.Equal(t, len(legacy.PersistentVolumes), len(spec.PersistentVolumes), "pv count")
	if len(legacy.PersistentVolumes) == 1 && len(spec.PersistentVolumes) == 1 {
		assert.Equal(t, legacy.PersistentVolumes[0].Spec.CSI.VolumeAttributes,
			spec.PersistentVolumes[0].Spec.CSI.VolumeAttributes, "CSI volume attributes")
	}

	// PDB.
	require.NotNil(t, spec.PodDisruptionBudget)
	assert.Equal(t, legacy.PodDisruptionBudget.Name, spec.PodDisruptionBudget.Name, "pdb name")
}

func assertContainersEquivalent(t *testing.T, legacy, spec []apiv1.Container) {
	t.Helper()
	require.Equal(t, containerNames(legacy), containerNames(spec), "container names/order")
	byName := func(cs []apiv1.Container) map[string]apiv1.Container {
		m := make(map[string]apiv1.Container, len(cs))
		for _, c := range cs {
			m[c.Name] = c
		}
		return m
	}
	lm, sm := byName(legacy), byName(spec)
	for name, lc := range lm {
		sc := sm[name]
		assert.Equalf(t, lc.Image, sc.Image, "%s image", name)
		assert.Equalf(t, lc.Command, sc.Command, "%s command", name)
		assert.Equalf(t, lc.Args, sc.Args, "%s args", name)
		assert.Equalf(t, lc.Resources, sc.Resources, "%s resources", name)
		assert.Equalf(t, lc.EnvFrom, sc.EnvFrom, "%s envFrom", name)
		assert.Equalf(t, lc.Ports, sc.Ports, "%s ports", name)
		assert.Equalf(t, lc.WorkingDir, sc.WorkingDir, "%s workingDir", name)
		assert.Equalf(t, lc.SecurityContext, sc.SecurityContext, "%s securityContext", name)
		assert.Equalf(t, lc.ReadinessProbe, sc.ReadinessProbe, "%s readinessProbe", name)
		// Env is the most analysis-specific surface (REDIRECT_URL, IPLANT_*,
		// plus the analysis's own vars); compare order-independently since the
		// two builders may emit the map-derived vars in different orders.
		assert.ElementsMatchf(t, lc.Env, sc.Env, "%s env", name)
		assert.Equalf(t, volumeMountNames(lc.VolumeMounts), volumeMountNames(sc.VolumeMounts), "%s volume mounts", name)
	}
}

func containerNames(cs []apiv1.Container) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func volumeNames(vs []apiv1.Volume) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}

func volumeMountNames(ms []apiv1.VolumeMount) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

func configMapData(cms []*apiv1.ConfigMap) map[string]map[string]string {
	out := make(map[string]map[string]string, len(cms))
	for _, cm := range cms {
		out[cm.Name] = cm.Data
	}
	return out
}

func pvcClasses(pvcs []*apiv1.PersistentVolumeClaim) map[string]string {
	out := make(map[string]string, len(pvcs))
	for _, pvc := range pvcs {
		sc := ""
		if pvc.Spec.StorageClassName != nil {
			sc = *pvc.Spec.StorageClassName
		}
		out[pvc.Name] = sc
	}
	return out
}
