package shutdownsignal

import (
	"context"
	"os/signal"
)

// NotifyContext cancels when the process is asked to stop. On Windows that
// includes Ctrl+Break so a test (or job object) can interrupt a child that was
// started in its own process group without delivering Ctrl+C to the parent.
func NotifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, signals()...)
}
