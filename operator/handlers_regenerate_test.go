package operator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestHandleRegenerateNetworkPolicies(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, cs *fake.Clientset)
		wantStatus  int
		wantUpdated int
		wantErrors  int
	}{
		{
			name: "regenerates policies for running analyses",
			setup: func(t *testing.T, cs *fake.Clientset) {
				t.Helper()
				ctx := context.Background()
				for _, id := range []string{"regen-analysis-1", "regen-analysis-2"} {
					_, err := cs.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "dep-" + id,
							Namespace: "vice-apps",
							Labels: map[string]string{
								constants.AppTypeLabel:    "interactive",
								constants.AnalysisIDLabel: id,
							},
						},
						Spec: appsv1.DeploymentSpec{
							Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": id}},
							Template: apiv1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": id}},
								Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
							},
						},
					}, metav1.CreateOptions{})
					require.NoError(t, err)
				}
			},
			wantStatus:  http.StatusOK,
			wantUpdated: 2,
			wantErrors:  0,
		},
		{
			name: "skips deployments without analysis-id label",
			setup: func(t *testing.T, cs *fake.Clientset) {
				t.Helper()
				ctx := context.Background()

				// Deployment with app-type but no analysis-id — should be skipped.
				_, err := cs.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep-no-analysis-id",
						Namespace: "vice-apps",
						Labels:    map[string]string{constants.AppTypeLabel: "interactive"},
					},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "no-id"}},
						Template: apiv1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "no-id"}},
							Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
						},
					},
				}, metav1.CreateOptions{})
				require.NoError(t, err)

				// Deployment with both labels — should be processed.
				_, err = cs.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep-with-analysis-id",
						Namespace: "vice-apps",
						Labels: map[string]string{
							constants.AppTypeLabel:    "interactive",
							constants.AnalysisIDLabel: "skip-test-analysis",
						},
					},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "with-id"}},
						Template: apiv1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "with-id"}},
							Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
						},
					},
				}, metav1.CreateOptions{})
				require.NoError(t, err)
			},
			wantStatus:  http.StatusOK,
			wantUpdated: 1,
			wantErrors:  0,
		},
		{
			name:        "handles no deployments",
			setup:       nil,
			wantStatus:  http.StatusOK,
			wantUpdated: 0,
			wantErrors:  0,
		},
		{
			// When a NetworkPolicy upsert fails for any analysis, the
			// handler should keep processing the rest but return 207
			// Multi-Status so automation can detect partial success.
			name: "partial failure returns 207 Multi-Status",
			setup: func(t *testing.T, cs *fake.Clientset) {
				t.Helper()
				ctx := context.Background()
				for _, id := range []string{"ok-analysis", "fail-analysis"} {
					_, err := cs.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "dep-" + id,
							Namespace: "vice-apps",
							Labels: map[string]string{
								constants.AppTypeLabel:    "interactive",
								constants.AnalysisIDLabel: id,
							},
						},
						Spec: appsv1.DeploymentSpec{
							Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": id}},
							Template: apiv1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": id}},
								Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
							},
						},
					}, metav1.CreateOptions{})
					require.NoError(t, err)
				}
				// Reject create attempts targeting the specific failing
				// analysis's NetworkPolicy so we can assert 207 on a
				// real partial outcome.
				cs.PrependReactor("create", "networkpolicies", func(action ktesting.Action) (bool, runtime.Object, error) {
					createAction, ok := action.(ktesting.CreateAction)
					if !ok {
						return false, nil, nil
					}
					obj, ok := createAction.GetObject().(metav1.Object)
					if !ok {
						return false, nil, nil
					}
					if obj.GetName() == "vice-egress-fail-analysis" {
						return true, nil, errors.New("injected failure")
					}
					return false, nil, nil
				})
			},
			wantStatus:  http.StatusMultiStatus,
			wantUpdated: 1,
			wantErrors:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, clientset, _ := newTestOperator(t, 10)
			if tt.setup != nil {
				tt.setup(t, clientset)
			}

			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/regenerate-network-policies", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := op.HandleRegenerateNetworkPolicies(c)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, rec.Code, "status code mismatch")

			var resp RegenerateResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, tt.wantUpdated, resp.Updated, "updated count mismatch")
			assert.Len(t, resp.Errors, tt.wantErrors, "errors count mismatch")

			// For the partial-failure case we only verify the response
			// shape and status code — the body-level presence check is
			// skipped because the injected reactor deliberately prevents
			// creation of one of the NetworkPolicies.
			if tt.wantErrors > 0 {
				return
			}

			// Verify NetworkPolicies were created for each deployment that had
			// a valid analysis-id label.
			ctx := context.Background()
			deps, err := clientset.AppsV1().Deployments("vice-apps").List(ctx, metav1.ListOptions{})
			require.NoError(t, err)
			for _, dep := range deps.Items {
				analysisID := dep.Labels[constants.AnalysisIDLabel]
				if analysisID == "" {
					continue
				}
				npName := "vice-egress-" + analysisID
				_, npErr := clientset.NetworkingV1().NetworkPolicies("vice-apps").Get(ctx, npName, metav1.GetOptions{})
				assert.NoError(t, npErr, "NetworkPolicy %q should exist for analysis %q", npName, analysisID)
			}
		})
	}
}
