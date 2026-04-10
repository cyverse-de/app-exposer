package reconciler

import (
	"testing"

	"github.com/cyverse-de/messaging/v12"
	"github.com/stretchr/testify/assert"
)

func TestMapPodPhaseToStatus(t *testing.T) {
	tests := []struct {
		phase    string
		expected string
	}{
		{"Pending", string(messaging.SubmittedState)},
		{"Running", string(messaging.RunningState)},
		{"Succeeded", string(messaging.SucceededState)},
		{"Failed", string(messaging.FailedState)},
		{"Unknown", string(messaging.SubmittedState)},
		{"", string(messaging.SubmittedState)},
	}

	for _, tt := range tests {
		t.Run(tt.phase, func(t *testing.T) {
			assert.Equal(t, tt.expected, mapPodPhaseToStatus(tt.phase))
		})
	}
}
