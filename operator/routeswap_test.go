package operator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
