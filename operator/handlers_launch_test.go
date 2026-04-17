package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestHandleLaunch(t *testing.T) {
	tests := []struct {
		name       string
		bundle     operatorclient.AnalysisBundle
		maxSlots   int
		setup      func(t *testing.T, cs *fake.Clientset) // optional pre-test setup
		wantStatus int
	}{
		{
			name: "successful launch creates resources",
			bundle: operatorclient.AnalysisBundle{
				AnalysisID: "test-analysis-1",
				Deployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-dep",
						Labels: map[string]string{constants.AnalysisIDLabel: "test-analysis-1"},
					},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "test"},
						},
						Template: apiv1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{"app": "test"},
							},
							Spec: apiv1.PodSpec{
								Containers: []apiv1.Container{{Name: "c", Image: "img"}},
							},
						},
					},
				},
				Service: &apiv1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-svc",
						Labels: map[string]string{constants.AnalysisIDLabel: "test-analysis-1"},
					},
					Spec: apiv1.ServiceSpec{
						Ports: []apiv1.ServicePort{{Port: 80}},
					},
				},
			},
			maxSlots:   10,
			wantStatus: http.StatusCreated,
		},
		{
			name: "launch at capacity returns 409",
			bundle: operatorclient.AnalysisBundle{
				AnalysisID: "test-analysis-2",
				Deployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "dep-2", Labels: map[string]string{constants.AnalysisIDLabel: "test-analysis-2"}},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test2"}},
						Template: apiv1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test2"}},
							Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
						},
					},
				},
				Service: &apiv1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-2", Labels: map[string]string{constants.AnalysisIDLabel: "test-analysis-2"}},
					Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
				},
			},
			maxSlots: 1,
			setup: func(t *testing.T, cs *fake.Clientset) {
				t.Helper()
				// Pre-fill the single slot with an existing VICE deployment.
				_, err := cs.AppsV1().Deployments("vice-apps").Create(context.Background(), &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "existing-dep",
						Labels: map[string]string{constants.AppTypeLabel: "interactive"},
					},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "existing"}},
						Template: apiv1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "existing"}},
							Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
						},
					},
				}, metav1.CreateOptions{})
				require.NoError(t, err)
			},
			wantStatus: http.StatusConflict,
		},
		{
			name:       "empty analysis ID returns 400",
			bundle:     operatorclient.AnalysisBundle{},
			maxSlots:   10,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, clientset, _ := newTestOperator(t, tt.maxSlots)
			if tt.setup != nil {
				tt.setup(t, clientset)
			}

			body, err := json.Marshal(tt.bundle)
			require.NoError(t, err)

			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/analyses", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err = op.HandleLaunch(c)
			if err != nil {
				he, ok := err.(*echo.HTTPError)
				require.True(t, ok, "expected HTTPError, got %T: %v", err, err)
				assert.Equal(t, tt.wantStatus, he.Code)
			} else {
				assert.Equal(t, tt.wantStatus, rec.Code)
			}

			// Verify resources were created for successful launches.
			if tt.wantStatus == http.StatusCreated {
				ctx := context.Background()
				_, err := clientset.AppsV1().Deployments("vice-apps").Get(ctx, "test-dep", metav1.GetOptions{})
				assert.NoError(t, err, "deployment should exist")

				_, err = clientset.CoreV1().Services("vice-apps").Get(ctx, "test-svc", metav1.GetOptions{})
				assert.NoError(t, err, "service should exist")

				// HandleLaunch creates a per-analysis egress NetworkPolicy using
				// deployment metadata labels (which include analysis-id), not pod
				// template labels (which may not). Verify the NP has the analysis-id
				// label so deleteAnalysisResources can find it during cleanup.
				np, err := clientset.NetworkingV1().NetworkPolicies("vice-apps").Get(ctx, "vice-egress-"+string(tt.bundle.AnalysisID), metav1.GetOptions{})
				assert.NoError(t, err, "per-analysis egress NetworkPolicy should exist")
				if np != nil {
					assert.Equal(t, string(tt.bundle.AnalysisID), np.Labels[constants.AnalysisIDLabel],
						"NetworkPolicy should have analysis-id label from deployment metadata")
				}
			}
		})
	}
}

// TestHandleLaunchGPUVendorAMD verifies that HandleLaunch rewrites NVIDIA GPU
// resources to AMD when the operator is configured with GPUVendorAMD.
func TestHandleLaunchGPUVendorAMD(t *testing.T) {
	op, clientset, _ := newTestOperator(t, 10, GPUVendorAMD)

	bundle := operatorclient.AnalysisBundle{
		AnalysisID: "gpu-amd-test",
		Deployment: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "gpu-dep",
				Labels: map[string]string{constants.AnalysisIDLabel: "gpu-amd-test"},
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
				Template: apiv1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
					Spec: apiv1.PodSpec{
						Containers: []apiv1.Container{
							{
								Name:  "analysis",
								Image: "img",
								Resources: apiv1.ResourceRequirements{
									Requests: apiv1.ResourceList{
										nvidiaGPUResource: resource.MustParse("1"),
									},
									Limits: apiv1.ResourceList{
										nvidiaGPUResource: resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
		},
		Service: &apiv1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "gpu-svc",
				Labels: map[string]string{constants.AnalysisIDLabel: "gpu-amd-test"},
			},
			Spec: apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
		},
	}

	body, err := json.Marshal(bundle)
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/analyses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = op.HandleLaunch(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rec.Code)

	// Verify the deployed deployment has AMD GPU resources, not NVIDIA.
	ctx := context.Background()
	dep, err := clientset.AppsV1().Deployments("vice-apps").Get(ctx, "gpu-dep", metav1.GetOptions{})
	require.NoError(t, err)

	reqs := dep.Spec.Template.Spec.Containers[0].Resources.Requests
	_, hasAMD := reqs[amdGPUResource]
	assert.True(t, hasAMD, "deployed deployment should have amd.com/gpu in requests")
	_, hasNvidia := reqs[nvidiaGPUResource]
	assert.False(t, hasNvidia, "deployed deployment should not have nvidia.com/gpu in requests")
}

func TestHandleLaunchFullBundle(t *testing.T) {
	op, clientset, gwClientset := newTestOperator(t, 10)

	analysisID := operatorclient.AnalysisID("full-bundle-test")
	labels := map[string]string{constants.AnalysisIDLabel: string(analysisID), constants.AppTypeLabel: "interactive", constants.UsernameLabel: "testuser"}
	port := gatewayv1.PortNumber(60000)

	bundle := operatorclient.AnalysisBundle{
		AnalysisID: analysisID,
		Deployment: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "dep", Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
				Template: apiv1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
					Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
				},
			},
		},
		Service: &apiv1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "svc", Labels: labels},
			Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
		},
		HTTPRoute: &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "vice-apps", Labels: labels},
			Spec: gatewayv1.HTTPRouteSpec{
				Hostnames: []gatewayv1.Hostname{"test.cyverse.run"},
				Rules: []gatewayv1.HTTPRouteRule{{
					BackendRefs: []gatewayv1.HTTPBackendRef{{
						BackendRef: gatewayv1.BackendRef{
							BackendObjectReference: gatewayv1.BackendObjectReference{
								Name: "svc",
								Port: &port,
							},
						},
					}},
				}},
			},
		},
		ConfigMaps: []*apiv1.ConfigMap{
			{ObjectMeta: metav1.ObjectMeta{Name: "cm-1", Labels: labels}},
		},
		PersistentVolumeClaims: []*apiv1.PersistentVolumeClaim{
			{ObjectMeta: metav1.ObjectMeta{Name: "pvc-1", Labels: labels}},
		},
	}

	body, err := json.Marshal(bundle)
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/analyses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = op.HandleLaunch(c)
	if err != nil {
		he, ok := err.(*echo.HTTPError)
		require.True(t, ok, "expected HTTPError, got %T: %v", err, err)
		require.Fail(t, "launch failed", "status=%d message=%v", he.Code, he.Message)
	}
	assert.Equal(t, http.StatusCreated, rec.Code)

	ctx := context.Background()

	// Verify all resource types were created.
	_, err = clientset.AppsV1().Deployments("vice-apps").Get(ctx, "dep", metav1.GetOptions{})
	assert.NoError(t, err, "deployment should exist")

	_, err = clientset.CoreV1().Services("vice-apps").Get(ctx, "svc", metav1.GetOptions{})
	assert.NoError(t, err, "service should exist")

	_, err = gwClientset.GatewayV1().HTTPRoutes("vice-apps").Get(ctx, "route", metav1.GetOptions{})
	assert.NoError(t, err, "httproute should exist")

	_, err = clientset.CoreV1().ConfigMaps("vice-apps").Get(ctx, "cm-1", metav1.GetOptions{})
	assert.NoError(t, err, "configmap should exist")

	_, err = clientset.CoreV1().PersistentVolumeClaims("vice-apps").Get(ctx, "pvc-1", metav1.GetOptions{})
	assert.NoError(t, err, "pvc should exist")
}
