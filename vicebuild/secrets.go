package vicebuild

import (
	"github.com/cyverse-de/app-exposer/operatorclient"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SensitiveEnvSecret builds the per-analysis Secret holding
// VICESpec.SensitiveEnvironment, or nil when the analysis has none.
func (c *Config) SensitiveEnvSecret(spec *operatorclient.VICESpec) *apiv1.Secret {
	if len(spec.SensitiveEnvironment) == 0 {
		return nil
	}
	data := make(map[string][]byte, len(spec.SensitiveEnvironment))
	for k, v := range spec.SensitiveEnvironment {
		data[k] = []byte(v)
	}
	return &apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:   sensitiveEnvSecretName(spec),
			Labels: BuildLabels(spec),
		},
		Type: apiv1.SecretTypeOpaque,
		Data: data,
	}
}
