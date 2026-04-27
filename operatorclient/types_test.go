package operatorclient

import (
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestCapacityResponseHasCapacity(t *testing.T) {
	tests := []struct {
		name           string
		availableSlots int
		want           bool
	}{
		{"unlimited reports true", -1, true},
		{"exhausted reports false", 0, false},
		{"single slot reports true", 1, true},
		{"many slots report true", 42, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CapacityResponse{AvailableSlots: tt.availableSlots}
			assert.Equal(t, tt.want, c.HasCapacity())
		})
	}
}

// validBundle builds an AnalysisBundle whose every labeled child resource
// carries the matching analysis-id label. Each TestAnalysisBundleValidate
// row mutates one field off this baseline so the tests stay focused on
// the one thing they're checking.
func validBundle(id string) *AnalysisBundle {
	labels := map[string]string{constants.AnalysisIDLabel: id}
	return &AnalysisBundle{
		AnalysisID: AnalysisID(id),
		Deployment: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "dep-" + id, Labels: labels},
		},
		Service: &apiv1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-" + id, Labels: labels},
		},
		HTTPRoute: &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route-" + id, Labels: labels},
		},
		ConfigMaps: []*apiv1.ConfigMap{{
			ObjectMeta: metav1.ObjectMeta{Name: "cm-" + id, Labels: labels},
		}},
		PersistentVolumes: []*apiv1.PersistentVolume{{
			ObjectMeta: metav1.ObjectMeta{Name: "pv-" + id, Labels: labels},
		}},
		PersistentVolumeClaims: []*apiv1.PersistentVolumeClaim{{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-" + id, Labels: labels},
		}},
		PodDisruptionBudget: &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Name: "pdb-" + id, Labels: labels},
		},
	}
}

// TestAnalysisBundleValidate exercises each Validate branch in turn. Rows
// mutate the baseline validBundle minimally so any failure points at the
// one invariant that broke rather than requiring the reader to diff the
// whole bundle structure.
func TestAnalysisBundleValidate(t *testing.T) {
	const validID = "abc-123"
	wrongLabels := map[string]string{constants.AnalysisIDLabel: "wrong-id"}

	tests := []struct {
		name    string
		mutate  func(*AnalysisBundle)
		wantSub string // substring expected in the error message; "" means no error
	}{
		{
			name:    "happy path: all fields valid",
			mutate:  func(*AnalysisBundle) {},
			wantSub: "",
		},
		{
			name:    "missing analysisID",
			mutate:  func(b *AnalysisBundle) { b.AnalysisID = "" },
			wantSub: "analysisID is required",
		},
		{
			name:    "missing deployment",
			mutate:  func(b *AnalysisBundle) { b.Deployment = nil },
			wantSub: "deployment is required",
		},
		{
			name:    "missing service",
			mutate:  func(b *AnalysisBundle) { b.Service = nil },
			wantSub: "service is required",
		},
		{
			name:    "deployment label mismatch",
			mutate:  func(b *AnalysisBundle) { b.Deployment.Labels = wrongLabels },
			wantSub: "deployment has analysis-id label",
		},
		{
			name:    "service label mismatch",
			mutate:  func(b *AnalysisBundle) { b.Service.Labels = wrongLabels },
			wantSub: "service has analysis-id label",
		},
		{
			name:    "httpRoute label mismatch",
			mutate:  func(b *AnalysisBundle) { b.HTTPRoute.Labels = wrongLabels },
			wantSub: "httpRoute has analysis-id label",
		},
		{
			name: "configMap label mismatch",
			mutate: func(b *AnalysisBundle) {
				b.ConfigMaps[0].Labels = wrongLabels
			},
			wantSub: "configMaps[0]",
		},
		{
			name: "persistentVolume label mismatch",
			mutate: func(b *AnalysisBundle) {
				b.PersistentVolumes[0].Labels = wrongLabels
			},
			wantSub: "persistentVolumes[0]",
		},
		{
			name: "persistentVolumeClaim label mismatch",
			mutate: func(b *AnalysisBundle) {
				b.PersistentVolumeClaims[0].Labels = wrongLabels
			},
			wantSub: "persistentVolumeClaims[0]",
		},
		{
			name: "podDisruptionBudget label mismatch",
			mutate: func(b *AnalysisBundle) {
				b.PodDisruptionBudget.Labels = wrongLabels
			},
			wantSub: "podDisruptionBudget has analysis-id label",
		},
		// Optional fields (HTTPRoute, PDB) should be skippable entirely.
		{
			name:    "optional httpRoute absent is fine",
			mutate:  func(b *AnalysisBundle) { b.HTTPRoute = nil },
			wantSub: "",
		},
		{
			name:    "optional podDisruptionBudget absent is fine",
			mutate:  func(b *AnalysisBundle) { b.PodDisruptionBudget = nil },
			wantSub: "",
		},
		// nil entries in slices are tolerated (they're skipped, not failed).
		{
			name: "nil configMap entry is skipped",
			mutate: func(b *AnalysisBundle) {
				b.ConfigMaps = append(b.ConfigMaps, nil)
			},
			wantSub: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := validBundle(validID)
			tt.mutate(b)

			err := b.Validate()
			if tt.wantSub == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantSub)
		})
	}
}

// gpuBundle builds a minimal AnalysisBundle whose deployment containers
// request the given GPU resource name in either Requests, Limits, or as
// an init-container resource. resourceName == "" means "no GPU."
// container == "init" places the resource on an init container instead
// of a regular one. Used to drive RequestedGPUVendor test cases.
func gpuBundle(resourceName, container, where string) *AnalysisBundle {
	c := apiv1.Container{Name: "main"}
	if resourceName != "" {
		rl := apiv1.ResourceList{apiv1.ResourceName(resourceName): resource.MustParse("1")}
		switch where {
		case "limits":
			c.Resources.Limits = rl
		default:
			c.Resources.Requests = rl
		}
	}

	dep := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: apiv1.PodTemplateSpec{}}}
	if container == "init" {
		dep.Spec.Template.Spec.InitContainers = []apiv1.Container{c}
		dep.Spec.Template.Spec.Containers = []apiv1.Container{{Name: "main"}}
	} else {
		dep.Spec.Template.Spec.Containers = []apiv1.Container{c}
	}
	return &AnalysisBundle{AnalysisID: AnalysisID("test"), Deployment: dep}
}

func TestRequestedGPUVendor(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		container    string // "main" or "init"
		where        string // "requests" or "limits"
		want         string
	}{
		{"no GPU returns empty", "", "main", "requests", ""},
		{"nvidia request returns nvidia", "nvidia.com/gpu", "main", "requests", "nvidia"},
		{"amd request returns amd", "amd.com/gpu", "main", "requests", "amd"},
		{"nvidia limit returns nvidia", "nvidia.com/gpu", "main", "limits", "nvidia"},
		{"amd limit returns amd", "amd.com/gpu", "main", "limits", "amd"},
		{"nvidia on init container returns nvidia", "nvidia.com/gpu", "init", "requests", "nvidia"},
		{"unknown GPU resource is ignored", "foo.example.com/gpu", "main", "requests", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := gpuBundle(tt.resourceName, tt.container, tt.where)
			assert.Equal(t, tt.want, b.RequestedGPUVendor())
		})
	}

	t.Run("nil bundle returns empty", func(t *testing.T) {
		var b *AnalysisBundle
		assert.Equal(t, "", b.RequestedGPUVendor())
	})

	t.Run("bundle with no deployment returns empty", func(t *testing.T) {
		b := &AnalysisBundle{AnalysisID: "test"}
		assert.Equal(t, "", b.RequestedGPUVendor())
	})
}
