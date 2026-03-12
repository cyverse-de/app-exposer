package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// makeTestHTTPRoute builds a minimal HTTPRoute for testing.
func makeTestHTTPRoute() *gatewayv1.HTTPRoute {
	port := gatewayv1.PortNumber(8080)
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "vice-apps",
			Labels: map[string]string{
				"external-id": "abc-123",
				"username":    "testuser",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"abc123.vice.example.com"},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "test-svc",
									Port: &port,
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestTransformRouting(t *testing.T) {
	tests := []struct {
		name         string
		route        *gatewayv1.HTTPRoute
		routing      RoutingType
		ingressClass string
		wantRoute    bool // true if HTTPRoute should be returned
		wantIngress  bool // true if Ingress should be returned
	}{
		{
			name:         "nil route returns nil for both",
			route:        nil,
			routing:      RoutingGateway,
			ingressClass: "",
			wantRoute:    false,
			wantIngress:  false,
		},
		{
			name:         "gateway returns HTTPRoute only",
			route:        makeTestHTTPRoute(),
			routing:      RoutingGateway,
			ingressClass: "",
			wantRoute:    true,
			wantIngress:  false,
		},
		{
			name:         "nginx converts to Ingress",
			route:        makeTestHTTPRoute(),
			routing:      RoutingNginx,
			ingressClass: "nginx",
			wantRoute:    false,
			wantIngress:  true,
		},
		{
			name:         "tailscale converts to Ingress",
			route:        makeTestHTTPRoute(),
			routing:      RoutingTailscale,
			ingressClass: "tailscale",
			wantRoute:    false,
			wantIngress:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, ingress := TransformRouting(tt.route, tt.routing, tt.ingressClass)

			if tt.wantRoute {
				assert.NotNil(t, route)
			} else {
				assert.Nil(t, route)
			}

			if tt.wantIngress {
				require.NotNil(t, ingress)
				assert.Equal(t, tt.ingressClass, *ingress.Spec.IngressClassName)
				// Verify metadata was copied from the HTTPRoute.
				assert.Equal(t, tt.route.Name, ingress.Name)
				assert.Equal(t, tt.route.Namespace, ingress.Namespace)
				assert.Equal(t, tt.route.Labels, ingress.Labels)
			} else {
				assert.Nil(t, ingress)
			}
		})
	}
}

func TestHttpRouteToIngress(t *testing.T) {
	route := makeTestHTTPRoute()
	ingress := httpRouteToIngress(route, "nginx")

	// Verify basic metadata.
	assert.Equal(t, "test-route", ingress.Name)
	assert.Equal(t, "vice-apps", ingress.Namespace)
	assert.Equal(t, route.Labels, ingress.Labels)

	// Verify ingress class.
	require.NotNil(t, ingress.Spec.IngressClassName)
	assert.Equal(t, "nginx", *ingress.Spec.IngressClassName)

	// Verify default backend.
	require.NotNil(t, ingress.Spec.DefaultBackend)
	assert.Equal(t, "test-svc", ingress.Spec.DefaultBackend.Service.Name)
	assert.Equal(t, int32(8080), ingress.Spec.DefaultBackend.Service.Port.Number)

	// Verify rules match hostnames.
	require.Len(t, ingress.Spec.Rules, 1)
	assert.Equal(t, "abc123.vice.example.com", ingress.Spec.Rules[0].Host)

	// Verify conversion annotation.
	assert.Equal(t, "httproute/test-route", ingress.Annotations["converted-from"])
}

func TestTransformRoutingTailscaleRemovesNginxAnnotations(t *testing.T) {
	route := makeTestHTTPRoute()

	_, ingress := TransformRouting(route, RoutingTailscale, "tailscale")
	require.NotNil(t, ingress)

	// The converted ingress should have no nginx annotations.
	for key := range ingress.Annotations {
		assert.False(t, isNginxAnnotation(key),
			"nginx annotation %q should have been removed", key)
	}
}

func TestTransformIngress(t *testing.T) {
	nginxClass := "nginx"
	tailscaleClass := "tailscale"

	tests := []struct {
		name               string
		ingress            *netv1.Ingress
		targetRouting      RoutingType
		targetIngressClass string
		wantClass          string
		wantNginxAnnots    bool
	}{
		{
			name:               "nil ingress returns nil",
			ingress:            nil,
			targetRouting:      RoutingNginx,
			targetIngressClass: nginxClass,
			wantClass:          "",
		},
		{
			name: "nginx to nginx same class is no-op",
			ingress: &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ingress",
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/proxy-body-size": "4096m",
					},
				},
				Spec: netv1.IngressSpec{
					IngressClassName: &nginxClass,
				},
			},
			targetRouting:      RoutingNginx,
			targetIngressClass: nginxClass,
			wantClass:          nginxClass,
			wantNginxAnnots:    true,
		},
		{
			name: "nginx to tailscale removes nginx annotations",
			ingress: &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ingress",
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/proxy-body-size": "4096m",
						"other-annotation":                            "keep-me",
					},
				},
				Spec: netv1.IngressSpec{
					IngressClassName: &nginxClass,
				},
			},
			targetRouting:      RoutingTailscale,
			targetIngressClass: tailscaleClass,
			wantClass:          tailscaleClass,
			wantNginxAnnots:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TransformIngress(tt.ingress, tt.targetRouting, tt.targetIngressClass)

			if tt.ingress == nil {
				assert.Nil(t, result)
				return
			}

			assert.NotNil(t, result)
			assert.Equal(t, tt.wantClass, *result.Spec.IngressClassName)

			if !tt.wantNginxAnnots {
				for key := range result.Annotations {
					assert.False(t, isNginxAnnotation(key),
						"nginx annotation %q should have been removed", key)
				}
			}

			if tt.targetRouting == RoutingTailscale && tt.ingress.Annotations["other-annotation"] != "" {
				assert.Equal(t, "keep-me", result.Annotations["other-annotation"])
			}
		})
	}
}

func TestIsNginxAnnotation(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"nginx.ingress.kubernetes.io/proxy-body-size", true},
		{"nginx.ingress.kubernetes.io/server-snippets", true},
		{"other-annotation", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.want, isNginxAnnotation(tt.key))
		})
	}
}
