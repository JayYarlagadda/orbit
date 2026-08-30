package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/api/commandservice"
	"github.com/JayYarlagadda/orbit/internal/command"
	"github.com/JayYarlagadda/orbit/internal/session"
	"github.com/JayYarlagadda/orbit/internal/storage/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type integrationClock struct{ now time.Time }

func (c integrationClock) Now() time.Time { return c.now }

func TestCommandStoreIntegration(t *testing.T) {
	databaseURL := os.Getenv("ORBIT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORBIT_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	migrationDirectory, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, pool, migrationDirectory, migrate.Up, 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	t.Run("idempotency and terminal cursor", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{})
		firstSubmission := newIntegrationSubmission(t, "producer", "key-1", "device-1", "first", now)

		first, created, err := store.Submit(ctx, firstSubmission, "test", "correlation-1")
		if err != nil || !created {
			t.Fatalf("first Submit() = (%+v, %v, %v)", first, created, err)
		}
		duplicate, created, err := store.Submit(ctx, firstSubmission, "test", "correlation-2")
		if err != nil || created || duplicate.ID != first.ID {
			t.Fatalf("duplicate Submit() = (%+v, %v, %v)", duplicate, created, err)
		}
		conflict := newIntegrationSubmission(t, "producer", "key-1", "device-1", "different", now)
		if _, _, err := store.Submit(ctx, conflict, "test", "correlation-3"); !errors.Is(err, command.ErrIdempotencyConflict) {
			t.Fatalf("conflicting Submit() error = %v", err)
		}

		second, _, err := store.Submit(ctx,
			newIntegrationSubmission(t, "producer", "key-2", "device-1", "second", now),
			"test",
			"correlation-4",
		)
		if err != nil || first.SequenceNumber != 1 || second.SequenceNumber != 2 {
			t.Fatalf("device sequences = (%d, %d), error = %v", first.SequenceNumber, second.SequenceNumber, err)
		}
		if _, err := store.Cancel(ctx, second.ID, "test", "correlation-5"); err != nil {
			t.Fatalf("Cancel(second) error = %v", err)
		}
		assertTerminalCursor(t, pool, "device-1", 0)
		if _, err := store.Cancel(ctx, first.ID, "test", "correlation-6"); err != nil {
			t.Fatalf("Cancel(first) error = %v", err)
		}
		assertTerminalCursor(t, pool, "device-1", 2)
	})

	t.Run("concurrent sequence allocation", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{})
		const count = 16
		sequences := make([]int64, count)
		errorsByIndex := make([]error, count)
		var waitGroup sync.WaitGroup
		for index := range count {
			submission := newIntegrationSubmission(
				t,
				"parallel",
				fmt.Sprintf("key-%d", index),
				"device-parallel",
				fmt.Sprintf("payload-%d", index),
				now,
			)
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				result, _, err := store.Submit(ctx,
					submission,
					"test",
					fmt.Sprintf("correlation-%d", index),
				)
				errorsByIndex[index] = err
				sequences[index] = result.SequenceNumber
			}()
		}
		waitGroup.Wait()
		for index, err := range errorsByIndex {
			if err != nil {
				t.Fatalf("Submit(%d) error = %v", index, err)
			}
		}
		sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
		for index, sequence := range sequences {
			if sequence != int64(index+1) {
				t.Fatalf("sorted sequences = %v", sequences)
			}
		}
	})

	t.Run("ordered lease and stale token fencing", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{})
		for _, device := range []string{"device-1", "device-2"} {
			acquisition, err := session.NewAcquisition(device, "worker-1", "client-"+device)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.AcquireSession(ctx, acquisition); err != nil {
				t.Fatalf("AcquireSession(%s) error = %v", device, err)
			}
		}
		for index, device := range []string{"device-1", "device-1", "device-2"} {
			_, _, err := store.Submit(ctx,
				newIntegrationSubmission(t, "lease-producer", fmt.Sprintf("key-%d", index), device, fmt.Sprintf("payload-%d", index), now),
				"test",
				fmt.Sprintf("submit-%d", index),
			)
			if err != nil {
				t.Fatalf("Submit(%d) error = %v", index, err)
			}
		}
		leaseRequest, err := command.NewLeaseRequest("worker-1", 10, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		leased, err := store.LeaseNext(ctx, leaseRequest, "lease-batch-1")
		if err != nil {
			t.Fatalf("LeaseNext() error = %v", err)
		}
		if len(leased) != 2 {
			t.Fatalf("len(LeaseNext()) = %d, want 2", len(leased))
		}
		byDevice := make(map[string]command.Command, len(leased))
		for _, lease := range leased {
			leasedCommand := lease.Command
			byDevice[leasedCommand.DeviceID] = leasedCommand
			if leasedCommand.SequenceNumber != 1 || leasedCommand.LeaseToken != 1 || leasedCommand.State != command.StateLeased || lease.SessionEpoch != 1 {
				t.Fatalf("leased command = %+v", leasedCommand)
			}
		}
		blocked, err := store.LeaseNext(ctx, leaseRequest, "lease-batch-2")
		if err != nil || len(blocked) != 0 {
			t.Fatalf("blocked LeaseNext() = (%+v, %v)", blocked, err)
		}

		deviceOne := byDevice["device-1"]
		if _, err := store.MarkInFlight(ctx, deviceOne.ID, "worker-1", deviceOne.LeaseToken+1, 1, "stale"); !errors.Is(err, command.ErrStaleLease) {
			t.Fatalf("stale MarkInFlight() error = %v", err)
		}
		inFlight, err := store.MarkInFlight(ctx, deviceOne.ID, "worker-1", deviceOne.LeaseToken, 1, "active")
		if err != nil || inFlight.State != command.StateInFlight {
			t.Fatalf("MarkInFlight() = (%+v, %v)", inFlight, err)
		}

		deviceTwo := byDevice["device-2"]
		newSession, err := session.NewAcquisition("device-2", "worker-1", "client-reconnected")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcquireSession(ctx, newSession); err != nil {
			t.Fatal(err)
		}
		if _, err := store.MarkInFlight(ctx, deviceTwo.ID, "worker-1", deviceTwo.LeaseToken, 1, "stale-session"); !errors.Is(err, command.ErrStaleLease) {
			t.Fatalf("old-session MarkInFlight() error = %v", err)
		}
		if _, err := store.MarkInFlight(ctx, deviceTwo.ID, "worker-1", deviceTwo.LeaseToken, 2, "mismatched-session"); !errors.Is(err, command.ErrStaleLease) {
			t.Fatalf("mismatched-session MarkInFlight() error = %v", err)
		}
		if err := store.ReleaseSession(ctx, "device-2", "worker-1", 1); !errors.Is(err, session.ErrStale) {
			t.Fatalf("old ReleaseSession() error = %v", err)
		}
		if _, err := store.Cancel(ctx, deviceOne.ID, "test", "cancel-after-send"); err == nil {
			t.Fatal("Cancel(in-flight) unexpectedly succeeded")
		}
	})

	t.Run("audit failure rolls back command and cursor", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{})
		_, _, err := store.Submit(ctx,
			newIntegrationSubmission(t, "rollback", "key", "device-rollback", "payload", now),
			"test",
			strings.Repeat("x", 129),
		)
		if err == nil {
			t.Fatal("Submit() unexpectedly succeeded")
		}
		var commands int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM orbit.commands`).Scan(&commands); err != nil {
			t.Fatal(err)
		}
		var cursors int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM orbit.device_cursors`).Scan(&cursors); err != nil {
			t.Fatal(err)
		}
		if commands != 0 || cursors != 0 {
			t.Fatalf("rollback left commands=%d cursors=%d", commands, cursors)
		}
	})

	t.Run("expired lease is recoverable with a higher token", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{})
		acquisition, err := session.NewAcquisition("retry-device", "retry-gateway", "retry-client")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcquireSession(ctx, acquisition); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Submit(ctx,
			newIntegrationSubmission(t, "retry-producer", "retry-key", "retry-device", "payload", now),
			"test",
			"retry-submit",
		); err != nil {
			t.Fatal(err)
		}
		leaseRequest, err := command.NewLeaseRequest("retry-gateway", 1, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		first, err := store.LeaseNext(ctx, leaseRequest, "retry-lease-1")
		if err != nil || len(first) != 1 {
			t.Fatalf("first LeaseNext() = (%+v, %v)", first, err)
		}

		laterStore := NewCommandStore(pool, integrationClock{now: now.Add(2 * time.Second)}, StorePolicy{})
		swept, err := laterStore.SweepExpiredLeases(ctx, 10, "retry-sweep")
		if err != nil || swept != 1 {
			t.Fatalf("SweepExpiredLeases() = (%d, %v)", swept, err)
		}
		retryStore := NewCommandStore(pool, integrationClock{now: now.Add(2*time.Second + command.DefaultRetryMaxDelay)}, StorePolicy{})
		second, err := retryStore.LeaseNext(ctx, leaseRequest, "retry-lease-2")
		if err != nil || len(second) != 1 || second[0].Command.LeaseToken != 2 || second[0].Command.AttemptCount != 2 {
			t.Fatalf("second LeaseNext() = (%+v, %v)", second, err)
		}
		var outcome string
		if err := pool.QueryRow(ctx, `
			SELECT outcome FROM orbit.delivery_attempts
			WHERE command_id = $1 AND lease_token = 1`,
			first[0].Command.ID,
		).Scan(&outcome); err != nil {
			t.Fatal(err)
		}
		if outcome != "LEASE_EXPIRED" {
			t.Fatalf("first attempt outcome = %q", outcome)
		}
	})

	t.Run("acknowledgement is fenced and idempotent", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{})
		acquisition, err := session.NewAcquisition("ack-device", "ack-gateway", "ack-client")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcquireSession(ctx, acquisition); err != nil {
			t.Fatal(err)
		}
		for index := range 2 {
			if _, _, err := store.Submit(ctx,
				newIntegrationSubmission(t, "ack-producer", fmt.Sprintf("ack-key-%d", index), "ack-device", fmt.Sprintf("payload-%d", index), now),
				"test",
				fmt.Sprintf("ack-submit-%d", index),
			); err != nil {
				t.Fatal(err)
			}
		}
		leaseRequest, err := command.NewLeaseRequest("ack-gateway", 2, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		leases, err := store.LeaseNext(ctx, leaseRequest, "ack-lease")
		if err != nil || len(leases) != 1 {
			t.Fatalf("LeaseNext() = (%+v, %v)", leases, err)
		}
		leased := leases[0]
		if _, err := store.MarkInFlight(ctx, leased.Command.ID, "ack-gateway", leased.Command.LeaseToken, leased.SessionEpoch, "ack-started"); err != nil {
			t.Fatal(err)
		}
		resultHash := sha256.Sum256([]byte("applied"))
		clientAppliedAt := now.Add(500 * time.Millisecond)
		acknowledgement, err := command.NewAcknowledgement(
			leased.Command.ID,
			leased.Command.DeviceID,
			leased.Command.SequenceNumber,
			"ack-gateway",
			leased.Command.LeaseToken,
			leased.SessionEpoch,
			resultHash[:],
			&clientAppliedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		stale := acknowledgement
		stale.LeaseToken++
		if _, err := store.Acknowledge(ctx, stale, "stale-ack"); !errors.Is(err, command.ErrStaleLease) {
			t.Fatalf("stale Acknowledge() error = %v", err)
		}
		acknowledged, err := store.Acknowledge(ctx, acknowledgement, "active-ack")
		if err != nil || acknowledged.State != command.StateAcknowledged {
			t.Fatalf("Acknowledge() = (%+v, %v)", acknowledged, err)
		}
		duplicate, err := store.Acknowledge(ctx, acknowledgement, "duplicate-ack")
		if err != nil || duplicate.ID != acknowledged.ID {
			t.Fatalf("duplicate Acknowledge() = (%+v, %v)", duplicate, err)
		}
		assertTerminalCursor(t, pool, "ack-device", 1)
		next, err := store.LeaseNext(ctx, leaseRequest, "ack-next")
		if err != nil || len(next) != 1 || next[0].Command.SequenceNumber != 2 {
			t.Fatalf("next LeaseNext() = (%+v, %v)", next, err)
		}
	})

	t.Run("expired command does not block its successors", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{})
		acquisition, err := session.NewAcquisition("expiry-device", "expiry-gateway", "expiry-client")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcquireSession(ctx, acquisition); err != nil {
			t.Fatal(err)
		}
		shortLived, _, err := store.Submit(ctx,
			newExpiringSubmission(t, "expiry-producer", "expiry-key-1", "expiry-device", "short", now, now.Add(time.Minute)),
			"test",
			"expiry-submit-1",
		)
		if err != nil {
			t.Fatal(err)
		}
		successor, _, err := store.Submit(ctx,
			newExpiringSubmission(t, "expiry-producer", "expiry-key-2", "expiry-device", "long", now, now.Add(time.Hour)),
			"test",
			"expiry-submit-2",
		)
		if err != nil {
			t.Fatal(err)
		}

		laterStore := NewCommandStore(pool, integrationClock{now: now.Add(2 * time.Minute)}, StorePolicy{})
		leaseRequest, err := command.NewLeaseRequest("expiry-gateway", 10, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		blocked, err := laterStore.LeaseNext(ctx, leaseRequest, "expiry-lease-blocked")
		if err != nil || len(blocked) != 0 {
			t.Fatalf("LeaseNext() before sweep = (%+v, %v), want no leases", blocked, err)
		}

		expired, err := laterStore.SweepExpiredCommands(ctx, 10, "expiry-sweep")
		if err != nil || expired != 1 {
			t.Fatalf("SweepExpiredCommands() = (%d, %v), want (1, nil)", expired, err)
		}
		swept, err := laterStore.Get(ctx, shortLived.ID)
		if err != nil || swept.State != command.StateExpired {
			t.Fatalf("expired command = (%+v, %v)", swept, err)
		}
		assertTerminalCursor(t, pool, "expiry-device", 1)

		unblocked, err := laterStore.LeaseNext(ctx, leaseRequest, "expiry-lease-unblocked")
		if err != nil || len(unblocked) != 1 || unblocked[0].Command.ID != successor.ID {
			t.Fatalf("LeaseNext() after sweep = (%+v, %v), want the successor", unblocked, err)
		}

		repeated, err := laterStore.SweepExpiredCommands(ctx, 10, "expiry-sweep-again")
		if err != nil || repeated != 0 {
			t.Fatalf("repeated SweepExpiredCommands() = (%d, %v), want (0, nil)", repeated, err)
		}
	})

	t.Run("expired lease schedules retry with backoff", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		retryPolicy := command.RetryPolicy{
			MaxAttempts: 5,
			BaseDelay:   100 * time.Millisecond,
			MaxDelay:    800 * time.Millisecond,
		}
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{Retry: retryPolicy})
		store.SetRetryRNG(rand.New(rand.NewSource(7)))
		acquisition, err := session.NewAcquisition("backoff-device", "backoff-gateway", "backoff-client")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcquireSession(ctx, acquisition); err != nil {
			t.Fatal(err)
		}
		submitted, _, err := store.Submit(ctx,
			newIntegrationSubmission(t, "backoff-producer", "backoff-key", "backoff-device", "payload", now),
			"test",
			"backoff-submit",
		)
		if err != nil {
			t.Fatal(err)
		}
		leaseRequest, err := command.NewLeaseRequest("backoff-gateway", 1, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.LeaseNext(ctx, leaseRequest, "backoff-lease-1"); err != nil {
			t.Fatal(err)
		}
		sweepAt := now.Add(2 * time.Second)
		sweepStore := NewCommandStore(pool, integrationClock{now: sweepAt}, StorePolicy{Retry: retryPolicy})
		sweepStore.SetRetryRNG(rand.New(rand.NewSource(7)))
		if _, err := sweepStore.SweepExpiredLeases(ctx, 10, "backoff-sweep"); err != nil {
			t.Fatal(err)
		}
		retrying, err := sweepStore.Get(ctx, submitted.ID)
		if err != nil {
			t.Fatal(err)
		}
		if retrying.State != command.StateRetryWait {
			t.Fatalf("state = %s, want RETRY_WAIT", retrying.State)
		}
		wantNext := retryPolicy.NextAttemptAt(1, sweepAt, rand.New(rand.NewSource(7)))
		gotNext := retrying.NextAttemptAt.Truncate(time.Microsecond)
		wantNext = wantNext.Truncate(time.Microsecond)
		if !gotNext.Equal(wantNext) {
			t.Fatalf("next_attempt_at = %s, want %s", gotNext, wantNext)
		}
	})

	t.Run("retry budget exhaustion dead letters the command", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		retryPolicy := command.RetryPolicy{
			MaxAttempts: 2,
			BaseDelay:   10 * time.Millisecond,
			MaxDelay:    10 * time.Millisecond,
		}
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{Retry: retryPolicy})
		acquisition, err := session.NewAcquisition("deadletter-device", "deadletter-gateway", "deadletter-client")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcquireSession(ctx, acquisition); err != nil {
			t.Fatal(err)
		}
		submitted, _, err := store.Submit(ctx,
			newIntegrationSubmission(t, "deadletter-producer", "deadletter-key", "deadletter-device", "payload", now),
			"test",
			"deadletter-submit",
		)
		if err != nil {
			t.Fatal(err)
		}
		leaseRequest, err := command.NewLeaseRequest("deadletter-gateway", 1, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		firstLeaseStore := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{Retry: retryPolicy})
		if _, err := firstLeaseStore.LeaseNext(ctx, leaseRequest, "deadletter-lease-1"); err != nil {
			t.Fatal(err)
		}
		firstSweepAt := now.Add(2 * time.Second)
		firstSweepStore := NewCommandStore(pool, integrationClock{now: firstSweepAt}, StorePolicy{Retry: retryPolicy})
		if _, err := firstSweepStore.SweepExpiredLeases(ctx, 10, "deadletter-sweep-1"); err != nil {
			t.Fatal(err)
		}
		secondLeaseAt := firstSweepAt.Add(retryPolicy.MaxDelay)
		secondLeaseStore := NewCommandStore(pool, integrationClock{now: secondLeaseAt}, StorePolicy{Retry: retryPolicy})
		if _, err := secondLeaseStore.LeaseNext(ctx, leaseRequest, "deadletter-lease-2"); err != nil {
			t.Fatal(err)
		}
		secondSweepAt := secondLeaseAt.Add(2 * time.Second)
		secondSweepStore := NewCommandStore(pool, integrationClock{now: secondSweepAt}, StorePolicy{Retry: retryPolicy})
		if _, err := secondSweepStore.SweepExpiredLeases(ctx, 10, "deadletter-sweep-2"); err != nil {
			t.Fatal(err)
		}
		finalStore := NewCommandStore(pool, integrationClock{now: secondSweepAt}, StorePolicy{Retry: retryPolicy})
		deadLettered, err := finalStore.Get(ctx, submitted.ID)
		if err != nil {
			t.Fatal(err)
		}
		if deadLettered.State != command.StateDeadLetter {
			t.Fatalf("state = %s, want DEAD_LETTER", deadLettered.State)
		}
		if deadLettered.FailureReason != retryBudgetExhaustedReason {
			t.Fatalf("failure_reason = %q, want %q", deadLettered.FailureReason, retryBudgetExhaustedReason)
		}
	})

	t.Run("admission limits reject durable overload", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{
			Admission: command.AdmissionLimits{GlobalMax: 2, PerDeviceMax: 1},
		})
		if _, _, err := store.Submit(ctx,
			newIntegrationSubmission(t, "admission-producer", "admission-key-1", "device-a", "payload", now),
			"test",
			"admission-submit-1",
		); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Submit(ctx,
			newIntegrationSubmission(t, "admission-producer", "admission-key-2", "device-a", "payload", now),
			"test",
			"admission-submit-2",
		); !errors.Is(err, command.ErrAdmissionLimited) {
			t.Fatalf("per-device Submit() error = %v, want ErrAdmissionLimited", err)
		}
		if _, _, err := store.Submit(ctx,
			newIntegrationSubmission(t, "admission-producer", "admission-key-3", "device-b", "payload", now),
			"test",
			"admission-submit-3",
		); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Submit(ctx,
			newIntegrationSubmission(t, "admission-producer", "admission-key-4", "device-c", "payload", now),
			"test",
			"admission-submit-4",
		); !errors.Is(err, command.ErrAdmissionLimited) {
			t.Fatalf("global Submit() error = %v, want ErrAdmissionLimited", err)
		}
	})

	t.Run("grpc submit get cancel workflow", func(t *testing.T) {
		resetDatabase(t, pool)
		now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
		store := NewCommandStore(pool, integrationClock{now: now}, StorePolicy{})
		listener := bufconn.Listen(1024 * 1024)
		server := grpc.NewServer()
		orbitv1.RegisterCommandServiceServer(server, commandservice.New(store, integrationClock{now: now}))
		serveErrors := make(chan error, 1)
		go func() { serveErrors <- server.Serve(listener) }()
		t.Cleanup(func() {
			server.Stop()
			_ = listener.Close()
			<-serveErrors
		})

		connection, err := grpc.NewClient(
			"passthrough:///orbit-integration",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return listener.Dial()
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		client := orbitv1.NewCommandServiceClient(connection)

		submitted, err := client.SubmitCommand(ctx, &orbitv1.SubmitCommandRequest{
			ProducerId:     "grpc-producer",
			IdempotencyKey: "grpc-request-1",
			DeviceId:       "grpc-device",
			Priority:       7,
			Payload:        []byte("diagnostic"),
			ExpiresAt:      timestamppb.New(now.Add(time.Hour)),
		})
		if err != nil {
			t.Fatalf("SubmitCommand() error = %v", err)
		}
		queried, err := client.GetCommand(ctx, &orbitv1.GetCommandRequest{CommandId: submitted.CommandId})
		if err != nil || queried.CommandId != submitted.CommandId {
			t.Fatalf("GetCommand() = (%+v, %v)", queried, err)
		}
		cancelled, err := client.CancelCommand(ctx, &orbitv1.CancelCommandRequest{CommandId: submitted.CommandId})
		if err != nil || cancelled.State != orbitv1.CommandState_COMMAND_STATE_CANCELLED {
			t.Fatalf("CancelCommand() = (%+v, %v)", cancelled, err)
		}
	})
}

func newIntegrationSubmission(t *testing.T, producer, key, device, payload string, now time.Time) command.Submission {
	t.Helper()
	result, err := command.NewSubmission(producer, key, device, 4, []byte(payload), now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func newExpiringSubmission(
	t *testing.T,
	producer, key, device, payload string,
	now time.Time,
	expiresAt time.Time,
) command.Submission {
	t.Helper()
	result, err := command.NewSubmission(producer, key, device, 4, []byte(payload), expiresAt, now)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func resetDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		TRUNCATE orbit.audit_events, orbit.delivery_attempts, orbit.commands, orbit.device_cursors
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
}

func assertTerminalCursor(t *testing.T, pool *pgxpool.Pool, deviceID string, want int64) {
	t.Helper()
	var got int64
	if err := pool.QueryRow(context.Background(),
		`SELECT last_terminal_sequence FROM orbit.device_cursors WHERE device_id = $1`,
		deviceID,
	).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("last_terminal_sequence = %d, want %d", got, want)
	}
}
