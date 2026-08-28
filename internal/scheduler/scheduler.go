package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/JayYarlagadda/orbit/internal/command"
)

type Store interface {
	SweepExpiredLeases(context.Context, int, string) (int, error)
	SweepExpiredCommands(context.Context, int, string) (int, error)
	LeaseNext(context.Context, command.LeaseRequest, string) ([]command.Lease, error)
}

type Dispatcher interface {
	Dispatch(context.Context, command.Lease) error
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type TickerFactory func(time.Duration) Ticker
type CorrelationFactory func() (string, error)
type ErrorHandler func(error)

type Config struct {
	GatewayID     string
	BatchSize     int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	SweepLimit    int
}

type CycleResult struct {
	ExpiredLeases   int
	ExpiredCommands int
	Leased          int
	Dispatched      int
}

type Scheduler struct {
	store          Store
	dispatcher     Dispatcher
	config         Config
	newTicker      TickerFactory
	newCorrelation CorrelationFactory
}

func New(
	store Store,
	dispatcher Dispatcher,
	config Config,
	newTicker TickerFactory,
	newCorrelation CorrelationFactory,
) (*Scheduler, error) {
	if store == nil || dispatcher == nil {
		return nil, errors.New("scheduler store and dispatcher are required")
	}
	if _, err := command.NewLeaseRequest(config.GatewayID, config.BatchSize, config.LeaseDuration); err != nil {
		return nil, err
	}
	if config.PollInterval < 10*time.Millisecond || config.PollInterval > time.Minute {
		return nil, fmt.Errorf("poll interval must be between 10ms and 1m")
	}
	if config.SweepLimit < 1 || config.SweepLimit > command.MaxLeaseBatchSize {
		return nil, fmt.Errorf("sweep limit must be between 1 and %d", command.MaxLeaseBatchSize)
	}
	if newTicker == nil {
		newTicker = func(duration time.Duration) Ticker { return &systemTicker{Ticker: time.NewTicker(duration)} }
	}
	if newCorrelation == nil {
		newCorrelation = randomCorrelation
	}
	return &Scheduler{
		store:          store,
		dispatcher:     dispatcher,
		config:         config,
		newTicker:      newTicker,
		newCorrelation: newCorrelation,
	}, nil
}

func (s *Scheduler) Run(ctx context.Context, onError ErrorHandler) {
	if onError == nil {
		onError = func(error) {}
	}
	runCycle := func() {
		if _, err := s.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			onError(err)
		}
	}
	runCycle()

	ticker := s.newTicker(s.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			runCycle()
		}
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) (CycleResult, error) {
	if err := ctx.Err(); err != nil {
		return CycleResult{}, err
	}
	sweepCorrelation, err := s.newCorrelation()
	if err != nil {
		return CycleResult{}, fmt.Errorf("create sweep correlation ID: %w", err)
	}
	swept, err := s.store.SweepExpiredLeases(ctx, s.config.SweepLimit, sweepCorrelation)
	if err != nil {
		return CycleResult{}, fmt.Errorf("sweep expired leases: %w", err)
	}

	// Runs after the lease sweep so a command that lost its lease and also
	// outlived its TTL reaches EXPIRED in the same cycle, and before leasing so
	// that successors it was blocking become eligible immediately.
	expiryCorrelation, err := s.newCorrelation()
	if err != nil {
		return CycleResult{ExpiredLeases: swept}, fmt.Errorf("create expiry correlation ID: %w", err)
	}
	expired, err := s.store.SweepExpiredCommands(ctx, s.config.SweepLimit, expiryCorrelation)
	if err != nil {
		return CycleResult{ExpiredLeases: swept}, fmt.Errorf("sweep expired commands: %w", err)
	}

	leaseCorrelation, err := s.newCorrelation()
	if err != nil {
		return CycleResult{ExpiredLeases: swept, ExpiredCommands: expired}, fmt.Errorf("create lease correlation ID: %w", err)
	}
	request, err := command.NewLeaseRequest(s.config.GatewayID, s.config.BatchSize, s.config.LeaseDuration)
	if err != nil {
		return CycleResult{ExpiredLeases: swept, ExpiredCommands: expired}, err
	}
	leases, err := s.store.LeaseNext(ctx, request, leaseCorrelation)
	if err != nil {
		return CycleResult{ExpiredLeases: swept, ExpiredCommands: expired}, fmt.Errorf("lease next commands: %w", err)
	}
	result := CycleResult{ExpiredLeases: swept, ExpiredCommands: expired, Leased: len(leases)}
	for _, lease := range leases {
		if err := s.dispatcher.Dispatch(ctx, lease); err != nil {
			return result, fmt.Errorf("dispatch command %s: %w", lease.Command.ID, err)
		}
		result.Dispatched++
	}
	return result, nil
}

type systemTicker struct{ *time.Ticker }

func (t *systemTicker) C() <-chan time.Time { return t.Ticker.C }

func randomCorrelation() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
