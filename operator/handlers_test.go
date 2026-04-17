package operator

import (
	"net/http"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	op, err := NewOperator(OperatorOptions{
		Clientset:           clientset,
		GatewayClient:       gwClientset.GatewayV1(),
		Namespace:           "vice-apps",
		GatewayNamespace:    "vice-apps",
		GatewayName:         "vice",
		GPUVendor:           gpuVendor,
		CapacityCalc:        calc,
		ImageCache:          cache,
		LoadingServiceName:  "vice-operator-loading",
		LoadingServicePort:  80,
		LoadingTimeoutMs:    600000,
		ClusterConfigSecret: "cluster-config-secret",
		UserSuffix:          constants.DefaultUserSuffix,
	})
	require.NoError(t, err)
	return op, clientset, gwClientset
}

func TestOperatorOptionsValidate(t *testing.T) {
	// valid is the minimum set of fields that satisfies Validate; each
	// row below mutates a single field so the assertion can isolate
	// which required-field check failed.
	valid := func() OperatorOptions {
		clientset := fake.NewSimpleClientset()
		gw := gatewayfake.NewSimpleClientset()
		return OperatorOptions{
			Clientset:     clientset,
			GatewayClient: gw.GatewayV1(),
			Namespace:     "vice-apps",
			CapacityCalc:  &CapacityCalculator{},
			ImageCache:    &ImageCacheManager{},
		}
	}

	tests := []struct {
		name        string
		mutate      func(*OperatorOptions)
		wantErr     bool
		wantErrPart string
	}{
		{name: "happy path", mutate: func(*OperatorOptions) {}, wantErr: false},
		{name: "missing Clientset", mutate: func(o *OperatorOptions) { o.Clientset = nil }, wantErr: true, wantErrPart: "Clientset"},
		{name: "missing GatewayClient", mutate: func(o *OperatorOptions) { o.GatewayClient = nil }, wantErr: true, wantErrPart: "GatewayClient"},
		{name: "empty Namespace", mutate: func(o *OperatorOptions) { o.Namespace = "" }, wantErr: true, wantErrPart: "Namespace"},
		{name: "missing CapacityCalc", mutate: func(o *OperatorOptions) { o.CapacityCalc = nil }, wantErr: true, wantErrPart: "CapacityCalc"},
		{name: "missing ImageCache", mutate: func(o *OperatorOptions) { o.ImageCache = nil }, wantErr: true, wantErrPart: "ImageCache"},
		{
			name: "invalid EgressConfig surfaces inner error",
			mutate: func(o *OperatorOptions) {
				o.EgressConfig = NetworkPolicyConfig{PodExceptions: []map[string]string{{}}}
			},
			wantErr:     true,
			wantErrPart: "EgressConfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := valid()
			tt.mutate(&o)
			err := o.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrPart != "" {
					assert.Contains(t, err.Error(), tt.wantErrPart)
				}
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestAnalysisBundleValidate(t *testing.T) {
	labels := func(id operatorclient.AnalysisID) map[string]string {
		return map[string]string{constants.AnalysisIDLabel: string(id)}
	}

	// withLabels returns a shallow bundle wired with a Deployment and a
	// Service both labeled with the given analysis-id, so each test row
	// can focus on the invariant it's exercising without restating the
	// valid-skeleton boilerplate.
	withLabels := func(id operatorclient.AnalysisID) operatorclient.AnalysisBundle {
		return operatorclient.AnalysisBundle{
			AnalysisID: id,
			Deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Labels: labels(id)},
			},
			Service: &apiv1.Service{
				ObjectMeta: metav1.ObjectMeta{Labels: labels(id)},
			},
		}
	}

	tests := []struct {
		name        string
		bundle      operatorclient.AnalysisBundle
		wantErr     bool
		wantErrPart string // optional substring match on the error message
	}{
		{name: "valid minimal bundle", bundle: withLabels("test"), wantErr: false},
		{
			name: "valid bundle with every child labeled",
			bundle: operatorclient.AnalysisBundle{
				AnalysisID: "test",
				Deployment: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Labels: labels("test")}},
				Service:    &apiv1.Service{ObjectMeta: metav1.ObjectMeta{Labels: labels("test")}},
				HTTPRoute:  &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Labels: labels("test")}},
				ConfigMaps: []*apiv1.ConfigMap{
					{ObjectMeta: metav1.ObjectMeta{Labels: labels("test")}},
				},
				PersistentVolumeClaims: []*apiv1.PersistentVolumeClaim{
					{ObjectMeta: metav1.ObjectMeta{Labels: labels("test")}},
				},
			},
			wantErr: false,
		},
		{name: "missing analysis ID", bundle: operatorclient.AnalysisBundle{Deployment: &appsv1.Deployment{}, Service: &apiv1.Service{}}, wantErr: true, wantErrPart: "analysisID"},
		{name: "missing deployment", bundle: operatorclient.AnalysisBundle{AnalysisID: "test", Service: &apiv1.Service{}}, wantErr: true, wantErrPart: "deployment"},
		{name: "missing service", bundle: operatorclient.AnalysisBundle{AnalysisID: "test", Deployment: &appsv1.Deployment{}}, wantErr: true, wantErrPart: "service"},
		{name: "empty bundle", bundle: operatorclient.AnalysisBundle{}, wantErr: true},
		{
			name: "deployment label mismatched",
			bundle: operatorclient.AnalysisBundle{
				AnalysisID: "test",
				Deployment: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Labels: labels("other")}},
				Service:    &apiv1.Service{ObjectMeta: metav1.ObjectMeta{Labels: labels("test")}},
			},
			wantErr:     true,
			wantErrPart: "deployment has analysis-id label",
		},
		{
			name: "service label mismatched",
			bundle: operatorclient.AnalysisBundle{
				AnalysisID: "test",
				Deployment: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Labels: labels("test")}},
				Service:    &apiv1.Service{ObjectMeta: metav1.ObjectMeta{Labels: labels("other")}},
			},
			wantErr:     true,
			wantErrPart: "service has analysis-id label",
		},
		{
			name: "httpRoute label mismatched",
			bundle: func() operatorclient.AnalysisBundle {
				b := withLabels("test")
				b.HTTPRoute = &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Labels: labels("other")}}
				return b
			}(),
			wantErr:     true,
			wantErrPart: "httpRoute has analysis-id label",
		},
		{
			name: "configmap label mismatched",
			bundle: func() operatorclient.AnalysisBundle {
				b := withLabels("test")
				b.ConfigMaps = []*apiv1.ConfigMap{
					{ObjectMeta: metav1.ObjectMeta{Labels: labels("other")}},
				}
				return b
			}(),
			wantErr:     true,
			wantErrPart: "configMaps[0] has analysis-id label",
		},
		{
			name: "nil pointer in ConfigMaps is skipped",
			bundle: func() operatorclient.AnalysisBundle {
				b := withLabels("test")
				b.ConfigMaps = []*apiv1.ConfigMap{nil}
				return b
			}(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.bundle.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrPart != "" {
					assert.Contains(t, err.Error(), tt.wantErrPart)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
