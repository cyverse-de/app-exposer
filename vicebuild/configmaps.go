package vicebuild

import (
	"strings"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExcludesConfigMap builds the ConfigMap listing paths porklock must not upload
// to iRODS. Always present.
func (c *Config) ExcludesConfigMap(spec *operatorclient.VICESpec) *apiv1.ConfigMap {
	return &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   excludesConfigMapName(spec),
			Labels: BuildLabels(spec),
		},
		Data: map[string]string{
			constants.ExcludesFileName: linesWithTrailingNewline(spec.ExcludeArguments),
		},
	}
}

// PermissionsConfigMap builds the owner-only allowed-users ConfigMap. The
// suffix is appended to match the form stored in the Keycloak JWT
// preferred_username; this folds in the operator's EnsurePermissionsConfigMap
// fallback by always building the ConfigMap here.
func (c *Config) PermissionsConfigMap(spec *operatorclient.VICESpec) *apiv1.ConfigMap {
	owner := spec.Submitter
	if !strings.HasSuffix(owner, c.UserSuffix) {
		owner += c.UserSuffix
	}
	return &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   permissionsConfigMapName(spec),
			Labels: BuildLabels(spec),
		},
		Data: map[string]string{
			constants.PermissionsFileName: owner + "\n",
		},
	}
}

// InputPathListConfigMap builds the porklock input-path-list ConfigMap. The
// first line is the cluster's list-format identifier; the remaining lines are
// the resolved ticketless input paths. Built unconditionally (matching the
// legacy bundle); it is only referenced by a volume when there are ticketless
// inputs.
func (c *Config) InputPathListConfigMap(spec *operatorclient.VICESpec) *apiv1.ConfigMap {
	lines := append([]string{c.InputPathListIdentifier}, spec.InputPathListPaths...)
	return &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   inputPathListConfigMapName(spec),
			Labels: BuildLabels(spec),
		},
		Data: map[string]string{
			constants.InputPathListFileName: linesWithTrailingNewline(lines),
		},
	}
}

// linesWithTrailingNewline joins lines with "\n" and appends a trailing newline,
// matching the per-line fmt.Fprintf("%s\n", …) the incluster builders used.
// An empty slice yields the empty string.
func linesWithTrailingNewline(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}
