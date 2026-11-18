package incluster

import (
	"context"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildVICESpecCarriesSensitiveEnvironment confirms buildVICESpec passes
// Step.SensitiveEnvironment through to VICESpec.SensitiveEnvironment
// unmodified — the chokepoint that lets the vicebuild Secret-based envFrom
// path (see vicebuild/secrets.go) actually see the values apps marked
// is_sensitive on. Environment and SensitiveEnvironment must stay disjoint
// per VICESpec.Validate.
func TestBuildVICESpecCarriesSensitiveEnvironment(t *testing.T) {
	job := goldenJob(false)
	job.Steps[0].SensitiveEnvironment = map[string]string{"API_KEY": "sekrit"}

	spec, err := buildVICESpec(job, constants.AnalysisID(job.ID), "10.0.0.1")
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"API_KEY": "sekrit"}, spec.SensitiveEnvironment)
	assert.Equal(t, map[string]string{"FOO": "bar"}, spec.Environment)
	assert.NoError(t, spec.Validate(), "disjoint Environment/SensitiveEnvironment keys must validate")
}

// TestBuildAnalysisBundleRefusesSensitiveEnvironment confirms the legacy
// (spec-unaware) bundle path refuses to build a job with sensitive environment
// variables. The legacy bundle has no Secret/envFrom support, so its only ways
// to handle them would be to inline them as plain container Env (readable by
// anyone who can read the Deployment, and persisted in it) or to silently drop
// them (launching an analysis missing configuration it needs). Both are worse
// than failing loudly. The scheduler skips spec-unaware operators for these
// analyses (VICESpec.RequiresSpecPath), so this refusal is only a backstop.
func TestBuildAnalysisBundleRefusesSensitiveEnvironment(t *testing.T) {
	job := goldenJob(false)
	job.Steps[0].SensitiveEnvironment = map[string]string{"API_KEY": "sekrit"}

	i := &Incluster{}
	_, err := i.BuildAnalysisBundle(context.Background(), job, constants.AnalysisID(job.ID))
	require.Error(t, err, "legacy bundle must refuse jobs with sensitive environment variables")
	assert.Contains(t, err.Error(), "sensitive environment")
}
