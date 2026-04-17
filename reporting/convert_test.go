package reporting

import (
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// testLabels returns a standard set of labels for testing.
func testLabels() map[string]string {
	return map[string]string{
		"analysis-name":           "test-analysis",
		constants.AppNameLabel:    "test-app",
		constants.AppIDLabel:      "app-123",
		constants.ExternalIDLabel: "ext-456",
		constants.UserIDLabel:     "user-789",
		constants.UsernameLabel:   "testuser",
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestDeploymentInfoFrom(t *testing.T) {
	tests := []struct {
		name       string
		deployment *appsv1.Deployment
		wantPort   int32
		wantUser   int64
		wantGroup  int64
		wantImage  string
	}{
		{
			name: "normal case with ports and security context",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deploy",
					Namespace: "vice-apps",
					Labels:    testLabels(),
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:    "analysis",
									Image:   "discoenv/test:latest",
									Command: []string{"/bin/sh"},
									Ports: []corev1.ContainerPort{
										{ContainerPort: 8080},
									},
									SecurityContext: &corev1.SecurityContext{
										RunAsUser:  int64Ptr(1000),
										RunAsGroup: int64Ptr(1000),
									},
								},
							},
						},
					},
				},
			},
			wantPort:  8080,
			wantUser:  1000,
			wantGroup: 1000,
			wantImage: "discoenv/test:latest",
		},
		{
			name: "minimal container with no ports and no security context",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "minimal-deploy",
					Namespace: "vice-apps",
					Labels:    testLabels(),
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "analysis",
									Image: "discoenv/minimal:latest",
								},
							},
						},
					},
				},
			},
			wantPort:  0,
			wantUser:  0,
			wantGroup: 0,
			wantImage: "discoenv/minimal:latest",
		},
		{
			name: "security context with only RunAsUser set",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "partial-sc",
					Namespace: "vice-apps",
					Labels:    testLabels(),
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "analysis",
									Image: "discoenv/partial:latest",
									SecurityContext: &corev1.SecurityContext{
										RunAsUser: int64Ptr(500),
									},
								},
							},
						},
					},
				},
			},
			wantPort:  0,
			wantUser:  500,
			wantGroup: 0,
			wantImage: "discoenv/partial:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := DeploymentInfoFrom(tt.deployment)
			require.NotNil(t, info)
			assert.Equal(t, tt.wantImage, info.Image)
			assert.Equal(t, tt.wantPort, info.Port)
			assert.Equal(t, tt.wantUser, info.User)
			assert.Equal(t, tt.wantGroup, info.Group)
			assert.Equal(t, tt.deployment.Name, info.Name)
			assert.Equal(t, tt.deployment.Namespace, info.Namespace)
			assert.Equal(t, "testuser", info.Username)
		})
	}
}

func TestIngressInfoFrom(t *testing.T) {
	tests := []struct {
		name               string
		ingress            *netv1.Ingress
		wantDefaultBackend string
	}{
		{
			name: "normal case with default backend",
			ingress: &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "vice-apps",
					Labels:    testLabels(),
				},
				Spec: netv1.IngressSpec{
					DefaultBackend: &netv1.IngressBackend{
						Service: &netv1.IngressServiceBackend{
							Name: "my-svc",
							Port: netv1.ServiceBackendPort{Number: 80},
						},
					},
					Rules: []netv1.IngressRule{
						{Host: "test.example.com"},
					},
				},
			},
			wantDefaultBackend: "my-svc:80",
		},
		{
			name: "nil default backend",
			ingress: &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-backend",
					Namespace: "vice-apps",
					Labels:    testLabels(),
				},
				Spec: netv1.IngressSpec{},
			},
			wantDefaultBackend: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := IngressInfoFrom(tt.ingress)
			require.NotNil(t, info)
			assert.Equal(t, tt.wantDefaultBackend, info.DefaultBackend)
			assert.Equal(t, tt.ingress.Name, info.Name)
		})
	}
}

func TestRouteInfoFrom(t *testing.T) {
	tests := []struct {
		name          string
		route         *gatewayv1.HTTPRoute
		wantHostnames []string
	}{
		{
			name: "normal case with hostnames",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "vice-apps",
					Labels:    testLabels(),
				},
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{"a.example.com", "b.example.com"},
				},
			},
			wantHostnames: []string{"a.example.com", "b.example.com"},
		},
		{
			name: "no hostnames",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-route",
					Namespace: "vice-apps",
					Labels:    testLabels(),
				},
				Spec: gatewayv1.HTTPRouteSpec{},
			},
			wantHostnames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := RouteInfoFrom(tt.route)
			require.NotNil(t, info)
			assert.Equal(t, tt.wantHostnames, info.Hostnames)
			assert.Equal(t, tt.route.Name, info.Name)
		})
	}
}

func TestServiceInfoFrom(t *testing.T) {
	tests := []struct {
		name      string
		svc       *corev1.Service
		wantPorts int
	}{
		{
			name: "normal case with ports",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-svc",
					Namespace: "vice-apps",
					Labels:    testLabels(),
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "http",
							Port:       80,
							TargetPort: intstr.FromInt32(8080),
							Protocol:   corev1.ProtocolTCP,
							NodePort:   30000,
						},
					},
				},
			},
			wantPorts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ServiceInfoFrom(tt.svc)
			require.NotNil(t, info)
			assert.Len(t, info.Ports, tt.wantPorts)
			assert.Equal(t, tt.svc.Name, info.Name)

			if tt.wantPorts > 0 {
				assert.Equal(t, "http", info.Ports[0].Name)
				assert.Equal(t, int32(80), info.Ports[0].Port)
				assert.Equal(t, int32(8080), info.Ports[0].TargetPort)
				assert.Equal(t, "TCP", info.Ports[0].Protocol)
			}
		})
	}
}

func TestSortByCreationTime(t *testing.T) {
	r := &ResourceInfo{
		Deployments: []DeploymentInfo{
			{MetaInfo: MetaInfo{Name: "oldest", CreationTimestamp: "2024-01-01 00:00:00 +0000 UTC"}},
			{MetaInfo: MetaInfo{Name: "newest", CreationTimestamp: "2024-03-01 00:00:00 +0000 UTC"}},
			{MetaInfo: MetaInfo{Name: "middle", CreationTimestamp: "2024-02-01 00:00:00 +0000 UTC"}},
		},
		Services: []ServiceInfo{
			{MetaInfo: MetaInfo{Name: "svc-old", CreationTimestamp: "2024-01-15 00:00:00 +0000 UTC"}},
			{MetaInfo: MetaInfo{Name: "svc-new", CreationTimestamp: "2024-06-15 00:00:00 +0000 UTC"}},
		},
		Pods:       []PodInfo{},
		ConfigMaps: []ConfigMapInfo{},
		Ingresses:  []IngressInfo{},
		Routes:     []RouteInfo{},
	}

	SortByCreationTime(r)

	// Deployments should be newest-first.
	require.Len(t, r.Deployments, 3)
	assert.Equal(t, "newest", r.Deployments[0].Name)
	assert.Equal(t, "middle", r.Deployments[1].Name)
	assert.Equal(t, "oldest", r.Deployments[2].Name)

	// Services should be newest-first.
	require.Len(t, r.Services, 2)
	assert.Equal(t, "svc-new", r.Services[0].Name)
	assert.Equal(t, "svc-old", r.Services[1].Name)
}
