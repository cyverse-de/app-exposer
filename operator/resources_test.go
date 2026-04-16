package operator

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ktesting "k8s.io/client-go/testing"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// seedAnalysisResources registers a mix of resource types carrying the
// analysis-id label so deleteAnalysisResources has something to iterate
// across every resource kind the function cares about.
func seedAnalysisResources(t *testing.T, op *Operator, analysisID string) {
	t.Helper()
	labels := map[string]string{"analysis-id": analysisID}
	ctx := context.Background()

	_, err := op.clientset.CoreV1().Services(op.namespace).Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-" + analysisID, Namespace: op.namespace, Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = op.clientset.AppsV1().Deployments(op.namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep-" + analysisID, Namespace: op.namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": analysisID}},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": analysisID}},
				Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = op.clientset.CoreV1().ConfigMaps(op.namespace).Create(ctx, &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm-" + analysisID, Namespace: op.namespace, Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = op.clientset.NetworkingV1().NetworkPolicies(op.namespace).Create(ctx, &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "np-" + analysisID, Namespace: op.namespace, Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = op.gatewayClient.HTTPRoutes(op.namespace).Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route-" + analysisID, Namespace: op.namespace, Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}

func TestDeleteAnalysisResourcesHappyPath(t *testing.T) {
	op, clientset, gwClientset := newTestOperator(t, 10)
	seedAnalysisResources(t, op, "del-ok")

	require.NoError(t, op.deleteAnalysisResources(context.Background(), "del-ok"))

	// Every seeded resource should be gone.
	ctx := context.Background()
	selector := analysisLabelSelector("del-ok")
	svcs, err := clientset.CoreV1().Services(op.namespace).List(ctx, selector)
	require.NoError(t, err)
	assert.Empty(t, svcs.Items, "services should be deleted")

	deps, err := clientset.AppsV1().Deployments(op.namespace).List(ctx, selector)
	require.NoError(t, err)
	assert.Empty(t, deps.Items, "deployments should be deleted")

	cms, err := clientset.CoreV1().ConfigMaps(op.namespace).List(ctx, selector)
	require.NoError(t, err)
	assert.Empty(t, cms.Items, "configmaps should be deleted")

	nps, err := clientset.NetworkingV1().NetworkPolicies(op.namespace).List(ctx, selector)
	require.NoError(t, err)
	assert.Empty(t, nps.Items, "networkpolicies should be deleted")

	routes, err := gwClientset.GatewayV1().HTTPRoutes(op.namespace).List(ctx, selector)
	require.NoError(t, err)
	assert.Empty(t, routes.Items, "httproutes should be deleted")
}

func TestDeleteAnalysisResourcesMissingIsNotAnError(t *testing.T) {
	// Nothing seeded. The function should succeed because it's
	// idempotent — missing resources are a no-op, and the per-item
	// deleteFn short-circuits on apierrors.IsNotFound.
	op, _, _ := newTestOperator(t, 10)
	require.NoError(t, op.deleteAnalysisResources(context.Background(), "del-empty"))
}

// TestDeleteAnalysisResourcesAggregatesErrors is the core partial-failure
// test: inject delete errors for Services and ConfigMaps and verify that
// (a) the function doesn't short-circuit — deployments and HTTPRoutes
// still get deleted, and (b) the returned error carries both of the
// injected errors via errors.Join so the caller can see the full
// picture rather than just the first failure.
func TestDeleteAnalysisResourcesAggregatesErrors(t *testing.T) {
	op, clientset, gwClientset := newTestOperator(t, 10)
	seedAnalysisResources(t, op, "del-partial")

	svcErr := errors.New("service delete borked")
	cmErr := errors.New("configmap delete borked")

	clientset.PrependReactor("delete", "services", func(_ ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, svcErr
	})
	clientset.PrependReactor("delete", "configmaps", func(_ ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, cmErr
	})

	err := op.deleteAnalysisResources(context.Background(), "del-partial")
	require.Error(t, err)
	// Both injected errors must be reachable through the returned error —
	// the partial-failure contract is that deleteAnalysisResources
	// surfaces everything rather than stopping at the first failure.
	assert.True(t, errors.Is(err, svcErr), "wrapped error should include svcErr")
	assert.True(t, errors.Is(err, cmErr), "wrapped error should include cmErr")

	// Deployments and HTTPRoutes (which are iterated AFTER the failing
	// resource types in deleteAnalysisResources) must still have been
	// deleted — a short-circuit would leak them.
	ctx := context.Background()
	selector := analysisLabelSelector("del-partial")
	deps, err := clientset.AppsV1().Deployments(op.namespace).List(ctx, selector)
	require.NoError(t, err)
	assert.Empty(t, deps.Items, "deployments must be deleted even when earlier deletes fail")

	routes, err := gwClientset.GatewayV1().HTTPRoutes(op.namespace).List(ctx, selector)
	require.NoError(t, err)
	assert.Empty(t, routes.Items, "httproutes must be deleted even when earlier deletes fail")

	nps, err := clientset.NetworkingV1().NetworkPolicies(op.namespace).List(ctx, selector)
	require.NoError(t, err)
	assert.Empty(t, nps.Items, "networkpolicies must be deleted even when earlier deletes fail")
}

func TestDeleteAnalysisResourcesIgnores404(t *testing.T) {
	// A 404 on an individual delete should not be treated as an error —
	// the operation is idempotent by design, so a resource that was
	// removed between List and Delete (e.g. by a concurrent cleanup)
	// must not fail the caller.
	op, clientset, _ := newTestOperator(t, 10)
	seedAnalysisResources(t, op, "del-404")

	clientset.PrependReactor("delete", "services", func(action ktesting.Action) (bool, runtime.Object, error) {
		delAction, ok := action.(ktesting.DeleteAction)
		if !ok {
			return false, nil, nil
		}
		gr := schema.GroupResource{Group: "", Resource: "services"}
		return true, nil, apierrors.NewNotFound(gr, delAction.GetName())
	})

	err := op.deleteAnalysisResources(context.Background(), "del-404")
	require.NoError(t, err, "404 on delete must be swallowed; got: %v", err)
}

// Silence unused-import guard for fmt in case a future edit to this
// file no longer needs it. Keeps builds green on small tweaks.
var _ = fmt.Sprintf
