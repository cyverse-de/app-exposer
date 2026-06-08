package vicebuild

import (
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
)

// BuildLabels assembles the fixed label set stamped on every resource for an
// analysis. It is the pure half of the old incluster jobinfo.JobLabels: the
// label assembly stays here, while the one DB-derived value (the user's login
// IP) is resolved app-exposer-side and arrives as spec.UserLoginIP. Splitting
// it this way is what removes vicebuild's dependency on the apps/DB layer.
//
// The analysis-id label is the cleanup key, so every builder labels its objects
// with this set; the operator thereby guarantees the cleanup invariant by
// construction rather than validating it after the fact.
func BuildLabels(spec *operatorclient.VICESpec) map[string]string {
	return map[string]string{
		constants.ExternalIDLabel:   string(spec.ExternalID),
		constants.AnalysisIDLabel:   string(spec.AnalysisID),
		constants.AppNameLabel:      common.LabelValueString(spec.AppName),
		constants.AppIDLabel:        spec.AppID,
		constants.UsernameLabel:     common.LabelValueString(spec.Submitter),
		constants.UserIDLabel:       spec.UserID,
		constants.AnalysisNameLabel: common.LabelValueString(truncateAnalysisName(spec.JobName)),
		constants.AppTypeLabel:      string(constants.Interactive),
		constants.SubdomainLabel:    common.Subdomain(spec.UserID, string(spec.ExternalID)),
		constants.LoginIPLabel:      spec.UserLoginIP,
	}
}

// truncateAnalysisName clamps the analysis name to the 63-rune label limit,
// matching jobinfo.JobLabels. Validation guarantees JobName is non-empty.
func truncateAnalysisName(name string) string {
	runes := []rune(name)
	if len(runes) > 63 {
		runes = runes[:63]
	}
	return string(runes)
}
