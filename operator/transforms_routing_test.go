package operator

import (
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
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
		gwName        string
		wantNamespace string
		wantName      string
	}{
		{
			name: "parentRef namespace and name rewritten",
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
			gwName:        "new-vice",
			wantNamespace: "vice-apps",
			wantName:      "new-vice",
		},
		{
			name:      "nil route does not panic",
			route:     nil,
			namespace: "vice-apps",
			gwName:    "vice",
		},
		{
			name: "empty namespace and name is no-op",
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
			gwName:        "",
			wantNamespace: "qa",
			wantName:      "vice",
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
			gwName:        "gw",
			wantNamespace: "de",
			wantName:      "gw",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TransformGatewayNamespace(tt.route, tt.namespace, tt.gwName)

			if tt.route == nil {
				return
			}
			for _, ref := range tt.route.Spec.ParentRefs {
				require.NotNil(t, ref.Namespace)
				assert.Equal(t, tt.wantNamespace, string(*ref.Namespace))
				assert.Equal(t, tt.wantName, string(ref.Name))
			}
		})
	}
}

func TestEnsurePermissionsConfigMap(t *testing.T) {
	makeBundle := func(username string, existingCM bool) *operatorclient.AnalysisBundle {
		b := &operatorclient.AnalysisBundle{
			AnalysisID: "test-analysis",
			Deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-dep",
					Labels: map[string]string{"analysis-id": "test-analysis"},
				},
			},
		}
		if username != "" {
			b.Deployment.Labels["username"] = username
		}
		if existingCM {
			b.ConfigMaps = append(b.ConfigMaps, &apiv1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "permissions-test-dep"},
			})
		}
		return b
	}

	tests := []struct {
		name      string
		bundle    *operatorclient.AnalysisBundle
		wantCMs   int
		wantOwner string
	}{
		{
			name:      "creates permissions ConfigMap with owner",
			bundle:    makeBundle("testuser", false),
			wantCMs:   1,
			wantOwner: "testuser" + constants.DefaultUserSuffix,
		},
		{
			name:      "does not double-append suffix when username already has it",
			bundle:    makeBundle("testuser"+constants.DefaultUserSuffix, false),
			wantCMs:   1,
			wantOwner: "testuser" + constants.DefaultUserSuffix,
		},
		{
			name:    "no-op when permissions ConfigMap already exists",
			bundle:  makeBundle("testuser", true),
			wantCMs: 1,
		},
		{
			name:    "skips when username label is missing",
			bundle:  makeBundle("", false),
			wantCMs: 0,
		},
		{
			name:    "nil bundle does not panic",
			bundle:  nil,
			wantCMs: 0,
		},
		{
			name: "nil deployment does not panic",
			bundle: &operatorclient.AnalysisBundle{
				AnalysisID: "test",
			},
			wantCMs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			EnsurePermissionsConfigMap(tt.bundle, constants.DefaultUserSuffix)

			if tt.bundle == nil {
				return
			}

			assert.Len(t, tt.bundle.ConfigMaps, tt.wantCMs)

			if tt.wantOwner != "" {
				require.Len(t, tt.bundle.ConfigMaps, 1)
				cm := tt.bundle.ConfigMaps[0]
				assert.Contains(t, cm.Data[constants.PermissionsFileName], tt.wantOwner)
			}
		})
	}
}
