package incluster

import (
	"testing"

	"github.com/cyverse-de/model/v9"
	"github.com/stretchr/testify/assert"
)

const (
	gib = 1024 * 1024 * 1024
)

func newIncluster() *Incluster {
	init := &Init{
		PorklockImage:                 "harbor.cyverse.org/de/porklock",
		PorklockTag:                   "latest",
		UseCSIDriver:                  true,
		InputPathListIdentifier:       "imapathlist",
		TicketInputPathListIdentifier: "imaticketpathlist",
		ImagePullSecretName:           "imanimagepullsecret",
		ViceProxyImage:       "harbor.cyverse.org/de/vice-proxy",
		FrontendBaseURL:      "https://de.example.org",
		VICEBackendNamespace: "prod",
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
		NATSEncodedConn:               nil,
	}
	return New(init, nil, nil, nil)
}

func jobWithDiskRequirements(capacities ...int64) *model.Job {
	steps := make([]model.Step, len(capacities))
	for i, capacity := range capacities {
		steps[i].Component.Container.MinDiskSpace = capacity
	}
	return &model.Job{
		Steps: steps,
	}
}

func TestPersistentVolumeCapacity(t *testing.T) {
	i := newIncluster()

	// Build the list of test cases.
	var testCases = []struct {
		capacities  []int64
		expected    int64
		description string
	}{
		{[]int64{0}, 5 * gib, "no capacity specified"},
		{[]int64{10 * gib, 20 * gib}, 20 * gib, "highest capacity greater than default"},
		{[]int64{32 * gib}, 32 * gib, "highest capacity equal to 32 gib"},
		{[]int64{64 * gib}, 64 * gib, "highest capacity greater than 32 gib"},
		{[]int64{4096 * gib, 5 * gib}, 4096 * gib, "highest capacity of 4 tib"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			job := jobWithDiskRequirements(testCase.capacities...)
			capacity := i.getPersistentVolumeCapacity(job)
			actual, _ := capacity.AsInt64()
			assert.Equal(t, testCase.expected, actual)
		})
	}
}

func TestPersistentVolumeDisplay(t *testing.T) {
	i := newIncluster()

	// Build the list of test cases.
	var testCases = []struct {
		capacities  []int64
		expected    string
		description string
	}{
		{[]int64{0}, "5Gi", "no capacity specified"},
		{[]int64{10 * gib, 20 * gib}, "20Gi", "highest capacity greater than default"},
		{[]int64{32 * gib}, "32Gi", "highest capacity equal to 32 gib"},
		{[]int64{64 * gib}, "64Gi", "highest capacity greater than 32 gib"},
		{[]int64{4096 * gib, 5 * gib}, "4Ti", "highest capacity of 4 tib"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			job := jobWithDiskRequirements(testCase.capacities...)
			capacity := i.getPersistentVolumeCapacity(job)
			actual := capacity.String()
			assert.Equal(t, testCase.expected, actual)
		})
	}
}
