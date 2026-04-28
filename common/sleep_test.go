package common

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSleepCtx(t *testing.T) {
	t.Run("returns true when duration elapses", func(t *testing.T) {
		start := time.Now()
		assert.True(t, SleepCtx(t.Context(), 20*time.Millisecond))
		assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
	})

	t.Run("returns false when context already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		start := time.Now()
		assert.False(t, SleepCtx(ctx, time.Hour))
		// Should return promptly — definitely not wait the full hour.
		assert.Less(t, time.Since(start), 100*time.Millisecond)
	})

	t.Run("returns false when context cancels mid-wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
		start := time.Now()
		assert.False(t, SleepCtx(ctx, time.Hour))
		assert.Less(t, time.Since(start), 100*time.Millisecond)
	})
}
