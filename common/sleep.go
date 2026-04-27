package common

import (
	"context"
	"time"
)

// SleepCtx waits for d or until ctx is canceled. Returns true if d elapsed
// normally, false if ctx canceled first — lets callers use it as a loop
// guard without checking ctx separately.
func SleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
