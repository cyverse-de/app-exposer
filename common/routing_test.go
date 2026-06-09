package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSubdomain(t *testing.T) {
	tests := []struct {
		name         string
		userID       string
		invocationID string
		expected     string
	}{
		{
			name:         "produces 9-character result",
			userID:       "user1",
			invocationID: "inv1",
			expected:     "a95291fac",
		},
		{
			name:         "different invocation IDs produce different subdomains",
			userID:       "user1",
			invocationID: "inv2",
			expected:     "acf515ff8",
		},
		{
			name:         "different user IDs produce different subdomains",
			userID:       "user2",
			invocationID: "inv1",
			expected:     "a65354440",
		},
		{
			name:         "empty inputs produce deterministic result",
			userID:       "",
			invocationID: "",
			expected:     "ae3b0c442",
		},
		{
			name:         "realistic username and UUID invocation ID",
			userID:       "user@iplantcollaborative.org",
			invocationID: "550e8400-e29b-41d4-a716-446655440000",
			expected:     "a6d7946c9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Subdomain(tt.userID, tt.invocationID)
			assert.Equal(t, tt.expected, result)
			assert.Len(t, result, 9, "subdomain must always be exactly 9 characters")
			assert.Equal(t, "a", string(result[0]), "subdomain must always start with 'a'")
		})
	}
}
