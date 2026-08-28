package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JayYarlagadda/orbit/internal/command"
)

type fakeStore struct {
	swept        int
	sweepErr     error
	expired      int
	expiryErr    error
	leases       []command.Lease
	leaseErr     error
	leaseRequest command.LeaseRequest
	correlations []string
	calls        []string
}

func (s *fakeStore) SweepExpiredLeases(_ context.Context, _ int, correlation string) (int, error) {
	s.correlations = append(s.correlations, correlation)
	s.calls = append(s.calls, "sweep-leases")
	return s.swept, s.sweepErr
}

func (s *fakeStore) SweepExpiredCommands(_ context.Context, _ int, correlation string) (int, error) {
	s.correlations = append(s.correlations, correlation)
	s.calls = append(s.calls, "sweep-commands")
	return s.expired, s.expiryErr
}

func (s *fakeStore) LeaseNext(_ context.Context, request command.LeaseRequest, correlation string) ([]command.Lease, error) {
	s.leaseRequest = request
	s.correlations = append(s.correlations, correlation)
	s.calls = append(s.calls, "lease")
	return s.leases, s.leaseErr
}

type fakeDispatcher struct {
	dispatched []command.Lease
	failAt     int
}

func (d *fakeDispatcher) Dispatch(_ context.Context, lease command.Lease) error {
	if d.failAt >= 0 && len(d.dispatched) == d.failAt {
		return errors.New("stream closed")
	}
	d.dispatched = append(d.dispatched, lease)
	return nil
}

func TestRunOnce(t *testing.T) {
	leases := []command.Lease{
		{Command: command.Command{ID: "command-1"}, SessionEpoch: 3},
		{Command: command.Command{ID: "command-2"}, SessionEpoch: 7},
	}
	store := &fakeStore{swept: 1, expired: 3, leases: leases}
	dispatcher := &fakeDispatcher{failAt: -1}
	scheduler := newTestScheduler(t, store, dispatcher)

	got, err := scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if got != (CycleResult{ExpiredLeases: 1, ExpiredCommands: 3, Leased: 2, Dispatched: 2}) {
		t.Fatalf("RunOnce() = %+v", got)
	}
	if len(dispatcher.dispatched) != 2 || dispatcher.dispatched[0].Command.ID != "command-1" {
		t.Fatalf("dispatch order = %+v", dispatcher.dispatched)
	}
	if store.leaseRequest.Owner != "gateway-1" || store.leaseRequest.Limit != 8 {
		t.Fatalf("lease request = %+v", store.leaseRequest)
	}
	if len(store.correlations) != 3 {
		t.Fatalf("correlations = %v", store.correlations)
	}
	unique := make(map[string]struct{}, len(store.correlations))
	for _, correlation := range store.correlations {
		unique[correlation] = struct{}{}
	}
	if len(unique) != len(store.correlations) {
		t.Fatalf("correlations are not distinct: %v", store.correlations)
	}
}

// Expiring commands before leasing is what unblocks successors that an expired
// predecessor was holding behind the per-device ordering guard.
func TestRunOnceExpiresCommandsBeforeLeasing(t *testing.T) {
	store := &fakeStore{}
	scheduler := newTestScheduler(t, store, &fakeDispatcher{failAt: -1})

	if _, err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	want := []string{"sweep-leases", "sweep-commands", "lease"}
	if len(store.calls) != len(want) {
		t.Fatalf("calls = %v", store.calls)
	}
	for i, call := range want {
		if store.calls[i] != call {
			t.Fatalf("calls = %v, want %v", store.calls, want)
		}
	}
}

func TestRunOnceStopsWhenCommandExpirySweepFails(t *testing.T) {
	store := &fakeStore{swept: 2, expiryErr: errors.New("expiry sweep failed")}
	scheduler := newTestScheduler(t, store, &fakeDispatcher{failAt: -1})

	got, err := scheduler.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce() unexpectedly succeeded")
	}
	if got.ExpiredLeases != 2 || got.Leased != 0 {
		t.Fatalf("RunOnce() = %+v", got)
	}
	for _, call := range store.calls {
		if call == "lease" {
			t.Fatalf("leasing ran after a failed expiry sweep: %v", store.calls)
		}
	}
}

func TestRunOnceStopsDispatchAfterFailure(t *testing.T) {
	store := &fakeStore{leases: []command.Lease{
		{Command: command.Command{ID: "command-1"}},
		{Command: command.Command{ID: "command-2"}},
	}}
	dispatcher := &fakeDispatcher{failAt: 1}
	scheduler := newTestScheduler(t, store, dispatcher)

	got, err := scheduler.RunOnce(context.Background())
	if err == nil || got.Leased != 2 || got.Dispatched != 1 {
		t.Fatalf("RunOnce() = (%+v, %v)", got, err)
	}
}

func TestRunOnceHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scheduler := newTestScheduler(t, &fakeStore{}, &fakeDispatcher{failAt: -1})
	if _, err := scheduler.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce() error = %v", err)
	}
}

func TestNewRejectsUnboundedConfiguration(t *testing.T) {
	_, err := New(
		&fakeStore{},
		&fakeDispatcher{failAt: -1},
		Config{GatewayID: "gateway", BatchSize: command.MaxLeaseBatchSize + 1, LeaseDuration: time.Second, PollInterval: time.Second, SweepLimit: 1},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("New() unexpectedly succeeded")
	}
}

func newTestScheduler(t *testing.T, store *fakeStore, dispatcher *fakeDispatcher) *Scheduler {
	t.Helper()
	correlationIndex := 0
	result, err := New(
		store,
		dispatcher,
		Config{GatewayID: "gateway-1", BatchSize: 8, LeaseDuration: 15 * time.Second, PollInterval: 100 * time.Millisecond, SweepLimit: 16},
		nil,
		func() (string, error) {
			correlationIndex++
			return string(rune('a' + correlationIndex)), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
