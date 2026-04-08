package operator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	gatewayfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

func TestEnsureGateway(t *testing.T) {
	tests := []struct {
		name      string
		preCreate bool // pre-create the Gateway before calling EnsureGateway
	}{
		{name: "creates Gateway when missing", preCreate: false},
		{name: "no-op when Gateway already exists", preCreate: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gwClientset := gatewayfake.NewSimpleClientset()
			gwClient := gwClientset.GatewayV1()

			if tt.preCreate {
				err := EnsureGateway(context.Background(), gwClient, "vice-apps", "vice", "traefik", 8000)
				require.NoError(t, err)
			}

			err := EnsureGateway(context.Background(), gwClient, "vice-apps", "vice", "traefik", 8000)
			require.NoError(t, err)

			// Verify the Gateway exists with correct spec.
			gw, err := gwClient.Gateways("vice-apps").Get(context.Background(), "vice", metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, "vice", gw.Name)
			require.Len(t, gw.Spec.Listeners, 1)
			assert.Equal(t, int32(8000), int32(gw.Spec.Listeners[0].Port))
		})
	}
}

func TestEnsureService(t *testing.T) {
	tests := []struct {
		name      string
		preCreate bool
	}{
		{name: "creates Service when missing", preCreate: false},
		{name: "no-op when Service already exists", preCreate: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := fake.NewSimpleClientset()
			selector := map[string]string{"app": "test"}

			if tt.preCreate {
				err := EnsureService(context.Background(), cs, "vice-apps", "test-svc", 80, 8080, selector)
				require.NoError(t, err)
			}

			err := EnsureService(context.Background(), cs, "vice-apps", "test-svc", 80, 8080, selector)
			require.NoError(t, err)

			svc, err := cs.CoreV1().Services("vice-apps").Get(context.Background(), "test-svc", metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, "test-svc", svc.Name)
			require.Len(t, svc.Spec.Ports, 1)
			assert.Equal(t, int32(80), svc.Spec.Ports[0].Port)
			assert.Equal(t, int32(8080), svc.Spec.Ports[0].TargetPort.IntVal)
			assert.Equal(t, selector, svc.Spec.Selector)
		})
	}
}

func TestEnsureAPIRoute(t *testing.T) {
	tests := []struct {
		name      string
		preCreate bool
		preNS     string
		wantNS    string
	}{
		{
			name:      "creates HTTPRoute when missing",
			preCreate: false,
			wantNS:    "vice-apps",
		},
		{
			name:      "no-op when HTTPRoute already matches",
			preCreate: true,
			preNS:     "vice-apps",
			wantNS:    "vice-apps",
		},
		{
			name:      "updates HTTPRoute when gateway namespace differs",
			preCreate: true,
			preNS:     "wrong-ns",
			wantNS:    "correct-ns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gwClientset := gatewayfake.NewSimpleClientset()
			gwClient := gwClientset.GatewayV1()

			if tt.preCreate {
				err := EnsureAPIRoute(context.Background(), gwClient, "vice-apps", tt.preNS, "vice", "vice-api.localhost", "vice-operator", 10000)
				require.NoError(t, err)
			}

			err := EnsureAPIRoute(context.Background(), gwClient, "vice-apps", tt.wantNS, "vice", "vice-api.localhost", "vice-operator", 10000)
			require.NoError(t, err)

			route, err := gwClient.HTTPRoutes("vice-apps").Get(context.Background(), "vice-operator-api", metav1.GetOptions{})
			require.NoError(t, err)
			require.Len(t, route.Spec.Hostnames, 1)
			assert.Equal(t, "vice-api.localhost", string(route.Spec.Hostnames[0]))

			require.Len(t, route.Spec.ParentRefs, 1)
			require.NotNil(t, route.Spec.ParentRefs[0].Namespace)
			assert.Equal(t, tt.wantNS, string(*route.Spec.ParentRefs[0].Namespace))
		})
	}
}
