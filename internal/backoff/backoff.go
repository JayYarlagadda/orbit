// Package backoff provides the bounded reconnect policy shared by the
// reference client and the gateway control stream.
package backoff

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Policy produces exponentially increasing, jittered delays bounded by a
// ceiling. It is not safe for concurrent use; each reconnect loop owns one.
type Policy struct {
	initial  time.Duration
	maximum  time.Duration
	current  time.Duration
	attempts int
}

// New returns a policy that starts at initial and never waits longer than
// maximum. A maximum below initial would make the delay shrink as failures
// accumulate, so it is rejected.
func New(initial, maximum time.Duration) (*Policy, error) {
	if initial <= 0 {
		return nil, errors.New("initial backoff delay must be positive")
	}
	if maximum < initial {
		return nil, errors.New("maximum backoff delay must not be below the initial delay")
	}
	return &Policy{initial: initial, maximum: maximum, current: initial}, nil
}

// Attempts reports how many delays have been handed out since the last Reset.
func (p *Policy) Attempts() int { return p.attempts }

// Reset returns the policy to its initial delay. Callers use this after an
// endpoint stays healthy long enough to count as recovered, so the next outage
// restarts from the initial delay rather than the ceiling.
func (p *Policy) Reset() {
	p.current = p.initial
	p.attempts = 0
}

// Next returns the delay to wait before the next attempt and advances the
// policy, doubling the underlying delay up to the ceiling.
func (p *Policy) Next() time.Duration {
	p.attempts++
	wait := jitter(p.current)
	if p.current = p.current * 2; p.current > p.maximum || p.current <= 0 {
		p.current = p.maximum
	}
	return wait
}

// jitter spreads retries across the upper half of the delay window so that many
// peers recovering from a single outage do not retry in lockstep.
func jitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}

// Wait blocks for delay, returning early if ctx is cancelled first.
func Wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
