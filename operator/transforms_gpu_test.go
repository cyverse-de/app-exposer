package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseGPUVendor(t *testing.T) {
	tests := []struct {
		input   string
		want    GPUVendor
		wantErr bool
	}{
		{"nvidia", GPUVendorNvidia, false},
		{"amd", GPUVendorAMD, false},
		{"intel", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseGPUVendor(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// makeGPUDeployment builds a deployment with NVIDIA GPU resources and
// GPU model node affinity for testing TransformGPUVendor.
func makeGPUDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-dep"},
		Spec: appsv1.DeploymentSpec{
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:  "analysis",
							Image: "img",
							Resources: apiv1.ResourceRequirements{
								Requests: apiv1.ResourceList{
									nvidiaGPUResource:    resource.MustParse("1"),
									apiv1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Limits: apiv1.ResourceList{
									nvidiaGPUResource:    resource.MustParse("1"),
									apiv1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
					Affinity: &apiv1.Affinity{
						NodeAffinity: &apiv1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &apiv1.NodeSelector{
								NodeSelectorTerms: []apiv1.NodeSelectorTerm{
									{
										MatchExpressions: []apiv1.NodeSelectorRequirement{
											{Key: "gpu", Operator: apiv1.NodeSelectorOpIn, Values: []string{"true"}},
											{Key: nvidiaModelAffinityK, Operator: apiv1.NodeSelectorOpIn, Values: []string{"NVIDIA-A100"}},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestTransformGPUVendor(t *testing.T) {
	tests := []struct {
		name              string
		deployment        *appsv1.Deployment
		vendor            GPUVendor
		wantGPUResource   apiv1.ResourceName // expected GPU resource key
		wantAffinityKey   string             // expected GPU model affinity key
		wantNoGPUResource apiv1.ResourceName // resource key that should NOT exist
	}{
		{
			name:              "nvidia vendor is a no-op",
			deployment:        makeGPUDeployment(),
			vendor:            GPUVendorNvidia,
			wantGPUResource:   nvidiaGPUResource,
			wantAffinityKey:   nvidiaModelAffinityK,
			wantNoGPUResource: amdGPUResource,
		},
		{
			name:              "amd vendor rewrites resources and affinity",
			deployment:        makeGPUDeployment(),
			vendor:            GPUVendorAMD,
			wantGPUResource:   amdGPUResource,
			wantAffinityKey:   amdModelAffinityK,
			wantNoGPUResource: nvidiaGPUResource,
		},
		{
			name:       "nil deployment does not panic",
			deployment: nil,
			vendor:     GPUVendorAMD,
		},
		{
			name: "deployment without GPU resources is unchanged",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "no-gpu"},
				Spec: appsv1.DeploymentSpec{
					Template: apiv1.PodTemplateSpec{
						Spec: apiv1.PodSpec{
							Containers: []apiv1.Container{
								{
									Name:  "analysis",
									Image: "img",
									Resources: apiv1.ResourceRequirements{
										Requests: apiv1.ResourceList{
											apiv1.ResourceCPU:    resource.MustParse("1"),
											apiv1.ResourceMemory: resource.MustParse("4Gi"),
										},
									},
								},
							},
						},
					},
				},
			},
			vendor: GPUVendorAMD,
		},
		{
			name: "gpu resources with nil affinity does not panic",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu-no-affinity"},
				Spec: appsv1.DeploymentSpec{
					Template: apiv1.PodTemplateSpec{
						Spec: apiv1.PodSpec{
							Containers: []apiv1.Container{
								{
									Name:  "analysis",
									Image: "img",
									Resources: apiv1.ResourceRequirements{
										Requests: apiv1.ResourceList{nvidiaGPUResource: resource.MustParse("1")},
										Limits:   apiv1.ResourceList{nvidiaGPUResource: resource.MustParse("1")},
									},
								},
							},
						},
					},
				},
			},
			vendor:            GPUVendorAMD,
			wantGPUResource:   amdGPUResource,
			wantNoGPUResource: nvidiaGPUResource,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TransformGPUVendor(tt.deployment, tt.vendor)

			if tt.deployment == nil {
				return // just verifying no panic
			}

			containers := tt.deployment.Spec.Template.Spec.Containers
			if tt.wantGPUResource != "" {
				reqs := containers[0].Resources.Requests
				qty, ok := reqs[tt.wantGPUResource]
				assert.True(t, ok, "expected resource %s in requests", tt.wantGPUResource)
				assert.Equal(t, "1", qty.String(), "GPU request quantity should be preserved")
				_, ok = reqs[tt.wantNoGPUResource]
				assert.False(t, ok, "unexpected resource %s in requests", tt.wantNoGPUResource)

				lims := containers[0].Resources.Limits
				qty, ok = lims[tt.wantGPUResource]
				assert.True(t, ok, "expected resource %s in limits", tt.wantGPUResource)
				assert.Equal(t, "1", qty.String(), "GPU limit quantity should be preserved")
				_, ok = lims[tt.wantNoGPUResource]
				assert.False(t, ok, "unexpected resource %s in limits", tt.wantNoGPUResource)
			}

			if tt.wantAffinityKey != "" {
				affinity := tt.deployment.Spec.Template.Spec.Affinity
				require.NotNil(t, affinity)
				terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
				found := false
				for _, term := range terms {
					for _, expr := range term.MatchExpressions {
						if expr.Key == tt.wantAffinityKey {
							found = true
						}
					}
				}
				assert.True(t, found, "expected affinity key %s", tt.wantAffinityKey)
			}

			// Non-GPU resources should be untouched.
			if len(containers[0].Resources.Requests) > 0 {
				_, hasMemory := containers[0].Resources.Requests[apiv1.ResourceMemory]
				if hasMemory {
					assert.Equal(t, "4Gi", containers[0].Resources.Requests.Memory().String())
				}
			}
		})
	}
}

// TestEqualizeGPUResources verifies that mismatched GPU requests/limits are
// equalized to the higher value for both NVIDIA and AMD vendors.
func TestEqualizeGPUResources(t *testing.T) {
	tests := []struct {
		name     string
		vendor   GPUVendor
		gpuKey   apiv1.ResourceName
		reqQty   string
		limQty   string
		wantBoth string // both requests and limits should equal this
	}{
		{
			name:     "nvidia: equalized to higher (requests < limits)",
			vendor:   GPUVendorNvidia,
			gpuKey:   nvidiaGPUResource,
			reqQty:   "1",
			limQty:   "2",
			wantBoth: "2",
		},
		{
			name:     "nvidia: equalized to higher (limits < requests)",
			vendor:   GPUVendorNvidia,
			gpuKey:   nvidiaGPUResource,
			reqQty:   "3",
			limQty:   "1",
			wantBoth: "3",
		},
		{
			name:     "amd: equalized to higher (requests < limits)",
			vendor:   GPUVendorAMD,
			gpuKey:   amdGPUResource,
			reqQty:   "1",
			limQty:   "2",
			wantBoth: "2",
		},
		{
			name:     "nvidia: already equal is no-op",
			vendor:   GPUVendorNvidia,
			gpuKey:   nvidiaGPUResource,
			reqQty:   "2",
			limQty:   "2",
			wantBoth: "2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build a deployment with NVIDIA GPU resources (the source format).
			// TransformGPUVendor renames to AMD before equalizing when needed.
			srcKey := nvidiaGPUResource
			dep := &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: apiv1.PodTemplateSpec{
						Spec: apiv1.PodSpec{
							Containers: []apiv1.Container{
								{
									Name:  "c",
									Image: "img",
									Resources: apiv1.ResourceRequirements{
										Requests: apiv1.ResourceList{srcKey: resource.MustParse(tt.reqQty)},
										Limits:   apiv1.ResourceList{srcKey: resource.MustParse(tt.limQty)},
									},
								},
							},
						},
					},
				},
			}

			TransformGPUVendor(dep, tt.vendor)

			res := dep.Spec.Template.Spec.Containers[0].Resources
			reqQty := res.Requests[tt.gpuKey]
			limQty := res.Limits[tt.gpuKey]
			assert.Equal(t, tt.wantBoth, reqQty.String(),
				"GPU requests should be equalized to the higher value")
			assert.Equal(t, tt.wantBoth, limQty.String(),
				"GPU limits should be equalized to the higher value")
		})
	}
}

func TestEqualizeGPUResourcesAsymmetric(t *testing.T) {
	tests := []struct {
		name     string
		requests apiv1.ResourceList // nil means not set
		limits   apiv1.ResourceList // nil means not set
		wantQty  string
	}{
		{
			name:     "only limits set — copies to requests",
			requests: nil,
			limits:   apiv1.ResourceList{nvidiaGPUResource: resource.MustParse("2")},
			wantQty:  "2",
		},
		{
			name:     "only requests set — copies to limits",
			requests: apiv1.ResourceList{nvidiaGPUResource: resource.MustParse("1")},
			limits:   nil,
			wantQty:  "1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dep := &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: apiv1.PodTemplateSpec{
						Spec: apiv1.PodSpec{
							Containers: []apiv1.Container{
								{
									Name:  "c",
									Image: "img",
									Resources: apiv1.ResourceRequirements{
										Requests: tt.requests,
										Limits:   tt.limits,
									},
								},
							},
						},
					},
				},
			}

			TransformGPUVendor(dep, GPUVendorNvidia)

			res := dep.Spec.Template.Spec.Containers[0].Resources
			reqQty := res.Requests[nvidiaGPUResource]
			limQty := res.Limits[nvidiaGPUResource]
			assert.Equal(t, tt.wantQty, reqQty.String(), "requests should be set")
			assert.Equal(t, tt.wantQty, limQty.String(), "limits should be set")
		})
	}
}

func TestTransformGPUVendorInitContainers(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-init"},
		Spec: appsv1.DeploymentSpec{
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					InitContainers: []apiv1.Container{
						{
							Name:  "init-data",
							Image: "img",
							Resources: apiv1.ResourceRequirements{
								Requests: apiv1.ResourceList{nvidiaGPUResource: resource.MustParse("2")},
								Limits:   apiv1.ResourceList{nvidiaGPUResource: resource.MustParse("2")},
							},
						},
					},
					Containers: []apiv1.Container{
						{Name: "main", Image: "img"},
					},
				},
			},
		},
	}

	TransformGPUVendor(dep, GPUVendorAMD)

	initContainer := dep.Spec.Template.Spec.InitContainers[0]
	qty, ok := initContainer.Resources.Requests[amdGPUResource]
	assert.True(t, ok, "init container should have amd.com/gpu in requests")
	assert.Equal(t, "2", qty.String(), "init container GPU quantity should be preserved")
	_, ok = initContainer.Resources.Requests[nvidiaGPUResource]
	assert.False(t, ok, "init container should not have nvidia.com/gpu in requests")

	qty, ok = initContainer.Resources.Limits[amdGPUResource]
	assert.True(t, ok, "init container should have amd.com/gpu in limits")
	assert.Equal(t, "2", qty.String(), "init container GPU limit quantity should be preserved")
}
