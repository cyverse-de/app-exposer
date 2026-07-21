package vicebuild

import (
	"strings"
	"testing"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testSpec() *operatorclient.VICESpec {
	return &operatorclient.VICESpec{
		SpecVersion: operatorclient.CurrentVICESpecVersion,
		AnalysisID:  "analysis-1",
		ExternalID:  "external-1",
		JobName:     "My Analysis",
		AppID:       "app-1",
		AppName:     "JupyterLab",
		UserID:      "user-1",
		Submitter:   "someuser",
		UserLoginIP: "10.0.0.1",
		Container: operatorclient.ContainerSpec{
			Image:      "cyverse/jupyter",
			Tag:        "latest",
			UID:        1000,
			Ports:      []int{8888},
			WorkingDir: "/de-app-work",
		},
		MountDataStore: true,
		UserHome:       "/iplant/home/someuser",
	}
}

func testConfig() *Config {
	return &Config{
		UserSuffix:              "@iplantcollaborative.org",
		InputPathListIdentifier: "# application/vnd.de.path-list+csv; version=1",
	}
}

// TestBuildLabelsMatchesJobInfo locks the contract that BuildLabels(spec) emits
// the same label set jobinfo.JobLabels does, with the DB-derived login IP
// arriving via the spec. The keys come straight from the constants package, so
// this asserts each key's presence and value rather than reaching into jobinfo
// (which carries an apps/DB dependency the test would otherwise drag in).
func TestBuildLabelsMatchesJobInfo(t *testing.T) {
	spec := testSpec()
	labels := BuildLabels(spec)

	want := map[string]string{
		constants.ExternalIDLabel:   "external-1",
		constants.AnalysisIDLabel:   "analysis-1",
		constants.AppNameLabel:      common.LabelValueString("JupyterLab"),
		constants.AppIDLabel:        "app-1",
		constants.UsernameLabel:     common.LabelValueString("someuser"),
		constants.UserIDLabel:       "user-1",
		constants.AnalysisNameLabel: common.LabelValueString("My Analysis"),
		constants.AppTypeLabel:      "interactive",
		constants.SubdomainLabel:    common.Subdomain("user-1", "external-1"),
		constants.LoginIPLabel:      "10.0.0.1",
	}
	assert.Equal(t, want, labels)
}

func TestBuildLabelsTruncatesAnalysisName(t *testing.T) {
	spec := testSpec()
	spec.JobName = strings.Repeat("x", 100)
	labels := BuildLabels(spec)
	assert.LessOrEqual(t, len([]rune(labels[constants.AnalysisNameLabel])), 63)
}

func TestExcludesConfigMap(t *testing.T) {
	spec := testSpec()
	spec.ExcludeArguments = []string{"/path/a", "/path/b"}
	cm := testConfig().ExcludesConfigMap(spec)

	assert.Equal(t, "excludes-file-external-1", cm.Name)
	assert.Equal(t, "analysis-1", cm.Labels[constants.AnalysisIDLabel])
	assert.Equal(t, "/path/a\n/path/b\n", cm.Data[constants.ExcludesFileName])
}

func TestPermissionsConfigMap(t *testing.T) {
	tests := []struct {
		name      string
		submitter string
		want      string
	}{
		{name: "bare username gets suffix", submitter: "someuser", want: "someuser@iplantcollaborative.org\n"},
		{name: "already-suffixed left alone", submitter: "someuser@iplantcollaborative.org", want: "someuser@iplantcollaborative.org\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := testSpec()
			spec.Submitter = tt.submitter
			cm := testConfig().PermissionsConfigMap(spec)
			assert.Equal(t, "permissions-external-1", cm.Name)
			assert.Equal(t, tt.want, cm.Data[constants.PermissionsFileName])
		})
	}
}

func TestInputPathListConfigMap(t *testing.T) {
	cfg := testConfig()
	spec := testSpec()
	spec.InputPathListPaths = []string{"/iplant/home/someuser/in1.txt", "/iplant/home/someuser/in2/"}

	cm := cfg.InputPathListConfigMap(spec)
	require.NotNil(t, cm)
	assert.Equal(t, "input-path-list-external-1", cm.Name)
	want := cfg.InputPathListIdentifier + "\n/iplant/home/someuser/in1.txt\n/iplant/home/someuser/in2/\n"
	assert.Equal(t, want, cm.Data[constants.InputPathListFileName])
}

func TestService(t *testing.T) {
	svc := testConfig().Service(testSpec())
	assert.Equal(t, "vice-external-1", svc.Name)
	assert.Equal(t, "external-1", svc.Spec.Selector[constants.ExternalIDLabel])
	require.Len(t, svc.Spec.Ports, 2)
	assert.Equal(t, constants.VICEProxyServicePort, svc.Spec.Ports[1].Port)
}

func TestPodDisruptionBudget(t *testing.T) {
	pdb := testConfig().PodDisruptionBudget(testSpec())
	assert.Equal(t, "external-1", pdb.Name)
	assert.Equal(t, int32(0), pdb.Spec.MaxUnavailable.IntVal)
	assert.Equal(t, "external-1", pdb.Spec.Selector.MatchLabels[constants.ExternalIDLabel])
}
