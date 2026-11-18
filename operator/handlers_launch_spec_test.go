package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func specTestSpec() operatorclient.VICESpec {
	return operatorclient.VICESpec{
		SpecVersion: operatorclient.CurrentVICESpecVersion,
		AnalysisID:  "spec-analysis-1",
		ExternalID:  "spec-external-1",
		JobName:     "My Spec Analysis",
		AppID:       "app-1",
		AppName:     "JupyterLab",
		UserID:      "user-1",
		Submitter:   "someuser",
		UserLoginIP: "10.0.0.1",
		Container: operatorclient.ContainerSpec{
			Image:      "cyverse/jupyter",
			Tag:        "latest",
			UID:        1000,
			Ports:      []int{8888},
			WorkingDir: "/de-app-work",
		},
		UserHome: "/cyverse/home/someuser",
	}
}

// configureBuild sets the build-path config the test operator needs; the shared
// newTestOperator helper leaves these zero because the legacy path doesn't use
// them.
func configureBuild(op *Operator) {
	op.porklockImage = "discoenv/vice-file-transfers"
	op.porklockTag = "latest"
	op.viceProxyImage = "harbor.cyverse.org/de/vice-proxy:latest"
	op.frontendBaseURL = "https://cyverse.run"
	op.irodsZone = "cyverse"
	op.gatewayProvider = "traefik"
	op.imagePullSecretName = "vice-image-pull-secret"
}

func postSpec(t *testing.T, op *Operator, spec operatorclient.VICESpec) (*httptest.ResponseRecorder, error) {
	t.Helper()
	body, err := json.Marshal(spec)
	require.NoError(t, err)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/analyses/spec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	return rec, op.HandleLaunchSpec(c)
}

func statusFromResult(t *testing.T, rec *httptest.ResponseRecorder, err error) int {
	t.Helper()
	if err != nil {
		he, ok := err.(*echo.HTTPError)
		require.True(t, ok, "expected *echo.HTTPError, got %T: %v", err, err)
		return he.Code
	}
	return rec.Code
}

// TestHandleLaunchSpecCreatesResources is the Phase 2 integration test: a
// VICESpec POSTed to the operator is built into cluster-correct objects and
// applied, including the per-analysis egress NetworkPolicy keyed on analysis-id.
func TestHandleLaunchSpecCreatesResources(t *testing.T) {
	op, clientset, gwClientset := newTestOperator(t, 10)
	configureBuild(op)

	rec, err := postSpec(t, op, specTestSpec())
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rec.Code)

	ctx := context.Background()
	ns := "vice-apps"

	dep, err := clientset.AppsV1().Deployments(ns).Get(ctx, "spec-external-1", metav1.GetOptions{})
	require.NoError(t, err, "deployment should exist")
	assert.Equal(t, "spec-analysis-1", dep.Labels[constants.AnalysisIDLabel])
	require.Len(t, dep.Spec.Template.Spec.ImagePullSecrets, 1, "image pull secret should be set from operator config")
	assert.Equal(t, "vice-image-pull-secret", dep.Spec.Template.Spec.ImagePullSecrets[0].Name)

	_, err = clientset.CoreV1().Services(ns).Get(ctx, "vice-spec-external-1", metav1.GetOptions{})
	assert.NoError(t, err, "service should exist")

	_, err = gwClientset.GatewayV1().HTTPRoutes(ns).Get(ctx, "spec-external-1", metav1.GetOptions{})
	assert.NoError(t, err, "httproute should exist")

	// Excludes, permissions, and input-path-list ConfigMaps.
	cms, err := clientset.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, cms.Items, 3, "excludes, permissions, and input-path-list ConfigMaps")

	_, err = clientset.CoreV1().PersistentVolumeClaims(ns).Get(ctx, "working-dir-spec-external-1", metav1.GetOptions{})
	assert.NoError(t, err, "working-dir PVC should exist")

	_, err = clientset.PolicyV1().PodDisruptionBudgets(ns).Get(ctx, "spec-external-1", metav1.GetOptions{})
	assert.NoError(t, err, "PDB should exist")

	np, err := clientset.NetworkingV1().NetworkPolicies(ns).Get(ctx, "vice-egress-spec-analysis-1", metav1.GetOptions{})
	require.NoError(t, err, "per-analysis egress NetworkPolicy should exist")
	assert.Equal(t, "spec-analysis-1", np.Labels[constants.AnalysisIDLabel])

	// No SensitiveEnvironment on this spec, so no sensitive-env Secret.
	secrets, err := clientset.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, secrets.Items, "no Secret should be created when the spec has no SensitiveEnvironment")
}

// TestHandleLaunchSpecCreatesSensitiveEnvSecret confirms a spec carrying
// SensitiveEnvironment gets its own Secret, that the analysis container
// consumes it via envFrom rather than Env, and that the value never appears
// anywhere in the Deployment object (e.g. "kubectl get deployment -o yaml").
func TestHandleLaunchSpecCreatesSensitiveEnvSecret(t *testing.T) {
	op, clientset, _ := newTestOperator(t, 10)
	configureBuild(op)

	spec := specTestSpec()
	spec.SensitiveEnvironment = map[string]string{"OPENAI_API_KEY": "sk-not-a-real-key"}

	rec, err := postSpec(t, op, spec)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rec.Code)

	ctx := context.Background()
	ns := "vice-apps"

	secret, err := clientset.CoreV1().Secrets(ns).Get(ctx, "sensitive-env-spec-external-1", metav1.GetOptions{})
	require.NoError(t, err, "sensitive-env secret should exist")
	assert.Equal(t, "spec-analysis-1", secret.Labels[constants.AnalysisIDLabel])
	assert.Equal(t, []byte("sk-not-a-real-key"), secret.Data["OPENAI_API_KEY"])

	dep, err := clientset.AppsV1().Deployments(ns).Get(ctx, "spec-external-1", metav1.GetOptions{})
	require.NoError(t, err)

	var analysis apiv1.Container
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == constants.AnalysisContainerName {
			analysis = c
		}
	}
	require.NotEmpty(t, analysis.Name, "analysis container should exist")

	require.Len(t, analysis.EnvFrom, 1)
	require.NotNil(t, analysis.EnvFrom[0].SecretRef)
	assert.Equal(t, "sensitive-env-spec-external-1", analysis.EnvFrom[0].SecretRef.Name)

	for _, e := range analysis.Env {
		assert.NotEqual(t, "OPENAI_API_KEY", e.Name,
			"the sensitive key must never appear on Env, where any read of the Deployment exposes it")
	}
}

// TestHandleLaunchSpecFailedApplyRemovesSensitiveEnvSecret confirms that when
// applying a bundle fails after the sensitive-env Secret has been created, the
// Secret is deleted rather than left in the namespace for an analysis that
// never started.
func TestHandleLaunchSpecFailedApplyRemovesSensitiveEnvSecret(t *testing.T) {
	op, clientset, _ := newTestOperator(t, 10)
	configureBuild(op)

	// Fail the Deployment create, which applyBundle runs after the Secret.
	clientset.PrependReactor("create", "deployments", func(_ ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("deployment create borked")
	})

	spec := specTestSpec()
	spec.SensitiveEnvironment = map[string]string{"OPENAI_API_KEY": "***"}

	rec, err := postSpec(t, op, spec)
	assert.Equal(t, http.StatusInternalServerError, statusFromResult(t, rec, err))

	_, err = clientset.CoreV1().Secrets("vice-apps").Get(context.Background(), "sensitive-env-spec-external-1", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "sensitive-env Secret should be removed after a failed launch, got err=%v", err)
}

func TestHandleLaunchSpecValidation(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*operatorclient.VICESpec)
		maxSlots   int
		setup      func(t *testing.T, op *Operator)
		wantStatus int
	}{
		{name: "valid", mutate: func(*operatorclient.VICESpec) {}, maxSlots: 10, wantStatus: http.StatusCreated},
		{name: "missing image", mutate: func(s *operatorclient.VICESpec) { s.Container.Image = "" }, maxSlots: 10, wantStatus: http.StatusBadRequest},
		{name: "missing analysisID", mutate: func(s *operatorclient.VICESpec) { s.AnalysisID = "" }, maxSlots: 10, wantStatus: http.StatusBadRequest},
		{
			name:       "spec version too new",
			mutate:     func(s *operatorclient.VICESpec) { s.SpecVersion = operatorclient.CurrentVICESpecVersion + 1 },
			maxSlots:   10,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:     "at capacity",
			mutate:   func(*operatorclient.VICESpec) {},
			maxSlots: 1,
			setup: func(t *testing.T, op *Operator) {
				t.Helper()
				prefillSlot(t, op)
			},
			wantStatus: http.StatusConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, _, _ := newTestOperator(t, tt.maxSlots)
			configureBuild(op)
			if tt.setup != nil {
				tt.setup(t, op)
			}
			spec := specTestSpec()
			tt.mutate(&spec)
			rec, err := postSpec(t, op, spec)
			assert.Equal(t, tt.wantStatus, statusFromResult(t, rec, err))
		})
	}
}

// TestHandleLaunchSpecDisabled confirms the spec endpoint refuses launches when
// spec launch is disabled, returning a transient 503 so a stray spec is retried
// elsewhere rather than failing outright.
func TestHandleLaunchSpecDisabled(t *testing.T) {
	op, clientset, _ := newTestOperator(t, 10)
	configureBuild(op)
	op.disableSpecLaunch = true

	rec, err := postSpec(t, op, specTestSpec())
	assert.Equal(t, http.StatusServiceUnavailable, statusFromResult(t, rec, err))

	// Nothing should have been created.
	deps, listErr := clientset.AppsV1().Deployments("vice-apps").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Empty(t, deps.Items, "no resources should be created when spec launch is disabled")
}

// prefillSlot occupies the operator's single capacity slot with an existing
// VICE deployment.
func prefillSlot(t *testing.T, op *Operator) {
	t.Helper()
	dep := &appsv1.Deployment{
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
	}
	_, err := op.clientset.AppsV1().Deployments("vice-apps").Create(context.Background(), dep, metav1.CreateOptions{})
	require.NoError(t, err)
}
