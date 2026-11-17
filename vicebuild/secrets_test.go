package vicebuild

import (
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSensitiveEnvSecretNilWhenEmpty(t *testing.T) {
	spec := testSpec()
	secret := testConfig().SensitiveEnvSecret(spec)
	assert.Nil(t, secret, "no Secret should be built when SensitiveEnvironment is empty")
}

func TestSensitiveEnvSecretBuilt(t *testing.T) {
	spec := testSpec()
	spec.SensitiveEnvironment = map[string]string{
		"OPENAI_API_KEY": "sk-not-a-real-key",
	}

	secret := testConfig().SensitiveEnvSecret(spec)
	require.NotNil(t, secret)

	assert.Equal(t, "sensitive-env-external-1", secret.Name)
	assert.Equal(t, "analysis-1", secret.Labels[constants.AnalysisIDLabel],
		"secret must carry the standard per-analysis labels so label-selector cleanup finds it")
	assert.Equal(t, []byte("sk-not-a-real-key"), secret.Data["OPENAI_API_KEY"])
}
