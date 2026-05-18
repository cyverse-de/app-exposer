package common

import (
	"testing"

	"github.com/cyverse-de/messaging/v12"
	"github.com/stretchr/testify/assert"
)

func TestMapPodPhaseToStatus(t *testing.T) {
	tests := []struct {
		phase    string
		expected messaging.JobState
	}{
		{"Pending", messaging.SubmittedState},
		{"Running", messaging.RunningState},
		{"Succeeded", messaging.SucceededState},
		{"Failed", messaging.FailedState},
		{"Unknown", messaging.SubmittedState},
		{"", messaging.SubmittedState},
	}

	for _, tt := range tests {
		t.Run(tt.phase, func(t *testing.T) {
			assert.Equal(t, tt.expected, MapPodPhaseToStatus(tt.phase))
		})
	}
}
