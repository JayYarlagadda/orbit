package heartbeat

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidateRejectsTimeoutBelowInterval(t *testing.T) {
	settings := Settings{Interval: time.Second, Timeout: 100 * time.Millisecond}
	if err := settings.Validate(); err == nil {
		t.Fatal("Validate() accepted a timeout below the interval")
	}
}

func TestWatchContextCancelsAfterSilence(t *testing.T) {
	watch := NewWatch(25 * time.Millisecond)
	ctx, stop := WatchContext(context.Background(), watch)
	defer stop()
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), ErrTimeout) {
			t.Fatalf("cause = %v, want ErrTimeout", context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("WatchContext did not cancel")
	}
}

func TestWatchExpiresAfterTimeout(t *testing.T) {
	watch := NewWatch(30 * time.Millisecond)
	if remaining := watch.Remaining(); remaining <= 0 || remaining > 30*time.Millisecond {
		t.Fatalf("Remaining() = %s", remaining)
	}
	time.Sleep(40 * time.Millisecond)
	if remaining := watch.Remaining(); remaining != 0 {
		t.Fatalf("Remaining() after sleep = %s, want 0", remaining)
	}
	watch.Touch()
	if remaining := watch.Remaining(); remaining <= 0 {
		t.Fatal("Touch() did not restart the timeout")
	}
}
