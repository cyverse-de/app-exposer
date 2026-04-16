package operator

import (
	"net/http"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

type mockHTTPClient struct {
	DoFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.DoFunc(req)
}

// makeGatewayPort converts an int32 to a gatewayv1.PortNumber pointer.
func makeGatewayPort(port int32) *gatewayv1.PortNumber {
	p := gatewayv1.PortNumber(port)
	return &p
}

// makeTestHTTPRouteWithLabels builds a minimal HTTPRoute with custom labels.
func makeTestHTTPRouteWithLabels(labels map[string]string, port *gatewayv1.PortNumber) *gatewayv1.HTTPRoute {
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "vice-apps",
			Labels:    labels,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"test.localhost"},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "test-svc",
									Port: port,
								},
							},
						},
					},
				},
			},
		},
	}
}

// newTestOperator creates an Operator wired to fake K8s clients for use in tests.
// vendor defaults to GPUVendorNvidia; pass GPUVendorAMD where needed.
func newTestOperator(t *testing.T, maxAnalyses int, vendor ...GPUVendor) (*Operator, *fake.Clientset, *gatewayfake.Clientset) {
	t.Helper()
	gpuVendor := GPUVendorNvidia
	if len(vendor) > 0 {
		gpuVendor = vendor[0]
	}
	clientset := fake.NewSimpleClientset()
	gwClientset := gatewayfake.NewSimpleClientset()
	calc := NewCapacityCalculator(clientset, "vice-apps", maxAnalyses, "")
	cache := NewImageCacheManager(clientset, "vice-apps", "vice-image-pull-secret")
	op := NewOperator(clientset, gwClientset.GatewayV1(), "vice-apps", "vice-apps", "vice", gpuVendor, calc, cache, "vice-operator-loading", 80, 600000, "", "cluster-config-secret", NetworkPolicyConfig{}, constants.DefaultUserSuffix)
	return op, clientset, gwClientset
}

func TestAnalysisBundleValidate(t *testing.T) {
	tests := []struct {
		name    string
		bundle  operatorclient.AnalysisBundle
		wantErr bool
	}{
		{
			name: "valid bundle",
			bundle: operatorclient.AnalysisBundle{
				AnalysisID: "test",
				Deployment: &appsv1.Deployment{},
				Service:    &apiv1.Service{},
			},
			wantErr: false,
		},
		{name: "missing analysis ID", bundle: operatorclient.AnalysisBundle{Deployment: &appsv1.Deployment{}, Service: &apiv1.Service{}}, wantErr: true},
		{name: "missing deployment", bundle: operatorclient.AnalysisBundle{AnalysisID: "test", Service: &apiv1.Service{}}, wantErr: true},
		{name: "missing service", bundle: operatorclient.AnalysisBundle{AnalysisID: "test", Deployment: &appsv1.Deployment{}}, wantErr: true},
		{name: "empty bundle", bundle: operatorclient.AnalysisBundle{}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.bundle.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
