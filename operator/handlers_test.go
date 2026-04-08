package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	gatewayfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

type mockHTTPClient struct {
	DoFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.DoFunc(req)
}

// makeGatewayPort converts an int32 to a gatewayv1.PortNumber pointer.
func makeGatewayPort(port int32) *gatewayv1.PortNumber {
	p := gatewayv1.PortNumber(port)
	return &p
}

// makeTestHTTPRouteWithLabels builds a minimal HTTPRoute with custom labels.
func makeTestHTTPRouteWithLabels(labels map[string]string, port *gatewayv1.PortNumber) *gatewayv1.HTTPRoute {
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "vice-apps",
			Labels:    labels,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"test.localhost"},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "test-svc",
									Port: port,
								},
							},
						},
					},
				},
			},
		},
	}
}

// newTestOperator creates an Operator wired to fake K8s clients for use in tests.
// vendor defaults to GPUVendorNvidia; pass GPUVendorAMD where needed.
func newTestOperator(t *testing.T, maxAnalyses int, vendor ...GPUVendor) (*Operator, *fake.Clientset, *gatewayfake.Clientset) {
	t.Helper()
	gpuVendor := GPUVendorNvidia
	if len(vendor) > 0 {
		gpuVendor = vendor[0]
	}
	clientset := fake.NewSimpleClientset()
	gwClientset := gatewayfake.NewSimpleClientset()
	calc := NewCapacityCalculator(clientset, "vice-apps", maxAnalyses, "")
	cache := NewImageCacheManager(clientset, "vice-apps", "vice-image-pull-secret")
	op := NewOperator(clientset, gwClientset.GatewayV1(), "vice-apps", "vice-apps", "vice", gpuVendor, calc, cache, "vice-operator-loading", 80, 600000, "", "cluster-config-secret", NetworkPolicyConfig{})
	return op, clientset, gwClientset
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
			},
			maxSlots:   10,
			wantStatus: http.StatusCreated,
		},
		{
			name: "launch at capacity returns 409",
			bundle: operatorclient.AnalysisBundle{
				AnalysisID: "test-analysis-2",
				Deployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "dep-2", Labels: map[string]string{"analysis-id": "test-analysis-2"}},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test2"}},
						Template: apiv1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test2"}},
							Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
						},
					},
				},
				Service: &apiv1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-2", Labels: map[string]string{"analysis-id": "test-analysis-2"}},
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

				// HandleLaunch now creates a per-analysis egress NetworkPolicy.
				_, err = clientset.NetworkingV1().NetworkPolicies("vice-apps").Get(ctx, "vice-egress-"+tt.bundle.AnalysisID, metav1.GetOptions{})
				assert.NoError(t, err, "per-analysis egress NetworkPolicy should exist")
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
	op, clientset, _ := newTestOperator(t, 10)
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

func TestHandleURLReady(t *testing.T) {
	op, clientset, gwClientset := newTestOperator(t, 10)
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

	var resp operatorclient.URLReadyResponse
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

	// Create the HTTPRoute via the gateway fake client.
	gwPort := makeGatewayPort(int32(80))
	_, err = gwClientset.GatewayV1().HTTPRoutes("vice-apps").Create(ctx, makeTestHTTPRouteWithLabels(labels, gwPort), metav1.CreateOptions{})
	require.NoError(t, err)

	// Mock the HTTP client for the vice-proxy check.
	op.httpClient = &mockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"url": "http://test.localhost"}`)),
			}, nil
		},
	}

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

func TestHandleStatus(t *testing.T) {
	analysisID := "status-test-1"
	labels := map[string]string{"analysis-id": analysisID}

	op, clientset, gwClientset := newTestOperator(t, 10)
	ctx := context.Background()

	// Create resources for the analysis.
	_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep-1", Labels: labels},
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

	_, err = clientset.CoreV1().Pods("vice-apps").Create(ctx, &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Labels: labels},
		Status:     apiv1.PodStatus{Phase: apiv1.PodRunning},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-1", Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = gwClientset.GatewayV1().HTTPRoutes("vice-apps").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route-1", Namespace: "vice-apps", Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Call HandleStatus.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err = op.HandleStatus(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp StatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, analysisID, resp.AnalysisID)
	assert.Len(t, resp.Deployments, 1)
	assert.Len(t, resp.Pods, 1)
	assert.Len(t, resp.Services, 1)
	assert.Len(t, resp.Routes, 1)
}

func TestHandleGetPermissions(t *testing.T) {
	analysisID := "perms-test-1"
	labels := map[string]string{"analysis-id": analysisID}

	op, clientset, _ := newTestOperator(t, 10)
	ctx := context.Background()

	// Create a permissions ConfigMap.
	_, err := clientset.CoreV1().ConfigMaps("vice-apps").Create(ctx, &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "permissions-" + analysisID,
			Labels: labels,
		},
		Data: map[string]string{
			"allowed-users": "user1@example.org\nuser2@example.org\n",
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Call HandleGetPermissions.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err = op.HandleGetPermissions(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp PermissionsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, []string{"user1@example.org", "user2@example.org"}, resp.AllowedUsers)
}

func TestHandleGetPermissionsNotFound(t *testing.T) {
	op, _, _ := newTestOperator(t, 10)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues("nonexistent")

	err := op.HandleGetPermissions(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, he.Code)
}

func TestHandleUpdatePermissions(t *testing.T) {
	analysisID := "perms-update-1"
	labels := map[string]string{"analysis-id": analysisID}

	op, clientset, _ := newTestOperator(t, 10)
	ctx := context.Background()

	// Create existing permissions ConfigMap.
	_, err := clientset.CoreV1().ConfigMaps("vice-apps").Create(ctx, &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "permissions-" + analysisID,
			Labels: labels,
		},
		Data: map[string]string{"allowed-users": "old-user\n"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Call HandleUpdatePermissions with new users.
	body, _ := json.Marshal(UpdatePermissionsRequest{AllowedUsers: []string{"new-user1", "new-user2"}})
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err = op.HandleUpdatePermissions(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify the ConfigMap was updated.
	cm, err := clientset.CoreV1().ConfigMaps("vice-apps").Get(ctx, "permissions-"+analysisID, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "new-user1\nnew-user2\n", cm.Data["allowed-users"])
}

func TestHandleUpdatePermissionsEmptyUsers(t *testing.T) {
	op, _, _ := newTestOperator(t, 10)

	body, _ := json.Marshal(UpdatePermissionsRequest{AllowedUsers: []string{}})
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues("any-id")

	err := op.HandleUpdatePermissions(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, he.Code)
}

func TestHandleLaunchFullBundle(t *testing.T) {
	op, clientset, gwClientset := newTestOperator(t, 10)

	analysisID := "full-bundle-test"
	labels := map[string]string{"analysis-id": analysisID, "app-type": "interactive", "username": "testuser"}
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

func TestAnalysisBundleValidate(t *testing.T) {
	tests := []struct {
		name    string
		bundle  operatorclient.AnalysisBundle
		wantErr bool
	}{
		{
			name: "valid bundle",
			bundle: operatorclient.AnalysisBundle{
				AnalysisID: "test",
				Deployment: &appsv1.Deployment{},
				Service:    &apiv1.Service{},
			},
			wantErr: false,
		},
		{name: "missing analysis ID", bundle: operatorclient.AnalysisBundle{Deployment: &appsv1.Deployment{}, Service: &apiv1.Service{}}, wantErr: true},
		{name: "missing deployment", bundle: operatorclient.AnalysisBundle{AnalysisID: "test", Service: &apiv1.Service{}}, wantErr: true},
		{name: "missing service", bundle: operatorclient.AnalysisBundle{AnalysisID: "test", Deployment: &appsv1.Deployment{}}, wantErr: true},
		{name: "empty bundle", bundle: operatorclient.AnalysisBundle{}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.bundle.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
