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
// (spec-unaware) bundle path refuses to build rather than silently leak a
// sensitive value into a plain container Env var — see the comment on
// BuildAnalysisBundle for why this is a hard refusal rather than a silent
// drop or best-effort inclusion.
func TestBuildAnalysisBundleRefusesSensitiveEnvironment(t *testing.T) {
	job := goldenJob(false)
	job.Steps[0].SensitiveEnvironment = map[string]string{"API_KEY": "sekrit"}

	i := &Incluster{}
	_, err := i.BuildAnalysisBundle(context.Background(), job, constants.AnalysisID(job.ID))
	require.Error(t, err, "legacy bundle must refuse jobs with sensitive environment variables")
	assert.Contains(t, err.Error(), "sensitive environment")
}
