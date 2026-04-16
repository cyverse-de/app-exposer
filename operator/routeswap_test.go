package operator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ktesting "k8s.io/client-go/testing"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestSwapRoute(t *testing.T) {
	analysisID := "swap-test-1"
	labels := map[string]string{"analysis-id": analysisID}
	targetSvcName := "analysis-svc"

	ctx := context.Background()
	op, clientset, gwClientset := newTestOperator(t, 10)

	// Create the target analysis service with the expected port names.
	_, err := clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: targetSvcName, Labels: labels},
		Spec: apiv1.ServiceSpec{Ports: []apiv1.ServicePort{
			{Name: "tcp-input", Port: 60001},
			{Name: "tcp-proxy", Port: 60000},
		}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create an HTTPRoute pointing at the loading page service.
	port := gatewayv1.PortNumber(80)
	_, err = gwClientset.GatewayV1().HTTPRoutes("vice-apps").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "vice-apps", Labels: labels},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"abc123.localhost"},
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

	err = op.SwapRoute(ctx, analysisID)
	require.NoError(t, err)

	// Verify the HTTPRoute was swapped to the analysis service.
	routes, err := gwClientset.GatewayV1().HTTPRoutes("vice-apps").List(ctx, analysisLabelSelector(analysisID))
	require.NoError(t, err)
	require.Len(t, routes.Items, 1)
	ref := routes.Items[0].Spec.Rules[0].BackendRefs[0]
	assert.Equal(t, gatewayv1.ObjectName(targetSvcName), ref.Name)
	expectedPort := gatewayv1.PortNumber(60000)
	assert.Equal(t, &expectedPort, ref.Port, "should route to vice-proxy port, not file transfers")
}

// TestSwapRouteConflictAlreadySwapped covers the concurrent-tab scenario:
// two loading-page polls call SwapRoute at the same time, the first one
// wins and completes the update, the second hits a 409 Conflict. The
// conflict handler must re-read the route, confirm it already points at
// the analysis service, and return nil — otherwise a second open tab
// would see a spurious "route swap failed" error on a legitimately
// swapped route.
func TestSwapRouteConflictAlreadySwapped(t *testing.T) {
	analysisID := "conflict-test-1"
	labels := map[string]string{"analysis-id": analysisID}
	targetSvcName := "analysis-svc"

	ctx := context.Background()
	op, clientset, gwClientset := newTestOperator(t, 10)

	_, err := clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: targetSvcName, Labels: labels},
		Spec: apiv1.ServiceSpec{Ports: []apiv1.ServicePort{
			{Name: "tcp-input", Port: 60001},
			{Name: "tcp-proxy", Port: 60000},
		}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	preSwapPort := gatewayv1.PortNumber(80)
	_, err = gwClientset.GatewayV1().HTTPRoutes("vice-apps").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "vice-apps", Labels: labels},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"abc123.localhost"},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "vice-operator-loading",
									Port: &preSwapPort,
								},
							},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Simulate a winning concurrent request: before SwapRoute's Update
	// gets executed, overwrite the stored route so it already points at
	// the analysis service, then reject the Update with 409 Conflict.
	// The conflict handler should Get the route, see it's already in the
	// desired state, and swallow the conflict.
	httpRouteGVR := schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"}
	targetPort := gatewayv1.PortNumber(60000)
	targetName := gatewayv1.ObjectName(targetSvcName)
	var conflictCount int
	gwClientset.PrependReactor("update", "httproutes", func(action ktesting.Action) (bool, runtime.Object, error) {
		conflictCount++
		// Directly mutate the stored route to the already-swapped state
		// so the follow-up Get call inside routeAlreadySwapped sees the
		// winning request's result.
		updated := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "vice-apps", Labels: labels},
			Spec: gatewayv1.HTTPRouteSpec{
				Hostnames: []gatewayv1.Hostname{"abc123.localhost"},
				Rules: []gatewayv1.HTTPRouteRule{
					{
						BackendRefs: []gatewayv1.HTTPBackendRef{
							{
								BackendRef: gatewayv1.BackendRef{
									BackendObjectReference: gatewayv1.BackendObjectReference{
										Name: targetName,
										Port: &targetPort,
									},
								},
							},
						},
					},
				},
			},
		}
		if err := gwClientset.Tracker().Update(httpRouteGVR.WithVersion("v1"), updated, "vice-apps"); err != nil {
			return true, nil, err
		}
		return true, nil, apierrors.NewConflict(httpRouteGVR, "test-route", nil)
	})

	err = op.SwapRoute(ctx, analysisID)
	require.NoError(t, err, "SwapRoute must swallow a 409 when the route is already swapped")
	assert.Equal(t, 1, conflictCount, "Update should have been attempted once and hit the injected conflict")

	// Verify the tracker state matches what the winning request would have set.
	routes, err := gwClientset.GatewayV1().HTTPRoutes("vice-apps").List(ctx, analysisLabelSelector(analysisID))
	require.NoError(t, err)
	require.Len(t, routes.Items, 1)
	ref := routes.Items[0].Spec.Rules[0].BackendRefs[0]
	assert.Equal(t, targetName, ref.Name)
	assert.Equal(t, &targetPort, ref.Port)
}

// TestSwapRouteConflictNotYetSwapped covers the other branch of the
// conflict handler: a 409 happens but the route is NOT yet in the
// target state (e.g. a concurrent edit from an unrelated actor). In
// that case SwapRoute must surface the error rather than pretend the
// swap succeeded.
func TestSwapRouteConflictNotYetSwapped(t *testing.T) {
	analysisID := "conflict-test-2"
	labels := map[string]string{"analysis-id": analysisID}
	targetSvcName := "analysis-svc"

	ctx := context.Background()
	op, clientset, gwClientset := newTestOperator(t, 10)

	_, err := clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: targetSvcName, Labels: labels},
		Spec: apiv1.ServiceSpec{Ports: []apiv1.ServicePort{
			{Name: "tcp-input", Port: 60001},
			{Name: "tcp-proxy", Port: 60000},
		}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	preSwapPort := gatewayv1.PortNumber(80)
	_, err = gwClientset.GatewayV1().HTTPRoutes("vice-apps").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "vice-apps", Labels: labels},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"abc123.localhost"},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "vice-operator-loading",
									Port: &preSwapPort,
								},
							},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Inject a Conflict without changing the tracker state — the route
	// remains pointed at the loading service after the Update is
	// rejected, so routeAlreadySwapped returns false and the handler
	// must return the original error.
	httpRouteGVR := schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"}
	gwClientset.PrependReactor("update", "httproutes", func(_ ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(httpRouteGVR, "test-route", nil)
	})

	err = op.SwapRoute(ctx, analysisID)
	require.Error(t, err, "SwapRoute must propagate a conflict when the route is not already swapped")
	assert.Contains(t, err.Error(), "test-route")
}
