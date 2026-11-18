package vicebuild

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildBundleSensitiveEnvSecret confirms BuildBundle wires the
// sensitive-env Secret only when the analysis actually requested one, so a
// bundle without SensitiveEnvironment never ships a spurious empty Secret.
func TestBuildBundleSensitiveEnvSecret(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		bundle, err := testConfig().BuildBundle(testSpec())
		require.NoError(t, err)
		assert.Nil(t, bundle.SensitiveEnvSecret)
	})

	t.Run("present", func(t *testing.T) {
		spec := testSpec()
		spec.SensitiveEnvironment = map[string]string{"OPENAI_API_KEY": "sk-not-a-real-key"}

		bundle, err := testConfig().BuildBundle(spec)
		require.NoError(t, err)
		require.NotNil(t, bundle.SensitiveEnvSecret)
		assert.Equal(t, "sensitive-env-external-1", bundle.SensitiveEnvSecret.Name)
	})
}
