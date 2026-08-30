package heartbeat

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrTimeout = errors.New("heartbeat timeout")

const (
	DefaultInterval = 5 * time.Second
	DefaultTimeout  = 15 * time.Second
)

type Settings struct {
	Interval time.Duration
	Timeout  time.Duration
}

func Default() Settings {
	return Settings{Interval: DefaultInterval, Timeout: DefaultTimeout}
}

func (s Settings) Validate() error {
	if s.Interval < 10*time.Millisecond || s.Interval > time.Minute {
		return errors.New("heartbeat interval must be between 10ms and 1m")
	}
	if s.Timeout < 100*time.Millisecond || s.Timeout > 5*time.Minute {
		return errors.New("heartbeat timeout must be between 100ms and 5m")
	}
	if s.Timeout < s.Interval {
		return errors.New("heartbeat timeout must not be below the interval")
	}
	return nil
}

// Watch records the last time any stream traffic arrived. Callers treat a
// timeout as a dead peer rather than waiting on a blocked Recv forever.
type Watch struct {
	timeout time.Duration
	mu      sync.Mutex
	last    time.Time
}

func NewWatch(timeout time.Duration) *Watch {
	return &Watch{timeout: timeout, last: time.Now()}
}

func (w *Watch) Touch() {
	w.mu.Lock()
	w.last = time.Now()
	w.mu.Unlock()
}

func (w *Watch) Remaining() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	elapsed := time.Since(w.last)
	if elapsed >= w.timeout {
		return 0
	}
	return w.timeout - elapsed
}

// WatchContext cancels with ErrTimeout when no Touch happens within the watch
// timeout. Any other traffic should call Touch so a live but quiet Recv is not
// mistaken for a dead peer.
func WatchContext(parent context.Context, watch *Watch) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		for {
			if parent.Err() != nil {
				cancel(parent.Err())
				return
			}
			remaining := watch.Remaining()
			if remaining == 0 {
				cancel(ErrTimeout)
				return
			}
			timer := time.NewTimer(remaining)
			select {
			case <-parent.Done():
				timer.Stop()
				cancel(parent.Err())
				return
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return ctx, func() { cancel(nil) }
}
