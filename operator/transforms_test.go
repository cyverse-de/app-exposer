package operator

import (
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// makeTestHTTPRoute builds a minimal HTTPRoute for testing.
func makeTestHTTPRoute() *gatewayv1.HTTPRoute {
	port := gatewayv1.PortNumber(8080)
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "vice-apps",
			Labels: map[string]string{
				"external-id": "abc-123",
				"username":    "testuser",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"abc123.vice.example.com"},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "test-svc",
									Port: &port,
								},
							},
						},
					},
				},
			},
		},
	}
}

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
// equalized to the lower value for both NVIDIA and AMD vendors.
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
			name:     "nvidia: equalized to lower (requests < limits)",
			vendor:   GPUVendorNvidia,
			gpuKey:   nvidiaGPUResource,
			reqQty:   "1",
			limQty:   "2",
			wantBoth: "1",
		},
		{
			name:     "nvidia: equalized to lower (limits < requests)",
			vendor:   GPUVendorNvidia,
			gpuKey:   nvidiaGPUResource,
			reqQty:   "3",
			limQty:   "1",
			wantBoth: "1",
		},
		{
			name:     "amd: equalized to lower (requests < limits)",
			vendor:   GPUVendorAMD,
			gpuKey:   amdGPUResource,
			reqQty:   "1",
			limQty:   "2",
			wantBoth: "1",
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
				"GPU requests should be equalized to the lower value")
			assert.Equal(t, tt.wantBoth, limQty.String(),
				"GPU limits should be equalized to the lower value")
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

func TestTransformBackendToLoadingService(t *testing.T) {
	tests := []struct {
		name        string
		route       *gatewayv1.HTTPRoute
		serviceName string
		servicePort int32
	}{
		{
			name:        "HTTPRoute backend is rewritten",
			route:       makeTestHTTPRoute(),
			serviceName: "vice-operator-loading",
			servicePort: 80,
		},
		{
			name:        "nil route is no-op",
			route:       nil,
			serviceName: "vice-operator-loading",
			servicePort: 80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TransformBackendToLoadingService(tt.route, tt.serviceName, tt.servicePort)

			if tt.route != nil {
				ref := tt.route.Spec.Rules[0].BackendRefs[0]
				assert.Equal(t, gatewayv1.ObjectName(tt.serviceName), ref.Name)
				assert.Equal(t, gatewayv1.PortNumber(tt.servicePort), *ref.Port)
			}
		})
	}
}

func TestTransformHostnames(t *testing.T) {
	tests := []struct {
		name           string
		route          *gatewayv1.HTTPRoute
		baseDomain     string
		wantRouteHosts []gatewayv1.Hostname
	}{
		{
			name: "HTTPRoute hostnames rewritten",
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{"a1234.cyverse.run"},
				},
			},
			baseDomain:     "localhost",
			wantRouteHosts: []gatewayv1.Hostname{"a1234.localhost"},
		},
		{
			name:       "nil route does not panic",
			baseDomain: "localhost",
		},
		{
			name: "empty baseDomain is no-op",
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{"a1234.cyverse.run"},
				},
			},
			baseDomain:     "",
			wantRouteHosts: []gatewayv1.Hostname{"a1234.cyverse.run"},
		},
		{
			name: "hostname with no dot is unchanged",
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{"localhost"},
				},
			},
			baseDomain:     "localhost",
			wantRouteHosts: []gatewayv1.Hostname{"localhost"},
		},
		{
			name: "multiple hostnames all rewritten",
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{
						"abc123.cyverse.run",
						"def456.cyverse.run",
					},
				},
			},
			baseDomain: "localhost",
			wantRouteHosts: []gatewayv1.Hostname{
				"abc123.localhost",
				"def456.localhost",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TransformHostnames(tt.route, tt.baseDomain)

			if tt.route != nil && tt.wantRouteHosts != nil {
				assert.Equal(t, tt.wantRouteHosts, tt.route.Spec.Hostnames)
			}
		})
	}
}

func TestTransformViceProxyArgs(t *testing.T) {
	// makeDeploymentWithContainers builds a deployment with the given containers.
	makeDeployment := func(containers ...apiv1.Container) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-dep"},
			Spec: appsv1.DeploymentSpec{
				Template: apiv1.PodTemplateSpec{
					Spec: apiv1.PodSpec{
						Containers: containers,
					},
				},
			},
		}
	}

	const testSecret = "cluster-config-secret"

	tests := []struct {
		name              string
		deployment        *appsv1.Deployment
		analysisID        string
		secretName        string
		wantArgs          []string
		wantBackendURL    string
		wantEnvFrom       bool // expect envFrom to be added
		wantPermissionsVM bool // expect permissions volume mount to be added
	}{
		{
			name: "injects args with correct backend URL from analysis container port",
			deployment: makeDeployment(
				apiv1.Container{Name: "vice-proxy", Image: "vice-proxy:latest"},
				apiv1.Container{
					Name:  "analysis",
					Image: "jupyter:latest",
					Ports: []apiv1.ContainerPort{{ContainerPort: 8888}},
				},
			),
			analysisID:        "abc-123",
			secretName:        testSecret,
			wantArgs:          []string{"--analysis-id", "abc-123", "--backend-url", "http://localhost:8888", "--ws-backend-url", "http://localhost:8888", "--listen-addr", "0.0.0.0:60002"},
			wantBackendURL:    "http://localhost:8888",
			wantEnvFrom:       true,
			wantPermissionsVM: true,
		},
		{
			name: "falls back to default backend URL when no analysis port",
			deployment: makeDeployment(
				apiv1.Container{Name: "vice-proxy", Image: "vice-proxy:latest"},
				apiv1.Container{Name: "analysis", Image: "jupyter:latest"},
			),
			analysisID:        "def-456",
			secretName:        testSecret,
			wantArgs:          []string{"--analysis-id", "def-456", "--backend-url", "http://localhost:60000", "--ws-backend-url", "http://localhost:60000", "--listen-addr", "0.0.0.0:60002"},
			wantBackendURL:    "http://localhost:60000",
			wantEnvFrom:       true,
			wantPermissionsVM: true,
		},
		{
			name:       "nil deployment does not panic",
			deployment: nil,
			analysisID: "abc-123",
			secretName: testSecret,
		},
		{
			name: "skips input-files container when deriving backend URL",
			deployment: makeDeployment(
				apiv1.Container{Name: "vice-proxy", Image: "vice-proxy:latest"},
				apiv1.Container{
					Name:  "input-files",
					Image: "porklock:latest",
					Ports: []apiv1.ContainerPort{{ContainerPort: 60001}},
				},
				apiv1.Container{
					Name:  "analysis",
					Image: "rstudio:latest",
					Ports: []apiv1.ContainerPort{{ContainerPort: 3838}},
				},
			),
			analysisID:        "ghi-789",
			secretName:        testSecret,
			wantBackendURL:    "http://localhost:3838",
			wantEnvFrom:       true,
			wantPermissionsVM: true,
		},
		{
			name: "no vice-proxy container is a no-op",
			deployment: makeDeployment(
				apiv1.Container{Name: "analysis", Image: "jupyter:latest"},
			),
			analysisID: "xyz",
			secretName: testSecret,
		},
		{
			name: "empty secret name skips envFrom",
			deployment: makeDeployment(
				apiv1.Container{Name: "vice-proxy", Image: "vice-proxy:latest"},
				apiv1.Container{
					Name:  "analysis",
					Image: "jupyter:latest",
					Ports: []apiv1.ContainerPort{{ContainerPort: 8888}},
				},
			),
			analysisID:        "no-secret",
			secretName:        "",
			wantArgs:          []string{"--analysis-id", "no-secret", "--backend-url", "http://localhost:8888", "--ws-backend-url", "http://localhost:8888", "--listen-addr", "0.0.0.0:60002"},
			wantPermissionsVM: true,
		},
		{
			name: "does not duplicate envFrom when already present",
			deployment: func() *appsv1.Deployment {
				d := makeDeployment(
					apiv1.Container{
						Name:  "vice-proxy",
						Image: "vice-proxy:latest",
						EnvFrom: []apiv1.EnvFromSource{
							{SecretRef: &apiv1.SecretEnvSource{
								LocalObjectReference: apiv1.LocalObjectReference{Name: testSecret},
							}},
						},
					},
					apiv1.Container{
						Name:  "analysis",
						Image: "jupyter:latest",
						Ports: []apiv1.ContainerPort{{ContainerPort: 8888}},
					},
				)
				return d
			}(),
			analysisID:        "dup-test",
			secretName:        testSecret,
			wantArgs:          []string{"--analysis-id", "dup-test", "--backend-url", "http://localhost:8888", "--ws-backend-url", "http://localhost:8888", "--listen-addr", "0.0.0.0:60002"},
			wantEnvFrom:       true,
			wantPermissionsVM: true,
		},
		{
			name: "does not duplicate permissions mount when already present",
			deployment: func() *appsv1.Deployment {
				return makeDeployment(
					apiv1.Container{
						Name:  "vice-proxy",
						Image: "vice-proxy:latest",
						VolumeMounts: []apiv1.VolumeMount{
							{
								Name:      constants.PermissionsVolumeName,
								MountPath: constants.PermissionsMountPath,
								ReadOnly:  true,
							},
						},
					},
					apiv1.Container{
						Name:  "analysis",
						Image: "jupyter:latest",
						Ports: []apiv1.ContainerPort{{ContainerPort: 8888}},
					},
				)
			}(),
			analysisID:        "perm-dup",
			secretName:        testSecret,
			wantArgs:          []string{"--analysis-id", "perm-dup", "--backend-url", "http://localhost:8888", "--ws-backend-url", "http://localhost:8888", "--listen-addr", "0.0.0.0:60002"},
			wantEnvFrom:       true,
			wantPermissionsVM: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TransformViceProxyArgs(tt.deployment, tt.analysisID, tt.secretName)

			if tt.deployment == nil {
				return
			}

			// Find vice-proxy container.
			var vp *apiv1.Container
			for i, c := range tt.deployment.Spec.Template.Spec.Containers {
				if c.Name == "vice-proxy" {
					vp = &tt.deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}

			if tt.wantArgs != nil {
				require.NotNil(t, vp, "vice-proxy container should exist")
				assert.Equal(t, tt.wantArgs, vp.Args)
			}

			if tt.wantBackendURL != "" && vp != nil {
				// Verify the backend URL appears in args.
				assert.Contains(t, vp.Args, tt.wantBackendURL)
			}

			if tt.wantEnvFrom && vp != nil {
				// Verify envFrom contains the secret reference exactly once.
				count := 0
				for _, ref := range vp.EnvFrom {
					if ref.SecretRef != nil && ref.SecretRef.Name == testSecret {
						count++
					}
				}
				assert.Equal(t, 1, count, "expected exactly one envFrom secretRef for %s", testSecret)
			}

			if tt.wantPermissionsVM && vp != nil {
				// Verify the permissions volume mount was added exactly once.
				count := 0
				for _, vm := range vp.VolumeMounts {
					if vm.Name == constants.PermissionsVolumeName {
						count++
					}
				}
				assert.Equal(t, 1, count, "expected exactly one permissions VolumeMount")
			}
		})
	}
}

func TestTransformGatewayNamespace(t *testing.T) {
	qaNamespace := gatewayv1.Namespace("qa")

	tests := []struct {
		name          string
		route         *gatewayv1.HTTPRoute
		namespace     string
		wantNamespace string
	}{
		{
			name: "parentRef namespace rewritten",
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{
							{Namespace: &qaNamespace, Name: "vice"},
						},
					},
				},
			},
			namespace:     "vice-apps",
			wantNamespace: "vice-apps",
		},
		{
			name:      "nil route does not panic",
			route:     nil,
			namespace: "vice-apps",
		},
		{
			name: "empty namespace is no-op",
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{
							{Namespace: &qaNamespace, Name: "vice"},
						},
					},
				},
			},
			namespace:     "",
			wantNamespace: "qa",
		},
		{
			name: "multiple parentRefs all rewritten",
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{
							{Namespace: &qaNamespace, Name: "vice"},
							{Namespace: &qaNamespace, Name: "other"},
						},
					},
				},
			},
			namespace:     "de",
			wantNamespace: "de",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TransformGatewayNamespace(tt.route, tt.namespace)

			if tt.route == nil {
				return
			}
			for _, ref := range tt.route.Spec.ParentRefs {
				require.NotNil(t, ref.Namespace)
				assert.Equal(t, tt.wantNamespace, string(*ref.Namespace))
			}
		})
	}
}
