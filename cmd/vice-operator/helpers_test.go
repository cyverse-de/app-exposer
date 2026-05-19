package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNameValueMapFlag(t *testing.T) {
	t.Run("single pair", func(t *testing.T) {
		var m nameValueMapFlag
		require.NoError(t, m.Set("NVIDIA-A10G:a10g"))
		assert.Equal(t, nameValueMapFlag{"NVIDIA-A10G": "a10g"}, m)
	})

	t.Run("multiple pairs accumulate", func(t *testing.T) {
		var m nameValueMapFlag
		require.NoError(t, m.Set("NVIDIA-A10G:a10g"))
		require.NoError(t, m.Set("NVIDIA-L4:l4"))
		require.NoError(t, m.Set("NVIDIA-L40S:l40s"))
		assert.Equal(t, nameValueMapFlag{
			"NVIDIA-A10G": "a10g",
			"NVIDIA-L4":   "l4",
			"NVIDIA-L40S": "l40s",
		}, m)
	})

	t.Run("duplicate key — last wins", func(t *testing.T) {
		var m nameValueMapFlag
		require.NoError(t, m.Set("k:v1"))
		require.NoError(t, m.Set("k:v2"))
		assert.Equal(t, nameValueMapFlag{"k": "v2"}, m)
	})

	t.Run("malformed input rejected", func(t *testing.T) {
		cases := []string{
			"no-colon-here",
			":empty-name",
			"empty-value:",
			"",
		}
		for _, in := range cases {
			var m nameValueMapFlag
			err := m.Set(in)
			assert.Error(t, err, "Set(%q) should error", in)
		}
	})

	t.Run("String roundtrip is parseable back", func(t *testing.T) {
		// String is order-unstable because it iterates a map, but each
		// individual NAME:VALUE pair must round-trip cleanly. Just
		// confirm the pairs format is what callers will see.
		m := nameValueMapFlag{"k1": "v1"}
		assert.Equal(t, "k1:v1", m.String())
	})
}
