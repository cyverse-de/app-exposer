package vicebuild

import (
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// PodDisruptionBudget builds the analysis PDB with maxUnavailable=0, so a
// voluntary disruption (node drain, etc.) never evicts the single analysis pod.
func (c *Config) PodDisruptionBudget(spec *operatorclient.VICESpec) *policyv1.PodDisruptionBudget {
	maxUnavailable := intstr.FromInt32(0)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:   pdbName(spec),
			Labels: BuildLabels(spec),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					constants.ExternalIDLabel: string(spec.ExternalID),
				},
			},
			MaxUnavailable: &maxUnavailable,
		},
	}
}
