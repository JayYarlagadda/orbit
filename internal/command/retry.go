package command

import (
	"errors"
	"math/rand"
	"time"
)

const (
	DefaultMaxDeliveryAttempts = int32(5)
	DefaultRetryBaseDelay      = 250 * time.Millisecond
	DefaultRetryMaxDelay       = 30 * time.Second
)

// RetryPolicy schedules the next lease attempt after a delivery failure.
type RetryPolicy struct {
	MaxAttempts int32
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: DefaultMaxDeliveryAttempts,
		BaseDelay:   DefaultRetryBaseDelay,
		MaxDelay:    DefaultRetryMaxDelay,
	}
}

func (p RetryPolicy) Validate() error {
	if p.MaxAttempts < 1 || p.MaxAttempts > 100 {
		return errors.New("max delivery attempts must be between 1 and 100")
	}
	if p.BaseDelay < 10*time.Millisecond || p.BaseDelay > time.Minute {
		return errors.New("retry base delay must be between 10ms and 1m")
	}
	if p.MaxDelay < time.Second || p.MaxDelay > 30*time.Minute {
		return errors.New("retry max delay must be between 1s and 30m")
	}
	if p.MaxDelay < p.BaseDelay {
		return errors.New("retry max delay must not be below the base delay")
	}
	return nil
}

// Exhausted reports whether attemptCount consumed the delivery retry budget.
func (p RetryPolicy) Exhausted(attemptCount int32) bool {
	return attemptCount >= p.MaxAttempts
}

// NextAttemptAt returns when a command may be leased again after attemptCount
// failures, using capped exponential backoff with full jitter:
//
//	cap = min(max_delay, base_delay * 2^(attemptCount-1))
//	delay = uniform(0, cap)
func (p RetryPolicy) NextAttemptAt(attemptCount int32, now time.Time, rng *rand.Rand) time.Time {
	if rng == nil {
		rng = rand.New(rand.NewSource(now.UnixNano()))
	}
	cap := p.capDelay(attemptCount)
	if cap <= 0 {
		return now
	}
	return now.Add(time.Duration(rng.Int63n(int64(cap) + 1)))
}

func (p RetryPolicy) capDelay(attemptCount int32) time.Duration {
	if attemptCount < 1 {
		return p.BaseDelay
	}
	cap := p.BaseDelay
	for exponent := int32(1); exponent < attemptCount; exponent++ {
		if cap >= p.MaxDelay {
			return p.MaxDelay
		}
		cap *= 2
	}
	if cap > p.MaxDelay {
		return p.MaxDelay
	}
	return cap
}
