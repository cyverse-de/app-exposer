package incluster

import (
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// baseInit returns a fully-populated Init for use in tests. Individual test
// cases override only the fields relevant to the scenario under test.
func baseInit() *Init {
	return &Init{
		PorklockImage:                 "harbor.cyverse.org/de/porklock",
		PorklockTag:                   "latest",
		UseCSIDriver:                  true,
		InputPathListIdentifier:       "imapathlist",
		TicketInputPathListIdentifier: "imaticketpathlist",
		ImagePullSecretName:           "imanimagepullsecret",
		ViceProxyImage:                "harbor.cyverse.org/de/vice-proxy",
		FrontendBaseURL:               "https://de.example.org",
		ViceDomain:                    "cyverse.run",
		VICEBackendNamespace:          "prod",
		AppsServiceBaseURL:            "http://apps.prod",
		ViceNamespace:                 "vice-apps",
		JobStatusURL:                  "http://job-status-recorder.prod",
		UserSuffix:                    "@example.org",
		PermissionsURL:                "http://permissions.prod",
		IRODSZone:                     "example",
		GatewayProvider:               "traefik",
		LocalStorageClass:             "example",
	}
}

func TestGetMillicoresFromDeployment(t *testing.T) {
	makeDeployment := func(containers []apiv1.Container) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-dep"},
			Spec: appsv1.DeploymentSpec{
				Template: apiv1.PodTemplateSpec{
					Spec: apiv1.PodSpec{Containers: containers},
				},
			},
		}
	}

	tests := []struct {
		name       string
		deployment *appsv1.Deployment
		wantErr    string
	}{
		{
			name: "returns millicores when CPU limit is set",
			deployment: makeDeployment([]apiv1.Container{
				{
					Name: constants.AnalysisContainerName,
					Resources: apiv1.ResourceRequirements{
						Limits: apiv1.ResourceList{
							apiv1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			}),
		},
		{
			name: "returns error when analysis container has no CPU limit",
			deployment: makeDeployment([]apiv1.Container{
				{
					Name:      constants.AnalysisContainerName,
					Resources: apiv1.ResourceRequirements{},
				},
			}),
			wantErr: "no CPU limit",
		},
		{
			name: "returns error when analysis container has nil Limits map",
			deployment: makeDeployment([]apiv1.Container{
				{Name: constants.AnalysisContainerName},
			}),
			wantErr: "no CPU limit",
		},
		{
			name: "returns error when analysis container is missing",
			deployment: makeDeployment([]apiv1.Container{
				{Name: "other-container"},
			}),
			wantErr: "could not find the analysis container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetMillicoresFromDeployment(tt.deployment)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				// 2 CPUs = 2000 millicores
				assert.Equal(t, "2000", result.String())
			}
		})
	}
}

func TestViceProxyCommand(t *testing.T) {
	cfg := baseInit()
	i := New(cfg, nil, nil, nil, nil)
	command := i.viceProxyCommand()

	// The command should just be the binary name with no args.
	assert.Equal(t, []string{"vice-proxy"}, command)
}
