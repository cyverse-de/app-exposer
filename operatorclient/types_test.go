package operatorclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCapacityResponseHasCapacity(t *testing.T) {
	tests := []struct {
		name           string
		availableSlots int
		want           bool
	}{
		{"unlimited reports true", -1, true},
		{"exhausted reports false", 0, false},
		{"single slot reports true", 1, true},
		{"many slots report true", 42, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CapacityResponse{AvailableSlots: tt.availableSlots}
			assert.Equal(t, tt.want, c.HasCapacity())
		})
	}
}
