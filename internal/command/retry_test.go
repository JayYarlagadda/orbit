package command

import (
	"math/rand"
	"testing"
	"time"
)

func TestRetryPolicyNextAttemptUsesFullJitterWithFixedSeed(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts: 5,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    800 * time.Millisecond,
	}
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(7))

	first := policy.NextAttemptAt(1, now, rng)
	if delay := first.Sub(now); delay < 0 || delay > 100*time.Millisecond {
		t.Fatalf("first delay = %s, want in [0, 100ms]", delay)
	}

	second := policy.NextAttemptAt(2, now, rng)
	if delay := second.Sub(now); delay < 0 || delay > 200*time.Millisecond {
		t.Fatalf("second delay = %s, want in [0, 200ms]", delay)
	}

	third := policy.NextAttemptAt(3, now, rng)
	if delay := third.Sub(now); delay < 0 || delay > 400*time.Millisecond {
		t.Fatalf("third delay = %s, want in [0, 400ms]", delay)
	}

	capped := policy.NextAttemptAt(10, now, rng)
	if delay := capped.Sub(now); delay < 0 || delay > 800*time.Millisecond {
		t.Fatalf("capped delay = %s, want in [0, 800ms]", delay)
	}
}

func TestRetryPolicyExhausted(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Minute}
	if policy.Exhausted(2) {
		t.Fatal("attempt 2 should not be exhausted with max 3")
	}
	if !policy.Exhausted(3) {
		t.Fatal("attempt 3 should be exhausted with max 3")
	}
}
