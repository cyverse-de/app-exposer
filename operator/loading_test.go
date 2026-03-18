package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestComputeStage(t *testing.T) {
	tests := []struct {
		name      string
		pods      []apiv1.Pod
		depReady  bool
		svcExists bool
		wantStage string
		wantError string
	}{
		{
			name:      "no pods returns deploying",
			pods:      nil,
			depReady:  false,
			svcExists: false,
			wantStage: StageDeploying,
		},
		{
			name: "pending pods returns deploying",
			pods: []apiv1.Pod{
				{Status: apiv1.PodStatus{Phase: apiv1.PodPending}},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageDeploying,
		},
		{
			name: "running pods not ready returns starting",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						ContainerStatuses: []apiv1.ContainerStatus{
							{Name: "analysis", Ready: false},
						},
					},
				},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageStarting,
		},
		{
			name: "all ready returns almost-ready when dep not ready",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						Conditions: []apiv1.PodCondition{
							{Type: apiv1.PodReady, Status: apiv1.ConditionTrue},
						},
						ContainerStatuses: []apiv1.ContainerStatus{
							{Name: "analysis", Ready: true},
						},
					},
				},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageAlmostReady,
		},
		{
			name: "dep ready and svc exists returns ready",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						Conditions: []apiv1.PodCondition{
							{Type: apiv1.PodReady, Status: apiv1.ConditionTrue},
						},
						ContainerStatuses: []apiv1.ContainerStatus{
							{Name: "analysis", Ready: true},
						},
					},
				},
			},
			depReady:  true,
			svcExists: true,
			wantStage: StageReady,
		},
		{
			name: "crashloopbackoff returns error",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						ContainerStatuses: []apiv1.ContainerStatus{
							{
								Name:         "analysis",
								Ready:        false,
								RestartCount: 3,
								State: apiv1.ContainerState{
									Waiting: &apiv1.ContainerStateWaiting{
										Reason: "CrashLoopBackOff",
									},
								},
							},
						},
					},
				},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageError,
			wantError: "container \"analysis\" is in CrashLoopBackOff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stage, errMsg := computeStage(tt.pods, tt.depReady, tt.svcExists)
			assert.Equal(t, tt.wantStage, stage)
			if tt.wantError != "" {
				assert.Contains(t, errMsg, tt.wantError)
			}
		})
	}
}

func TestHandleLoadingPage(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()
	analysisID := "loading-page-test"

	_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dep",
			Labels: map[string]string{
				"analysis-id": analysisID,
				"subdomain":   "a1234abcd",
				"app-name":    "JupyterLab",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
				Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "a1234abcd.cyverse.run"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = op.HandleLoadingPage(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "JupyterLab")
	assert.Contains(t, rec.Body.String(), analysisID)
}

func TestHandleLoadingPageUnknownSubdomain(t *testing.T) {
	op, _ := newTestOperator(t, 10)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "unknown.cyverse.run"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := op.HandleLoadingPage(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, he.Code)
}

func TestHandleLoadingStatus(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()
	analysisID := "status-test"

	_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dep1",
			Labels: map[string]string{
				"analysis-id": analysisID,
				"subdomain":   "b5678efgh",
				"app-name":    "RStudio",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
				Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/loading/status", nil)
	req.Host = "b5678efgh.cyverse.run"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = op.HandleLoadingStatus(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp LoadingStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.False(t, resp.Ready)
	assert.Equal(t, StageDeploying, resp.Stage)
}

func TestHandleLoadingStatusReady(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()
	analysisID := "ready-status-test"

	_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dep1",
			Labels: map[string]string{
				"analysis-id": analysisID,
				"subdomain":   "c9999xyz",
				"app-name":    "JupyterLab",
			},
		},
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
		ObjectMeta: metav1.ObjectMeta{Name: "analysis-svc", Labels: map[string]string{"analysis-id": analysisID}},
		Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = clientset.CoreV1().Pods("vice-apps").Create(ctx, &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pod1",
			Labels: map[string]string{"analysis-id": analysisID},
		},
		Status: apiv1.PodStatus{
			Phase: apiv1.PodRunning,
			Conditions: []apiv1.PodCondition{
				{Type: apiv1.PodReady, Status: apiv1.ConditionTrue},
			},
			ContainerStatuses: []apiv1.ContainerStatus{
				{Name: "analysis", Ready: true, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	pathType := netv1.PathTypePrefix
	_, err = clientset.NetworkingV1().Ingresses("vice-apps").Create(ctx, &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ing", Labels: map[string]string{"analysis-id": analysisID}},
		Spec: netv1.IngressSpec{
			DefaultBackend: &netv1.IngressBackend{
				Service: &netv1.IngressServiceBackend{
					Name: "vice-operator-loading",
					Port: netv1.ServiceBackendPort{Number: 80},
				},
			},
			Rules: []netv1.IngressRule{{
				Host: "c9999xyz.cyverse.run",
				IngressRuleValue: netv1.IngressRuleValue{
					HTTP: &netv1.HTTPIngressRuleValue{
						Paths: []netv1.HTTPIngressPath{{
							Path: "/", PathType: &pathType,
							Backend: netv1.IngressBackend{
								Service: &netv1.IngressServiceBackend{
									Name: "vice-operator-loading",
									Port: netv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/loading/status", nil)
	req.Host = "c9999xyz.cyverse.run"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = op.HandleLoadingStatus(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp LoadingStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.Ready)
	assert.Equal(t, StageReady, resp.Stage)

	// Verify the ingress was swapped to the analysis service.
	ings, err := clientset.NetworkingV1().Ingresses("vice-apps").List(ctx, analysisLabelSelector(analysisID))
	require.NoError(t, err)
	require.Len(t, ings.Items, 1)
	assert.Equal(t, "analysis-svc", ings.Items[0].Spec.DefaultBackend.Service.Name)
}
