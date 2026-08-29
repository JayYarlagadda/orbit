package backoff

import (
	"context"
	"testing"
	"time"
)

func TestNewRejectsNonPositiveInitial(t *testing.T) {
	if _, err := New(0, time.Second); err == nil {
		t.Fatal("New() accepted a zero initial delay")
	}
}

func TestNewRejectsMaximumBelowInitial(t *testing.T) {
	if _, err := New(time.Second, 100*time.Millisecond); err == nil {
		t.Fatal("New() accepted a maximum below the initial delay")
	}
}

func TestNextStaysInUpperHalfAndCaps(t *testing.T) {
	policy, err := New(100*time.Millisecond, 400*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	first := policy.Next()
	if first < 50*time.Millisecond || first > 100*time.Millisecond {
		t.Fatalf("first delay = %s, want in [50ms, 100ms]", first)
	}
	if policy.Attempts() != 1 {
		t.Fatalf("Attempts() = %d, want 1", policy.Attempts())
	}

	second := policy.Next()
	if second < 100*time.Millisecond || second > 200*time.Millisecond {
		t.Fatalf("second delay = %s, want in [100ms, 200ms]", second)
	}

	_ = policy.Next()
	fourth := policy.Next()
	if fourth < 200*time.Millisecond || fourth > 400*time.Millisecond {
		t.Fatalf("capped delay = %s, want in [200ms, 400ms]", fourth)
	}
}

func TestResetRestartsFromInitialDelay(t *testing.T) {
	policy, err := New(40*time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = policy.Next()
	_ = policy.Next()
	policy.Reset()
	if policy.Attempts() != 0 {
		t.Fatalf("Attempts() after Reset = %d, want 0", policy.Attempts())
	}
	wait := policy.Next()
	if wait < 20*time.Millisecond || wait > 40*time.Millisecond {
		t.Fatalf("delay after Reset = %s, want in [20ms, 40ms]", wait)
	}
}

func TestWaitReturnsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	err := Wait(ctx, time.Second)
	if err == nil {
		t.Fatal("Wait() succeeded after cancel")
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatalf("Wait() blocked for %s after cancel", time.Since(started))
	}
}
