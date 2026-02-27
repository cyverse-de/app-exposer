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
		Steps: []model.Step{
			{},
		},
	}
	// Set the port using struct initialization to avoid type issues
	job.Steps[0].Component.Container.Ports = make([]model.Ports, 1)
	job.Steps[0].Component.Container.Ports[0].ContainerPort = 8888
	return job
}

func TestViceProxyCommandWithAuthEnabled(t *testing.T) {
	init := &Init{
		PorklockImage:                 "harbor.cyverse.org/de/porklock",
		PorklockTag:                   "latest",
		UseCSIDriver:                  true,
		InputPathListIdentifier:       "imapathlist",
		TicketInputPathListIdentifier: "imaticketpathlist",
		ImagePullSecretName:           "imanimagepullsecret",
		ViceProxyImage:                "harbor.cyverse.org/de/vice-proxy",
		FrontendBaseURL:               "https://de.example.org",
		ViceDefaultBackendService:     "vice-default-backend",
		ViceDefaultBackendServicePort: 80,
		VICEBackendNamespace:          "prod",
		AppsServiceBaseURL:            "http://apps.prod",
		ViceNamespace:                 "vice-apps",
		JobStatusURL:                  "http://job-satus-recorder.prod",
		UserSuffix:                    "@example.org",
		PermissionsURL:                "http://permissions.prod",
		KeycloakBaseURL:               "https://keycloak.example.org/auth",
		KeycloakRealm:                 "example",
		KeycloakClientID:              "theclient",
		KeycloakClientSecret:          "thesecret",
		IRODSZone:                     "example",
		IngressClass:                  "nginx",
		LocalStorageClass:             "example",
		DisableViceProxyAuth:          false, // Authentication enabled
		NATSEncodedConn:               nil,
	}
	i := New(init, nil, nil, nil)
	job := testJob()

	command := i.viceProxyCommand(job)

	// Verify that --disable-auth is NOT in the command when authentication is enabled
	for _, arg := range command {
		assert.NotEqual(t, "--disable-auth", arg, "command should not contain --disable-auth when authentication is enabled")
	}

	// Verify that essential flags are present
	assert.Contains(t, command, "vice-proxy")
	assert.Contains(t, command, "--listen-addr")
	assert.Contains(t, command, "--backend-url")
	assert.Contains(t, command, "--frontend-url")
	assert.Contains(t, command, "--analysis-id")
	assert.Contains(t, command, "test-analysis-id")
	assert.Contains(t, command, "--keycloak-base-url")
	// Verify old/legacy flags are NOT present in default mode
	assert.NotContains(t, command, "--external-id")
	assert.NotContains(t, command, "--get-analysis-id-base")
	assert.NotContains(t, command, "--check-resource-access-base")
	assert.NotContains(t, command, "--enable-legacy-auth")
}

func TestViceProxyCommandWithAuthDisabled(t *testing.T) {
	init := &Init{
		PorklockImage:                 "harbor.cyverse.org/de/porklock",
		PorklockTag:                   "latest",
		UseCSIDriver:                  true,
		InputPathListIdentifier:       "imapathlist",
		TicketInputPathListIdentifier: "imaticketpathlist",
		ImagePullSecretName:           "imanimagepullsecret",
		ViceProxyImage:                "harbor.cyverse.org/de/vice-proxy",
		FrontendBaseURL:               "https://de.example.org",
		ViceDefaultBackendService:     "vice-default-backend",
		ViceDefaultBackendServicePort: 80,
		VICEBackendNamespace:          "prod",
		AppsServiceBaseURL:            "http://apps.prod",
		ViceNamespace:                 "vice-apps",
		JobStatusURL:                  "http://job-satus-recorder.prod",
		UserSuffix:                    "@example.org",
		PermissionsURL:                "http://permissions.prod",
		KeycloakBaseURL:               "https://keycloak.example.org/auth",
		KeycloakRealm:                 "example",
		KeycloakClientID:              "theclient",
		KeycloakClientSecret:          "thesecret",
		IRODSZone:                     "example",
		IngressClass:                  "nginx",
		LocalStorageClass:             "example",
		DisableViceProxyAuth:          true, // Authentication disabled
		NATSEncodedConn:               nil,
	}
	i := New(init, nil, nil, nil)
	job := testJob()

	command := i.viceProxyCommand(job)

	// Verify that --disable-auth IS in the command when authentication is disabled
	assert.Contains(t, command, "--disable-auth", "command should contain --disable-auth when authentication is disabled")

	// Verify that essential flags are still present
	assert.Contains(t, command, "vice-proxy")
	assert.Contains(t, command, "--listen-addr")
	assert.Contains(t, command, "--backend-url")
	assert.Contains(t, command, "--frontend-url")
	assert.Contains(t, command, "--analysis-id")
	assert.Contains(t, command, "test-analysis-id")
}

func TestViceProxyCommandFlagOrdering(t *testing.T) {
	init := &Init{
		PorklockImage:                 "harbor.cyverse.org/de/porklock",
		PorklockTag:                   "latest",
		UseCSIDriver:                  true,
		InputPathListIdentifier:       "imapathlist",
		TicketInputPathListIdentifier: "imaticketpathlist",
		ImagePullSecretName:           "imanimagepullsecret",
		ViceProxyImage:                "harbor.cyverse.org/de/vice-proxy",
		FrontendBaseURL:               "https://de.example.org",
		ViceDefaultBackendService:     "vice-default-backend",
		ViceDefaultBackendServicePort: 80,
		VICEBackendNamespace:          "prod",
		AppsServiceBaseURL:            "http://apps.prod",
		ViceNamespace:                 "vice-apps",
		JobStatusURL:                  "http://job-satus-recorder.prod",
		UserSuffix:                    "@example.org",
		PermissionsURL:                "http://permissions.prod",
		KeycloakBaseURL:               "https://keycloak.example.org/auth",
		KeycloakRealm:                 "example",
		KeycloakClientID:              "theclient",
		KeycloakClientSecret:          "thesecret",
		IRODSZone:                     "example",
		IngressClass:                  "nginx",
		LocalStorageClass:             "example",
		DisableViceProxyAuth:          true,
		NATSEncodedConn:               nil,
	}
	i := New(init, nil, nil, nil)
	job := testJob()

	command := i.viceProxyCommand(job)

	// --disable-auth should be at the end of the command (last element)
	assert.Equal(t, "--disable-auth", command[len(command)-1], "--disable-auth should be the last argument")
}

func TestViceProxyCommandWithLegacyAuth(t *testing.T) {
	init := &Init{
		PorklockImage:                 "harbor.cyverse.org/de/porklock",
		PorklockTag:                   "latest",
		UseCSIDriver:                  true,
		InputPathListIdentifier:       "imapathlist",
		TicketInputPathListIdentifier: "imaticketpathlist",
		ImagePullSecretName:           "imanimagepullsecret",
		ViceProxyImage:                "harbor.cyverse.org/de/vice-proxy",
		FrontendBaseURL:               "https://de.example.org",
		ViceDefaultBackendService:     "vice-default-backend",
		ViceDefaultBackendServicePort: 80,
		VICEBackendNamespace:          "prod",
		AppsServiceBaseURL:            "http://apps.prod",
		ViceNamespace:                 "vice-apps",
		JobStatusURL:                  "http://job-satus-recorder.prod",
		UserSuffix:                    "@example.org",
		PermissionsURL:                "http://permissions.prod",
		KeycloakBaseURL:               "https://keycloak.example.org/auth",
		KeycloakRealm:                 "example",
		KeycloakClientID:              "theclient",
		KeycloakClientSecret:          "thesecret",
		IRODSZone:                     "example",
		IngressClass:                  "nginx",
		LocalStorageClass:             "example",
		DisableViceProxyAuth:          false,
		EnableLegacyViceProxyAuth:     true,
		CheckResourceAccessService:    "check-resource-access",
		NATSEncodedConn:               nil,
	}
	i := New(init, nil, nil, nil)
	job := testJob()

	command := i.viceProxyCommand(job)

	// Legacy auth flags should be present
	assert.Contains(t, command, "--enable-legacy-auth")
	assert.Contains(t, command, "--check-resource-access-base")
	assert.Contains(t, command, "http://check-resource-access.vice-apps")

	// --analysis-id should still be present (both modes use it)
	assert.Contains(t, command, "--analysis-id")
	assert.Contains(t, command, "test-analysis-id")

	// --disable-auth should NOT be present (auth is enabled)
	assert.NotContains(t, command, "--disable-auth")
}

func TestViceProxyCommandLegacyWithAuthDisabled(t *testing.T) {
	init := &Init{
		PorklockImage:                 "harbor.cyverse.org/de/porklock",
		PorklockTag:                   "latest",
		UseCSIDriver:                  true,
		InputPathListIdentifier:       "imapathlist",
		TicketInputPathListIdentifier: "imaticketpathlist",
		ImagePullSecretName:           "imanimagepullsecret",
		ViceProxyImage:                "harbor.cyverse.org/de/vice-proxy",
		FrontendBaseURL:               "https://de.example.org",
		ViceDefaultBackendService:     "vice-default-backend",
		ViceDefaultBackendServicePort: 80,
		VICEBackendNamespace:          "prod",
		AppsServiceBaseURL:            "http://apps.prod",
		ViceNamespace:                 "vice-apps",
		JobStatusURL:                  "http://job-satus-recorder.prod",
		UserSuffix:                    "@example.org",
		PermissionsURL:                "http://permissions.prod",
		KeycloakBaseURL:               "https://keycloak.example.org/auth",
		KeycloakRealm:                 "example",
		KeycloakClientID:              "theclient",
		KeycloakClientSecret:          "thesecret",
		IRODSZone:                     "example",
		IngressClass:                  "nginx",
		LocalStorageClass:             "example",
		DisableViceProxyAuth:          true,
		EnableLegacyViceProxyAuth:     true,
		CheckResourceAccessService:    "my-cra-service",
		NATSEncodedConn:               nil,
	}
	i := New(init, nil, nil, nil)
	job := testJob()

	command := i.viceProxyCommand(job)

	// Both --disable-auth and legacy auth flags should coexist
	assert.Contains(t, command, "--disable-auth")
	assert.Contains(t, command, "--enable-legacy-auth")
	assert.Contains(t, command, "--check-resource-access-base")
	assert.Contains(t, command, "http://my-cra-service.vice-apps")

	// --analysis-id should still be present
	assert.Contains(t, command, "--analysis-id")
	assert.Contains(t, command, "test-analysis-id")
}
