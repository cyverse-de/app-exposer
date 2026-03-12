package incluster

import (
	"testing"

	"github.com/cyverse-de/model/v10"
	"github.com/stretchr/testify/assert"
)

// testJob creates a minimal Job for testing purposes.
func testJob() *model.Job {
	job := &model.Job{
		ID:           "test-analysis-id",
		UserID:       "test-user",
		InvocationID: "test-invocation-id",
		Steps:        []model.Step{{}},
	}
	job.Steps[0].Component.Container.Ports = make([]model.Ports, 1)
	job.Steps[0].Component.Container.Ports[0].ContainerPort = 8888
	return job
}

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
		KeycloakBaseURL:               "https://keycloak.example.org/auth",
		KeycloakRealm:                 "example",
		KeycloakClientID:              "theclient",
		KeycloakClientSecret:          "thesecret",
		IRODSZone:                     "example",
		GatewayProvider:               "traefik",
		LocalStorageClass:             "example",
	}
}

func TestViceProxyCommand(t *testing.T) {
	tests := []struct {
		name                   string
		disableAuth            bool
		enableLegacyAuth       bool
		checkResourceAccessURL string
		// expected contains strings that must appear in the command.
		expected []string
		// notExpected contains strings that must NOT appear in the command.
		notExpected []string
		// checkLastArg, when set, asserts the final command element equals this value.
		checkLastArg string
	}{
		{
			name:        "auth enabled (default mode)",
			disableAuth: false,
			expected: []string{
				"vice-proxy",
				"--listen-addr",
				"--backend-url",
				"--frontend-url",
				"--analysis-id",
				"test-analysis-id",
				"--keycloak-base-url",
			},
			notExpected: []string{
				"--disable-auth",
				"--external-id",
				"--get-analysis-id-base",
				"--check-resource-access-base",
				"--enable-legacy-auth",
			},
		},
		{
			name:        "auth disabled",
			disableAuth: true,
			expected: []string{
				"vice-proxy",
				"--listen-addr",
				"--backend-url",
				"--frontend-url",
				"--analysis-id",
				"test-analysis-id",
				"--disable-auth",
			},
			notExpected: []string{
				"--enable-legacy-auth",
				"--check-resource-access-base",
			},
		},
		{
			// --disable-auth should be the final argument when legacy auth is not
			// appending additional flags after it.
			name:         "disable-auth is last arg when no legacy auth",
			disableAuth:  true,
			checkLastArg: "--disable-auth",
		},
		{
			name:                   "legacy auth enabled",
			disableAuth:            false,
			enableLegacyAuth:       true,
			checkResourceAccessURL: "http://check-resource-access.qa",
			expected: []string{
				"--enable-legacy-auth",
				"--check-resource-access-base",
				"http://check-resource-access.qa",
				"--analysis-id",
				"test-analysis-id",
			},
			notExpected: []string{
				"--disable-auth",
			},
		},
		{
			name:                   "legacy auth and disable-auth coexist",
			disableAuth:            true,
			enableLegacyAuth:       true,
			checkResourceAccessURL: "http://my-cra-service.custom-ns",
			expected: []string{
				"--disable-auth",
				"--enable-legacy-auth",
				"--check-resource-access-base",
				"http://my-cra-service.custom-ns",
				"--analysis-id",
				"test-analysis-id",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseInit()
			cfg.DisableViceProxyAuth = tc.disableAuth
			cfg.EnableLegacyViceProxyAuth = tc.enableLegacyAuth
			cfg.CheckResourceAccessURL = tc.checkResourceAccessURL

			i := New(cfg, nil, nil, nil, nil)
			command := i.viceProxyCommand(testJob())

			for _, want := range tc.expected {
				assert.Contains(t, command, want)
			}
			for _, unwanted := range tc.notExpected {
				assert.NotContains(t, command, unwanted)
			}
			if tc.checkLastArg != "" {
				assert.Equal(t, tc.checkLastArg, command[len(command)-1])
			}
		})
	}
}
