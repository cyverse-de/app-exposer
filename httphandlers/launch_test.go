package httphandlers

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewLaunchSemaphoreCapacity verifies the constructor's cap handling.
// A zero or negative value must fall back to DefaultMaxConcurrentLaunches
// so misconfiguration doesn't ship a zero-capacity (deadlocking) channel.
func TestNewLaunchSemaphoreCapacity(t *testing.T) {
	tests := []struct {
		name    string
		in      int
		wantCap int
	}{
		{"zero uses default", 0, DefaultMaxConcurrentLaunches},
		{"negative uses default", -1, DefaultMaxConcurrentLaunches},
		{"explicit positive is honored", 7, 7},
		{"large explicit is honored", 500, 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(nil, nil, nil, nil, nil, tt.in)
			assert.Equal(t, tt.wantCap, cap(h.launchSemaphore))
		})
	}
}

// TestAcquireLaunchSlotSucceeds covers the fast path: a fresh handler has
// N free slots, so the first N acquires return ok=true with a working
// release callback. The release callback, when invoked, frees the slot.
func TestAcquireLaunchSlotSucceeds(t *testing.T) {
	h := New(nil, nil, nil, nil, nil, 2)

	release1, ok := h.acquireLaunchSlot()
	require.True(t, ok)
	require.NotNil(t, release1)

	release2, ok := h.acquireLaunchSlot()
	require.True(t, ok)
	require.NotNil(t, release2)

	assert.Equal(t, 2, len(h.launchSemaphore), "both slots should be held")

	release1()
	release2()
	assert.Equal(t, 0, len(h.launchSemaphore), "release should drain the semaphore")
}

// TestAcquireLaunchSlotTimesOut covers the saturation case. With the
// semaphore full, a subsequent acquire must block until launchAcquireTimeout
// elapses and then return (nil, false). We cap the test's wall-clock by
// temporarily shrinking launchAcquireTimeout via the exported indirection
// pattern — can't monkey-patch a const, so swap it with a var on the test
// instead (see launchAcquireTimeoutForTest).
func TestAcquireLaunchSlotTimesOut(t *testing.T) {
	// Preserve and restore the real timeout so parallel tests aren't affected.
	orig := launchAcquireTimeoutForTest
	launchAcquireTimeoutForTest = 50 * time.Millisecond
	t.Cleanup(func() { launchAcquireTimeoutForTest = orig })

	h := New(nil, nil, nil, nil, nil, 1)
	h.launchSemaphore <- struct{}{} // saturate

	start := time.Now()
	release, ok := h.acquireLaunchSlot()
	elapsed := time.Since(start)

	assert.False(t, ok, "saturated semaphore must fail to acquire")
	assert.Nil(t, release)
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "must wait at least the timeout")
	assert.Less(t, elapsed, 500*time.Millisecond, "must not wait indefinitely")
}

// TestAcquireLaunchSlotRecoversAfterRelease ensures the semaphore is usable
// again once the holder releases its slot — guards against a regression where
// the release callback accidentally leaves the channel in a broken state.
func TestAcquireLaunchSlotRecoversAfterRelease(t *testing.T) {
	h := New(nil, nil, nil, nil, nil, 1)

	release, ok := h.acquireLaunchSlot()
	require.True(t, ok)

	// A second acquire would block; run it in a goroutine after releasing.
	var wg sync.WaitGroup
	wg.Add(1)
	var secondOK bool
	go func() {
		defer wg.Done()
		_, secondOK = h.acquireLaunchSlot()
	}()

	release()
	wg.Wait()
	assert.True(t, secondOK, "acquire must succeed after the prior slot is released")
}
