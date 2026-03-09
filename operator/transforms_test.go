package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTransformIngress(t *testing.T) {
	nginxClass := "nginx"
	tailscaleClass := "tailscale"

	tests := []struct {
		name               string
		ingress            *netv1.Ingress
		targetRouting      RoutingType
		targetIngressClass string
		wantClass          string
		wantNginxAnnots    bool // true if nginx annotations should remain
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
						"nginx.ingress.kubernetes.io/proxy-body-size":       "4096m",
						"nginx.ingress.kubernetes.io/proxy-read-timeout":    "172800",
						"nginx.ingress.kubernetes.io/proxy-send-timeout":    "172800",
						"nginx.ingress.kubernetes.io/proxy-connect-timeout": "5000",
						"nginx.ingress.kubernetes.io/server-snippets":       "location / { ... }",
						"other-annotation": "keep-me",
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
		{
			name: "nginx to different nginx class updates class",
			ingress: &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-ingress",
					Annotations: map[string]string{},
				},
				Spec: netv1.IngressSpec{
					IngressClassName: &nginxClass,
				},
			},
			targetRouting:      RoutingNginx,
			targetIngressClass: "nginx-internal",
			wantClass:          "nginx-internal",
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

			// Verify non-nginx annotations are preserved during tailscale transform.
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
