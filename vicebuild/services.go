package vicebuild

import (
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Service builds the Service fronting the analysis pod: the file-transfers port
// and the vice-proxy port, selected by external-id.
func (c *Config) Service(spec *operatorclient.VICESpec) *apiv1.Service {
	return &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:   serviceName(spec),
			Labels: BuildLabels(spec),
		},
		Spec: apiv1.ServiceSpec{
			Selector: map[string]string{
				constants.ExternalIDLabel: string(spec.ExternalID),
			},
			Ports: []apiv1.ServicePort{
				{
					Name:       constants.FileTransfersPortName,
					Protocol:   apiv1.ProtocolTCP,
					Port:       constants.FileTransfersPort,
					TargetPort: intstr.FromInt32(constants.FileTransfersPort),
				},
				{
					Name:       constants.VICEProxyPortName,
					Protocol:   apiv1.ProtocolTCP,
					Port:       constants.VICEProxyServicePort,
					TargetPort: intstr.FromInt32(constants.VICEProxyPort),
				},
			},
		},
	}
}
