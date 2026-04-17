package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestHandleExit(t *testing.T) {
	op, clientset, _ := newTestOperator(t, 10)
	ctx := context.Background()

	// Create some resources with the analysis-id label.
	analysisID := "exit-test-1"
	labels := map[string]string{constants.AnalysisIDLabel: analysisID}

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
	c.SetParamNames(constants.AnalysisIDLabel)
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
	labels := map[string]string{constants.AnalysisIDLabel: analysisID}

	// No resources — should return not ready.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/analyses/"+analysisID+"/url-ready", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames(constants.AnalysisIDLabel)
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
	c.SetParamNames(constants.AnalysisIDLabel)
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
	labels := map[string]string{constants.AnalysisIDLabel: analysisID}

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
	c.SetParamNames(constants.AnalysisIDLabel)
	c.SetParamValues(analysisID)

	err = op.HandleStatus(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp StatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, constants.AnalysisID(analysisID), resp.AnalysisID)
	assert.Len(t, resp.Deployments, 1)
	assert.Len(t, resp.Pods, 1)
	assert.Len(t, resp.Services, 1)
	assert.Len(t, resp.Routes, 1)
}

func TestHandleLogs(t *testing.T) {
	tests := []struct {
		name       string
		analysisID string
		query      string
		setup      func(t *testing.T, cs *fake.Clientset)
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "returns 400 for missing analysis-id",
			analysisID: "",
			query:      "",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "returns 404 when no pods exist",
			analysisID: "no-pods-analysis",
			query:      "",
			wantStatus: http.StatusNotFound,
			wantMsg:    "no pods found for analysis",
		},
		{
			// The fake clientset returns an empty stream for GetLogs().Stream(),
			// so the handler completes successfully with an empty log body. This
			// verifies that param parsing and pod lookup both succeed — the
			// container and tail-lines query params are processed before the
			// pod list call.
			name:       "parses query params correctly and returns 200 with pod present",
			analysisID: "logs-test-1",
			query:      "?container=mycontainer&tail-lines=100",
			setup: func(t *testing.T, cs *fake.Clientset) {
				t.Helper()
				_, err := cs.CoreV1().Pods("vice-apps").Create(
					context.Background(),
					&apiv1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pod-logs-1",
							Namespace: "vice-apps",
							Labels:    map[string]string{constants.AnalysisIDLabel: "logs-test-1"},
						},
					},
					metav1.CreateOptions{},
				)
				require.NoError(t, err)
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, clientset, _ := newTestOperator(t, 10)
			if tt.setup != nil {
				tt.setup(t, clientset)
			}

			url := "/analyses/" + tt.analysisID + "/logs" + tt.query
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames(constants.AnalysisIDLabel)
			c.SetParamValues(tt.analysisID)

			err := op.HandleLogs(c)

			if tt.wantStatus == http.StatusOK {
				// Successful path: handler writes directly to the response.
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, rec.Code)
			} else {
				// Error path: handler returns an *echo.HTTPError.
				he, ok := err.(*echo.HTTPError)
				require.True(t, ok, "expected *echo.HTTPError, got %T: %v", err, err)
				assert.Equal(t, tt.wantStatus, he.Code)
				if tt.wantMsg != "" {
					assert.Contains(t, fmt.Sprintf("%v", he.Message), tt.wantMsg)
				}
			}
		})
	}
}
