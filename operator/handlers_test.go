package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestOperator(t *testing.T, maxAnalyses int) (*Operator, *fake.Clientset) {
	t.Helper()
	clientset := fake.NewSimpleClientset()
	calc := NewCapacityCalculator(clientset, "vice-apps", maxAnalyses, "")
	cache := NewImageCacheManager(clientset, "vice-apps", "vice-image-pull-secret")
	op := NewOperator(clientset, nil, "vice-apps", RoutingNginx, "nginx", GPUVendorNvidia, calc, cache, "vice-operator-loading", 80, 600000)
	return op, clientset
}

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
						Labels: map[string]string{"analysis-id": "test-analysis-1"},
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
						Labels: map[string]string{"analysis-id": "test-analysis-1"},
					},
					Spec: apiv1.ServiceSpec{
						Ports: []apiv1.ServicePort{{Port: 80}},
					},
				},
				Ingress: &netv1.Ingress{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-ing",
						Labels: map[string]string{"analysis-id": "test-analysis-1"},
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
			},
			maxSlots: 1,
			setup: func(t *testing.T, cs *fake.Clientset) {
				t.Helper()
				// Pre-fill the single slot with an existing VICE deployment.
				_, err := cs.AppsV1().Deployments("vice-apps").Create(context.Background(), &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "existing-dep",
						Labels: map[string]string{"app-type": "interactive"},
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
			op, clientset := newTestOperator(t, tt.maxSlots)
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

				_, err = clientset.NetworkingV1().Ingresses("vice-apps").Get(ctx, "test-ing", metav1.GetOptions{})
				assert.NoError(t, err, "ingress should exist")
			}
		})
	}
}

// TestHandleLaunchGPUVendorAMD verifies that HandleLaunch rewrites NVIDIA GPU
// resources to AMD when the operator is configured with GPUVendorAMD.
func TestHandleLaunchGPUVendorAMD(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	calc := NewCapacityCalculator(clientset, "vice-apps", 10, "")
	cache := NewImageCacheManager(clientset, "vice-apps", "vice-image-pull-secret")
	op := NewOperator(clientset, nil, "vice-apps", RoutingNginx, "nginx", GPUVendorAMD, calc, cache, "vice-operator-loading", 80, 600000)

	bundle := operatorclient.AnalysisBundle{
		AnalysisID: "gpu-amd-test",
		Deployment: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "gpu-dep",
				Labels: map[string]string{"analysis-id": "gpu-amd-test"},
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
				Labels: map[string]string{"analysis-id": "gpu-amd-test"},
			},
			Spec: apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
		},
		Ingress: &netv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "gpu-ing",
				Labels: map[string]string{"analysis-id": "gpu-amd-test"},
			},
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

func TestHandleExit(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()

	// Create some resources with the analysis-id label.
	analysisID := "exit-test-1"
	labels := map[string]string{"analysis-id": analysisID}

	_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
				Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc1", Labels: labels},
		Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Call HandleExit.
	e := echo.New()
	req := httptest.NewRequest(http.MethodDelete, "/analyses/"+analysisID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err = op.HandleExit(c)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify resources were deleted.
	deps, err := clientset.AppsV1().Deployments("vice-apps").List(ctx, analysisLabelSelector(analysisID))
	assert.NoError(t, err)
	assert.Empty(t, deps.Items)

	svcs, err := clientset.CoreV1().Services("vice-apps").List(ctx, analysisLabelSelector(analysisID))
	assert.NoError(t, err)
	assert.Empty(t, svcs.Items)
}

func TestHandleSwapRoute(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()
	analysisID := "swap-route-test"
	labels := map[string]string{"analysis-id": analysisID}

	_, err := clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "analysis-svc", Labels: labels},
		Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	pathType := netv1.PathTypePrefix
	_, err = clientset.NetworkingV1().Ingresses("vice-apps").Create(ctx, &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ing", Labels: labels},
		Spec: netv1.IngressSpec{
			DefaultBackend: &netv1.IngressBackend{
				Service: &netv1.IngressServiceBackend{
					Name: "vice-operator-loading",
					Port: netv1.ServiceBackendPort{Number: 80},
				},
			},
			Rules: []netv1.IngressRule{
				{
					Host: "abc123.cyverse.run",
					IngressRuleValue: netv1.IngressRuleValue{
						HTTP: &netv1.HTTPIngressRuleValue{
							Paths: []netv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: netv1.IngressBackend{
										Service: &netv1.IngressServiceBackend{
											Name: "vice-operator-loading",
											Port: netv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/analyses/"+analysisID+"/swap-route", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err = op.HandleSwapRoute(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	ings, err := clientset.NetworkingV1().Ingresses("vice-apps").List(ctx, analysisLabelSelector(analysisID))
	require.NoError(t, err)
	require.Len(t, ings.Items, 1)
	assert.Equal(t, "analysis-svc", ings.Items[0].Spec.DefaultBackend.Service.Name)
}

func TestHandleURLReady(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()
	analysisID := "ready-test-1"
	labels := map[string]string{"analysis-id": analysisID}

	// No resources — should return not ready.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/analyses/"+analysisID+"/url-ready", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err := op.HandleURLReady(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp URLReadyResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.False(t, resp.Ready)

	// Create all resources with ready status.
	_, err = clientset.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep1", Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
				Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
			},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc1", Labels: labels},
		Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = clientset.NetworkingV1().Ingresses("vice-apps").Create(ctx, &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ing1", Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Now should be ready.
	req = httptest.NewRequest(http.MethodGet, "/analyses/"+analysisID+"/url-ready", nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err = op.HandleURLReady(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.Ready)
}
