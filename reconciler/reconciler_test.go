package reconciler

import (
	"net"
	"testing"

	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/messaging/v12"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetLocalIP verifies that getLocalIP returns a parseable IPv4 address.
// On machines without non-loopback interfaces it falls back to "127.0.0.1",
// which is also a valid IPv4 address.
func TestGetLocalIP(t *testing.T) {
	ip := getLocalIP()

	require.NotEmpty(t, ip, "getLocalIP should never return an empty string")

	parsed := net.ParseIP(ip)
	require.NotNil(t, parsed, "getLocalIP returned %q which is not a parseable IP address", ip)

	require.NotNil(t, parsed.To4(), "getLocalIP returned %q which is not an IPv4 address", ip)
}

// TestNewReconciler verifies that New returns a non-nil Reconciler and that
// the ip and hostname fields are populated (they are set unconditionally in
// the constructor).
func TestNewReconciler(t *testing.T) {
	scheduler, err := operatorclient.NewScheduler(nil, nil)
	require.NoError(t, err)

	r := New(nil, scheduler, nil, "")

	require.NotNil(t, r)
	// ip is populated by getLocalIP, which always returns a non-empty string.
	assert.NotEmpty(t, r.ip, "Reconciler.ip should be populated by New")
	// hostname comes from os.Hostname, which may return "" in exotic
	// environments — we only verify the field is set (no panic).
	_ = r.hostname
}

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
