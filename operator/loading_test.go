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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
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

// makeLoadingPageDeployment creates a Deployment with the labels needed by
// resolveSubdomain for loading page tests.
func makeLoadingPageDeployment(analysisID, subdomain, appName string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dep",
			Labels: map[string]string{
				"analysis-id": analysisID,
				"subdomain":   subdomain,
				"app-name":    appName,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
				Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
			},
		},
	}
}

func TestHandleLoadingPage(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		setup        func(t *testing.T, clientset *fake.Clientset)
		wantErrCode  int // non-zero: expect an HTTPError with this code
		wantContains []string
	}{
		{
			name: "known subdomain renders loading page",
			host: "a1234abcd.cyverse.run",
			setup: func(t *testing.T, clientset *fake.Clientset) {
				t.Helper()
				_, err := clientset.AppsV1().Deployments("vice-apps").Create(
					context.Background(),
					makeLoadingPageDeployment("loading-page-test", "a1234abcd", "JupyterLab"),
					metav1.CreateOptions{},
				)
				require.NoError(t, err)
			},
			wantContains: []string{"JupyterLab", "loading-page-test"},
		},
		{
			name:         "unknown subdomain serves waiting page",
			host:         "unknown.cyverse.run",
			wantContains: []string{"Waiting for Analysis", "window.location.reload()"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, clientset, _ := newTestOperator(t, 10)
			if tt.setup != nil {
				tt.setup(t, clientset)
			}

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := op.HandleLoadingPage(c)
			if tt.wantErrCode != 0 {
				require.Error(t, err)
				he, ok := err.(*echo.HTTPError)
				require.True(t, ok)
				assert.Equal(t, tt.wantErrCode, he.Code)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)
			body := rec.Body.String()
			for _, s := range tt.wantContains {
				assert.Contains(t, body, s)
			}
		})
	}
}

func TestHandleLoadingStatus(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		analysisID string
		subdomain  string
		setup      func(t *testing.T, clientset *fake.Clientset, gwClientset *gatewayfake.Clientset, analysisID string)
		wantReady  bool
		wantStage  string
		// verify is called after the handler succeeds to perform extra assertions.
		verify func(t *testing.T, gwClientset *gatewayfake.Clientset, analysisID string)
	}{
		{
			name:       "not-ready analysis returns deploying",
			host:       "b5678efgh.cyverse.run",
			analysisID: "status-test",
			subdomain:  "b5678efgh",
			setup: func(t *testing.T, clientset *fake.Clientset, _ *gatewayfake.Clientset, analysisID string) {
				t.Helper()
				_, err := clientset.AppsV1().Deployments("vice-apps").Create(
					context.Background(),
					makeLoadingPageDeployment(analysisID, "b5678efgh", "RStudio"),
					metav1.CreateOptions{},
				)
				require.NoError(t, err)
			},
			wantReady: false,
			wantStage: StageDeploying,
		},
		{
			name:       "ready analysis returns ready and swaps route",
			host:       "c9999xyz.cyverse.run",
			analysisID: "ready-status-test",
			subdomain:  "c9999xyz",
			setup: func(t *testing.T, clientset *fake.Clientset, gwClientset *gatewayfake.Clientset, analysisID string) {
				t.Helper()
				ctx := context.Background()

				dep := makeLoadingPageDeployment(analysisID, "c9999xyz", "JupyterLab")
				dep.Status.ReadyReplicas = 1
				_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, dep, metav1.CreateOptions{})
				require.NoError(t, err)

				_, err = clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "analysis-svc", Labels: map[string]string{"analysis-id": analysisID}},
					Spec: apiv1.ServiceSpec{Ports: []apiv1.ServicePort{
						{Name: "tcp-input", Port: 60001},
						{Name: "tcp-proxy", Port: 60000},
					}},
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

				// Create an HTTPRoute pointing at the loading page service.
				port := gatewayv1.PortNumber(80)
				_, err = gwClientset.GatewayV1().HTTPRoutes("vice-apps").Create(ctx, &gatewayv1.HTTPRoute{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-route",
						Labels: map[string]string{"analysis-id": analysisID},
					},
					Spec: gatewayv1.HTTPRouteSpec{
						Hostnames: []gatewayv1.Hostname{"c9999xyz.cyverse.run"},
						Rules: []gatewayv1.HTTPRouteRule{
							{
								BackendRefs: []gatewayv1.HTTPBackendRef{
									{
										BackendRef: gatewayv1.BackendRef{
											BackendObjectReference: gatewayv1.BackendObjectReference{
												Name: "vice-operator-loading",
												Port: &port,
											},
										},
									},
								},
							},
						},
					},
				}, metav1.CreateOptions{})
				require.NoError(t, err)
			},
			wantReady: true,
			wantStage: StageReady,
			verify: func(t *testing.T, gwClientset *gatewayfake.Clientset, analysisID string) {
				t.Helper()
				// Verify the HTTPRoute was swapped to the analysis service.
				routes, err := gwClientset.GatewayV1().HTTPRoutes("vice-apps").List(
					context.Background(), analysisLabelSelector(analysisID),
				)
				require.NoError(t, err)
				require.Len(t, routes.Items, 1)
				ref := routes.Items[0].Spec.Rules[0].BackendRefs[0]
				assert.Equal(t, gatewayv1.ObjectName("analysis-svc"), ref.Name)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, clientset, gwClientset := newTestOperator(t, 10)
			if tt.setup != nil {
				tt.setup(t, clientset, gwClientset, tt.analysisID)
			}

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/loading/status", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := op.HandleLoadingStatus(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)

			var resp LoadingStatusResponse
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, tt.wantReady, resp.Ready)
			assert.Equal(t, tt.wantStage, resp.Stage)

			if tt.verify != nil {
				tt.verify(t, gwClientset, tt.analysisID)
			}
		})
	}
}
