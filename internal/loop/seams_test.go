package loop

import (
	"context"
	"testing"
	"time"
)

// defaultClock.Sleep must honor ctx cancellation so a Ctrl-C during a
// long backoff returns promptly instead of stranding the loop
// (byob-lifecycle.2).
func TestDefaultClockSleepReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	defaultClock{}.Sleep(ctx, time.Hour)
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Errorf("Sleep(ctx, 1h) blocked %v after cancel; expected prompt return", elapsed)
	}
}

// defaultClock.Sleep returns immediately for non-positive durations
// without arming a timer.
func TestDefaultClockSleepZeroDuration(t *testing.T) {
	start := time.Now()
	defaultClock{}.Sleep(context.Background(), 0)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("Sleep(ctx, 0) took %v; expected ~immediate", elapsed)
	}
}

// defaultClock.Sleep waits the full duration when ctx is not cancelled.
// Uses a short interval so the test stays fast.
func TestDefaultClockSleepElapsesWithoutCancel(t *testing.T) {
	start := time.Now()
	defaultClock{}.Sleep(context.Background(), 20*time.Millisecond)
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Errorf("Sleep(ctx, 20ms) returned after %v; expected >= 20ms", elapsed)
	}
}
