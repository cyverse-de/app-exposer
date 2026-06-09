package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFixUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		suffix   string
		expected string
	}{
		{
			name:     "adds domain suffix when missing",
			username: "jdoe",
			suffix:   "iplantcollaborative.org",
			expected: "jdoe@iplantcollaborative.org",
		},
		{
			name:     "does not duplicate suffix when already present",
			username: "jdoe@iplantcollaborative.org",
			suffix:   "iplantcollaborative.org",
			expected: "jdoe@iplantcollaborative.org",
		},
		{
			name:     "accepts suffix with leading @ and adds it correctly",
			username: "jdoe",
			suffix:   "@iplantcollaborative.org",
			expected: "jdoe@iplantcollaborative.org",
		},
		{
			name:     "does not duplicate suffix when suffix has leading @ and username already has it",
			username: "jdoe@iplantcollaborative.org",
			suffix:   "@iplantcollaborative.org",
			expected: "jdoe@iplantcollaborative.org",
		},
		{
			name:     "handles username that ends with a different domain",
			username: "jdoe@otherdomain.org",
			suffix:   "iplantcollaborative.org",
			expected: "jdoe@otherdomain.org@iplantcollaborative.org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, FixUsername(tt.username, tt.suffix))
		})
	}
}
