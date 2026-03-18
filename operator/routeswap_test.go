package operator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSwapRoute(t *testing.T) {
	analysisID := "swap-test-1"
	labels := map[string]string{"analysis-id": analysisID}
	targetSvcName := "analysis-svc"

	tests := []struct {
		name        string
		routingType RoutingType
	}{
		{
			name:        "nginx: swaps Ingress backend",
			routingType: RoutingNginx,
		},
		{
			name:        "tailscale: swaps Ingress backend",
			routingType: RoutingTailscale,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			clientset := fake.NewSimpleClientset()
			calc := NewCapacityCalculator(clientset, "vice-apps", 10, "")
			cache := NewImageCacheManager(clientset, "vice-apps", "vice-image-pull-secret")
			op := NewOperator(clientset, nil, "vice-apps", tt.routingType, "nginx", GPUVendorNvidia, calc, cache,
				"vice-operator-loading", 80, 600000)

			// Create the target analysis service.
			_, err := clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: targetSvcName, Labels: labels},
				Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
			}, metav1.CreateOptions{})
			require.NoError(t, err)

			// Create ingress pointing at loading page service.
			pathType := netv1.PathTypePrefix
			_, err = clientset.NetworkingV1().Ingresses("vice-apps").Create(ctx, &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ing", Namespace: "vice-apps", Labels: labels},
				Spec: netv1.IngressSpec{
					DefaultBackend: &netv1.IngressBackend{
						Service: &netv1.IngressServiceBackend{
							Name: "vice-operator-loading",
							Port: netv1.ServiceBackendPort{Number: 80},
						},
					},
					Rules: []netv1.IngressRule{
						{
							Host: "abc123.vice.example.com",
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

			err = op.SwapRoute(ctx, analysisID)
			require.NoError(t, err)

			// Verify the ingress was swapped.
			ings, err := clientset.NetworkingV1().Ingresses("vice-apps").List(ctx, analysisLabelSelector(analysisID))
			require.NoError(t, err)
			require.Len(t, ings.Items, 1)
			assert.Equal(t, targetSvcName, ings.Items[0].Spec.DefaultBackend.Service.Name)
			for _, rule := range ings.Items[0].Spec.Rules {
				for _, path := range rule.HTTP.Paths {
					assert.Equal(t, targetSvcName, path.Backend.Service.Name)
				}
			}
		})
	}
}
