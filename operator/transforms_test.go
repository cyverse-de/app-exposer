package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
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

func TestTransformRouting(t *testing.T) {
	tests := []struct {
		name         string
		route        *gatewayv1.HTTPRoute
		routing      RoutingType
		ingressClass string
		wantRoute    bool // true if HTTPRoute should be returned
		wantIngress  bool // true if Ingress should be returned
	}{
		{
			name:         "nil route returns nil for both",
			route:        nil,
			routing:      RoutingGateway,
			ingressClass: "",
			wantRoute:    false,
			wantIngress:  false,
		},
		{
			name:         "gateway returns HTTPRoute only",
			route:        makeTestHTTPRoute(),
			routing:      RoutingGateway,
			ingressClass: "",
			wantRoute:    true,
			wantIngress:  false,
		},
		{
			name:         "nginx converts to Ingress",
			route:        makeTestHTTPRoute(),
			routing:      RoutingNginx,
			ingressClass: "nginx",
			wantRoute:    false,
			wantIngress:  true,
		},
		{
			name:         "tailscale converts to Ingress",
			route:        makeTestHTTPRoute(),
			routing:      RoutingTailscale,
			ingressClass: "tailscale",
			wantRoute:    false,
			wantIngress:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, ingress := TransformRouting(tt.route, tt.routing, tt.ingressClass)

			if tt.wantRoute {
				assert.NotNil(t, route)
			} else {
				assert.Nil(t, route)
			}

			if tt.wantIngress {
				require.NotNil(t, ingress)
				assert.Equal(t, tt.ingressClass, *ingress.Spec.IngressClassName)
				// Verify metadata was copied from the HTTPRoute.
				assert.Equal(t, tt.route.Name, ingress.Name)
				assert.Equal(t, tt.route.Namespace, ingress.Namespace)
				assert.Equal(t, tt.route.Labels, ingress.Labels)
			} else {
				assert.Nil(t, ingress)
			}
		})
	}
}

func TestHttpRouteToIngress(t *testing.T) {
	route := makeTestHTTPRoute()
	ingress := httpRouteToIngress(route, "nginx")

	// Verify basic metadata.
	assert.Equal(t, "test-route", ingress.Name)
	assert.Equal(t, "vice-apps", ingress.Namespace)
	assert.Equal(t, route.Labels, ingress.Labels)

	// Verify ingress class.
	require.NotNil(t, ingress.Spec.IngressClassName)
	assert.Equal(t, "nginx", *ingress.Spec.IngressClassName)

	// Verify default backend.
	require.NotNil(t, ingress.Spec.DefaultBackend)
	assert.Equal(t, "test-svc", ingress.Spec.DefaultBackend.Service.Name)
	assert.Equal(t, int32(8080), ingress.Spec.DefaultBackend.Service.Port.Number)

	// Verify rules match hostnames.
	require.Len(t, ingress.Spec.Rules, 1)
	assert.Equal(t, "abc123.vice.example.com", ingress.Spec.Rules[0].Host)

	// Verify conversion annotation.
	assert.Equal(t, "httproute/test-route", ingress.Annotations["converted-from"])
}

func TestTransformRoutingTailscaleRemovesNginxAnnotations(t *testing.T) {
	route := makeTestHTTPRoute()

	_, ingress := TransformRouting(route, RoutingTailscale, "tailscale")
	require.NotNil(t, ingress)

	// The converted ingress should have no nginx annotations.
	for key := range ingress.Annotations {
		assert.False(t, isNginxAnnotation(key),
			"nginx annotation %q should have been removed", key)
	}
}

func TestTransformIngress(t *testing.T) {
	nginxClass := "nginx"
	tailscaleClass := "tailscale"

	tests := []struct {
		name               string
		ingress            *netv1.Ingress
		targetRouting      RoutingType
		targetIngressClass string
		wantClass          string
		wantNginxAnnots    bool
	}{
		{
			name:               "nil ingress returns nil",
			ingress:            nil,
			targetRouting:      RoutingNginx,
			targetIngressClass: nginxClass,
			wantClass:          "",
		},
		{
			name: "nginx to nginx same class is no-op",
			ingress: &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ingress",
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/proxy-body-size": "4096m",
					},
				},
				Spec: netv1.IngressSpec{
					IngressClassName: &nginxClass,
				},
			},
			targetRouting:      RoutingNginx,
			targetIngressClass: nginxClass,
			wantClass:          nginxClass,
			wantNginxAnnots:    true,
		},
		{
			name: "nginx to tailscale removes nginx annotations",
			ingress: &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ingress",
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/proxy-body-size": "4096m",
						"other-annotation":                            "keep-me",
					},
				},
				Spec: netv1.IngressSpec{
					IngressClassName: &nginxClass,
				},
			},
			targetRouting:      RoutingTailscale,
			targetIngressClass: tailscaleClass,
			wantClass:          tailscaleClass,
			wantNginxAnnots:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TransformIngress(tt.ingress, tt.targetRouting, tt.targetIngressClass)

			if tt.ingress == nil {
				assert.Nil(t, result)
				return
			}

			assert.NotNil(t, result)
			assert.Equal(t, tt.wantClass, *result.Spec.IngressClassName)

			if !tt.wantNginxAnnots {
				for key := range result.Annotations {
					assert.False(t, isNginxAnnotation(key),
						"nginx annotation %q should have been removed", key)
				}
			}

			if tt.targetRouting == RoutingTailscale && tt.ingress.Annotations["other-annotation"] != "" {
				assert.Equal(t, "keep-me", result.Annotations["other-annotation"])
			}
		})
	}
}

func TestIsNginxAnnotation(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"nginx.ingress.kubernetes.io/proxy-body-size", true},
		{"nginx.ingress.kubernetes.io/server-snippets", true},
		{"other-annotation", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.want, isNginxAnnotation(tt.key))
		})
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
				// Check resource was renamed and quantity preserved.
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

func TestTransformBackendToLoadingService(t *testing.T) {
	tests := []struct {
		name        string
		route       *gatewayv1.HTTPRoute
		ingress     *netv1.Ingress
		serviceName string
		servicePort int32
		wantRoute   bool
		wantIngress bool
	}{
		{
			name:        "HTTPRoute backend is rewritten",
			route:       makeTestHTTPRoute(),
			serviceName: "vice-operator-loading",
			servicePort: 80,
			wantRoute:   true,
		},
		{
			name: "Ingress backend is rewritten",
			ingress: &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ing"},
				Spec: netv1.IngressSpec{
					DefaultBackend: &netv1.IngressBackend{
						Service: &netv1.IngressServiceBackend{
							Name: "analysis-svc",
							Port: netv1.ServiceBackendPort{Number: 8080},
						},
					},
					Rules: []netv1.IngressRule{
						{
							Host: "abc123.vice.example.com",
							IngressRuleValue: netv1.IngressRuleValue{
								HTTP: &netv1.HTTPIngressRuleValue{
									Paths: []netv1.HTTPIngressPath{
										{
											Backend: netv1.IngressBackend{
												Service: &netv1.IngressServiceBackend{
													Name: "analysis-svc",
													Port: netv1.ServiceBackendPort{Number: 8080},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			serviceName: "vice-operator-loading",
			servicePort: 80,
			wantIngress: true,
		},
		{
			name:        "nil route and nil ingress is no-op",
			serviceName: "vice-operator-loading",
			servicePort: 80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TransformBackendToLoadingService(tt.route, tt.ingress, tt.serviceName, tt.servicePort)

			if tt.wantRoute {
				require.NotNil(t, tt.route)
				ref := tt.route.Spec.Rules[0].BackendRefs[0]
				assert.Equal(t, gatewayv1.ObjectName(tt.serviceName), ref.Name)
				assert.Equal(t, gatewayv1.PortNumber(tt.servicePort), *ref.Port)
			}

			if tt.wantIngress {
				require.NotNil(t, tt.ingress)
				assert.Equal(t, tt.serviceName, tt.ingress.Spec.DefaultBackend.Service.Name)
				assert.Equal(t, tt.servicePort, tt.ingress.Spec.DefaultBackend.Service.Port.Number)
				for _, rule := range tt.ingress.Spec.Rules {
					for _, path := range rule.HTTP.Paths {
						assert.Equal(t, tt.serviceName, path.Backend.Service.Name)
						assert.Equal(t, tt.servicePort, path.Backend.Service.Port.Number)
					}
				}
			}
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
