package incluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
		ViceDefaultBackendService:     "vice-default-backend",
		ViceDefaultBackendServicePort: 80,
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

func TestViceProxyCommand(t *testing.T) {
	cfg := baseInit()
	i := New(cfg, nil, nil, nil, nil)
	command := i.viceProxyCommand()

	// The command should just be the binary name with no args.
	assert.Equal(t, []string{"vice-proxy"}, command)
}
