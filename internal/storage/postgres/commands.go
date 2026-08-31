package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/JayYarlagadda/orbit/internal/command"
	"github.com/JayYarlagadda/orbit/internal/metrics"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type CommandStore struct {
	pool      *pgxpool.Pool
	clock     Clock
	retry     command.RetryPolicy
	admission command.AdmissionLimits
	retryRNG  *rand.Rand
}

func NewCommandStore(pool *pgxpool.Pool, clock Clock, policy StorePolicy) *CommandStore {
	if clock == nil {
		clock = SystemClock{}
	}
	policy = policy.withDefaults()
	return &CommandStore{
		pool:      pool,
		clock:     clock,
		retry:     policy.Retry,
		admission: policy.Admission,
	}
}

// SetRetryRNG configures deterministic retry jitter for tests.
func (s *CommandStore) SetRetryRNG(rng *rand.Rand) {
	s.retryRNG = rng
}

func (s *CommandStore) retryRandom() *rand.Rand {
	if s.retryRNG != nil {
		return s.retryRNG
	}
	return rand.New(rand.NewSource(s.clock.Now().UnixNano()))
}

func Open(ctx context.Context, databaseURL string, maxConnections int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = "orbitd"
	config.MaxConns = maxConnections

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func (s *CommandStore) Submit(
	ctx context.Context,
	submission command.Submission,
	actor string,
	correlationID string,
) (command.Command, bool, error) {
	if s.pool == nil {
		return command.Command{}, false, fmt.Errorf("submit command: database pool is nil")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return command.Command{}, false, fmt.Errorf("begin submit transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0) # hashtextextended($2, 1))`,
		submission.ProducerID,
		submission.IdempotencyKey,
	); err != nil {
		return command.Command{}, false, fmt.Errorf("lock idempotency key: %w", err)
	}

	existing, err := getByIdempotencyKey(ctx, tx, submission.ProducerID, submission.IdempotencyKey)
	switch {
	case err == nil:
		if existing.RequestHash != submission.RequestHash {
			return command.Command{}, false, command.ErrIdempotencyConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return command.Command{}, false, fmt.Errorf("commit idempotent lookup: %w", err)
		}
		_ = metrics.RecordCommandSubmitted(metrics.SubmittedResultIdempotent)
		return existing, false, nil
	case !errors.Is(err, command.ErrNotFound):
		return command.Command{}, false, err
	}
	now := s.clock.Now().UTC()
	if !submission.ExpiresAt.After(now) {
		return command.Command{}, false, &command.ValidationError{
			Field:   "expires_at",
			Message: "must be after the server time when persisted",
		}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO orbit.device_cursors (device_id) VALUES ($1) ON CONFLICT DO NOTHING`,
		submission.DeviceID,
	); err != nil {
		return command.Command{}, false, fmt.Errorf("ensure device cursor: %w", err)
	}

	var globalOutstanding int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM orbit.commands WHERE state IN `+outstandingStatesSQL,
	).Scan(&globalOutstanding); err != nil {
		return command.Command{}, false, fmt.Errorf("count global outstanding commands: %w", err)
	}
	if globalOutstanding >= s.admission.GlobalMax {
		_ = metrics.RecordAdmissionRejected(metrics.AdmissionReasonGlobal)
		return command.Command{}, false, command.ErrAdmissionLimited
	}
	var deviceOutstanding int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM orbit.commands WHERE device_id = $1 AND state IN `+outstandingStatesSQL,
		submission.DeviceID,
	).Scan(&deviceOutstanding); err != nil {
		return command.Command{}, false, fmt.Errorf("count device outstanding commands: %w", err)
	}
	if deviceOutstanding >= s.admission.PerDeviceMax {
		_ = metrics.RecordAdmissionRejected(metrics.AdmissionReasonPerDevice)
		return command.Command{}, false, command.ErrAdmissionLimited
	}

	var sequenceNumber int64
	if err := tx.QueryRow(ctx,
		`SELECT next_sequence_number FROM orbit.device_cursors WHERE device_id = $1 FOR UPDATE`,
		submission.DeviceID,
	).Scan(&sequenceNumber); err != nil {
		return command.Command{}, false, fmt.Errorf("lock device cursor: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orbit.device_cursors
		 SET next_sequence_number = next_sequence_number + 1, updated_at = $2
		 WHERE device_id = $1`,
		submission.DeviceID,
		now,
	); err != nil {
		return command.Command{}, false, fmt.Errorf("advance device cursor: %w", err)
	}

	created, err := scanCommand(tx.QueryRow(ctx, `
		INSERT INTO orbit.commands (
			producer_id, idempotency_key, device_id, sequence_number, priority,
			payload, payload_hash, request_hash, state, created_at, expires_at,
			next_attempt_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'QUEUED', $9, $10, $9)
		RETURNING `+commandColumns,
		submission.ProducerID,
		submission.IdempotencyKey,
		submission.DeviceID,
		sequenceNumber,
		submission.Priority,
		submission.Payload,
		submission.PayloadHash[:],
		submission.RequestHash[:],
		now,
		submission.ExpiresAt,
	))
	if err != nil {
		return command.Command{}, false, fmt.Errorf("insert command: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO orbit.audit_events (
			command_id, old_state, new_state, actor, lease_token, occurred_at, correlation_id
		) VALUES ($1, NULL, 'QUEUED', $2, 0, $3, $4)`,
		created.ID,
		actor,
		now,
		correlationID,
	); err != nil {
		return command.Command{}, false, fmt.Errorf("insert submit audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return command.Command{}, false, fmt.Errorf("commit submit transaction: %w", err)
	}
	_ = metrics.RecordCommandSubmitted(metrics.SubmittedResultCreated)
	return created, true, nil
}

func (s *CommandStore) Get(ctx context.Context, commandID string) (command.Command, error) {
	if s.pool == nil {
		return command.Command{}, fmt.Errorf("get command: database pool is nil")
	}
	result, err := scanCommand(s.pool.QueryRow(ctx,
		`SELECT `+commandColumns+` FROM orbit.commands WHERE id = $1`,
		commandID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return command.Command{}, command.ErrNotFound
	}
	if err != nil {
		return command.Command{}, fmt.Errorf("get command: %w", err)
	}
	return result, nil
}

func (s *CommandStore) Cancel(
	ctx context.Context,
	commandID string,
	actor string,
	correlationID string,
) (command.Command, error) {
	if s.pool == nil {
		return command.Command{}, fmt.Errorf("cancel command: database pool is nil")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return command.Command{}, fmt.Errorf("begin cancel transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existing, err := scanCommand(tx.QueryRow(ctx,
		`SELECT `+commandColumns+` FROM orbit.commands WHERE id = $1 FOR UPDATE`,
		commandID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return command.Command{}, command.ErrNotFound
	}
	if err != nil {
		return command.Command{}, fmt.Errorf("lock command for cancellation: %w", err)
	}
	if existing.State == command.StateCancelled {
		if err := tx.Commit(ctx); err != nil {
			return command.Command{}, fmt.Errorf("commit repeated cancellation: %w", err)
		}
		return existing, nil
	}
	if err := command.ValidateTransition(existing.State, command.StateCancelled); err != nil {
		return command.Command{}, err
	}

	now := s.clock.Now().UTC()
	cancelled, err := scanCommand(tx.QueryRow(ctx, `
		UPDATE orbit.commands
		SET state = 'CANCELLED', lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1 AND state = $2
		RETURNING `+commandColumns,
		commandID,
		existing.State,
	))
	if err != nil {
		return command.Command{}, fmt.Errorf("cancel command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO orbit.audit_events (
			command_id, old_state, new_state, actor, lease_token, occurred_at, correlation_id
		) VALUES ($1, $2, 'CANCELLED', $3, $4, $5, $6)`,
		cancelled.ID,
		existing.State,
		actor,
		cancelled.LeaseToken,
		now,
		correlationID,
	); err != nil {
		return command.Command{}, fmt.Errorf("insert cancellation audit event: %w", err)
	}
	if err := advanceTerminalCursor(ctx, tx, cancelled.DeviceID); err != nil {
		return command.Command{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return command.Command{}, fmt.Errorf("commit cancel transaction: %w", err)
	}
	return cancelled, nil
}

func (s *CommandStore) LeaseNext(
	ctx context.Context,
	request command.LeaseRequest,
	correlationID string,
) ([]command.Lease, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("lease commands: database pool is nil")
	}
	validatedRequest, err := command.NewLeaseRequest(request.Owner, request.Limit, request.Duration)
	if err != nil {
		return nil, err
	}
	request = validatedRequest
	now := s.clock.Now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin lease transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT candidate.id::text, candidate.state::text, cursor.active_session_epoch
		FROM orbit.commands AS candidate
		JOIN orbit.device_cursors AS cursor ON cursor.device_id = candidate.device_id
		WHERE candidate.state IN ('QUEUED', 'RETRY_WAIT')
		  AND candidate.next_attempt_at <= $1
		  AND candidate.expires_at > $1
		  AND cursor.active_gateway_id = $3
		  AND NOT EXISTS (
			SELECT 1
			FROM orbit.commands AS earlier
			WHERE earlier.device_id = candidate.device_id
			  AND earlier.sequence_number < candidate.sequence_number
			  AND earlier.state NOT IN ('ACKNOWLEDGED', 'EXPIRED', 'DEAD_LETTER', 'CANCELLED')
		  )
		ORDER BY candidate.priority DESC, candidate.created_at, candidate.id
		FOR UPDATE OF candidate SKIP LOCKED
		LIMIT $2`,
		now,
		request.Limit,
		request.Owner,
	)
	if err != nil {
		return nil, fmt.Errorf("select lease candidates: %w", err)
	}
	type candidate struct {
		id           string
		oldState     command.State
		sessionEpoch int64
	}
	candidates := make([]candidate, 0, request.Limit)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.oldState, &item.sessionEpoch); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan lease candidate: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate lease candidates: %w", err)
	}
	rows.Close()

	leased := make([]command.Lease, 0, len(candidates))
	for _, candidate := range candidates {
		if err := command.ValidateTransition(candidate.oldState, command.StateLeased); err != nil {
			return nil, err
		}
		result, err := scanCommand(tx.QueryRow(ctx, `
			UPDATE orbit.commands
			SET state = 'LEASED',
				lease_owner = $2,
				lease_token = lease_token + 1,
				lease_expires_at = $3,
				attempt_count = attempt_count + 1,
				failure_reason = NULL
			WHERE id = $1 AND state = $4
			RETURNING `+commandColumns,
			candidate.id,
			request.Owner,
			now.Add(request.Duration),
			candidate.oldState,
		))
		if err != nil {
			return nil, fmt.Errorf("lease command %s: %w", candidate.id, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO orbit.audit_events (
				command_id, old_state, new_state, actor, lease_token, occurred_at, correlation_id
			) VALUES ($1, $2, 'LEASED', $3, $4, $5, $6)`,
			result.ID,
			candidate.oldState,
			request.Owner,
			result.LeaseToken,
			now,
			correlationID,
		); err != nil {
			return nil, fmt.Errorf("insert lease audit event: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO orbit.delivery_attempts (
				command_id, attempt_number, gateway_id, session_epoch, lease_token, started_at
			) VALUES ($1, $2, $3, $4, $5, $6)`,
			result.ID,
			result.AttemptCount,
			request.Owner,
			candidate.sessionEpoch,
			result.LeaseToken,
			now,
		); err != nil {
			return nil, fmt.Errorf("insert delivery attempt: %w", err)
		}
		leased = append(leased, command.Lease{Command: result, SessionEpoch: candidate.sessionEpoch})
		metrics.RecordCommandLeased()
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit lease transaction: %w", err)
	}
	return leased, nil
}

func (s *CommandStore) MarkInFlight(
	ctx context.Context,
	commandID string,
	leaseOwner string,
	leaseToken int64,
	sessionEpoch int64,
	correlationID string,
) (command.Command, error) {
	if s.pool == nil {
		return command.Command{}, fmt.Errorf("mark command in flight: database pool is nil")
	}
	now := s.clock.Now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return command.Command{}, fmt.Errorf("begin in-flight transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := scanCommand(tx.QueryRow(ctx, `
		UPDATE orbit.commands AS leased
		SET state = 'IN_FLIGHT'
		FROM orbit.device_cursors AS cursor
		WHERE leased.id = $1
		  AND leased.state = 'LEASED'
		  AND leased.lease_owner = $2
		  AND leased.lease_token = $3
		  AND leased.lease_expires_at > $5
		  AND cursor.device_id = leased.device_id
		  AND cursor.active_gateway_id = $2
		  AND cursor.active_session_epoch = $4
		  AND EXISTS (
			SELECT 1
			FROM orbit.delivery_attempts AS attempt
			WHERE attempt.command_id = leased.id
			  AND attempt.gateway_id = $2
			  AND attempt.lease_token = $3
			  AND attempt.session_epoch = $4
		  )
		RETURNING `+leasedCommandColumns,
		commandID,
		leaseOwner,
		leaseToken,
		sessionEpoch,
		now,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = metrics.RecordStaleLeaseRejection(metrics.StaleLeaseOperationInFlight)
		return command.Command{}, command.ErrStaleLease
	}
	if err != nil {
		return command.Command{}, fmt.Errorf("mark command in flight: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO orbit.audit_events (
			command_id, old_state, new_state, actor, lease_token, occurred_at, correlation_id
		) VALUES ($1, 'LEASED', 'IN_FLIGHT', $2, $3, $4, $5)`,
		result.ID,
		leaseOwner,
		leaseToken,
		now,
		correlationID,
	); err != nil {
		return command.Command{}, fmt.Errorf("insert in-flight audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return command.Command{}, fmt.Errorf("commit in-flight transaction: %w", err)
	}
	return result, nil
}

func (s *CommandStore) SweepExpiredLeases(ctx context.Context, limit int, correlationID string) (int, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("sweep expired leases: database pool is nil")
	}
	if limit < 1 || limit > command.MaxLeaseBatchSize {
		return 0, &command.ValidationError{
			Field:   "sweep_limit",
			Message: fmt.Sprintf("must be between 1 and %d", command.MaxLeaseBatchSize),
		}
	}
	now := s.clock.Now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("begin lease sweep transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id::text, state::text, lease_token, attempt_count
		FROM orbit.commands
		WHERE state IN ('LEASED', 'IN_FLIGHT') AND lease_expires_at <= $1
		ORDER BY lease_expires_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT $2`,
		now,
		limit,
	)
	if err != nil {
		return 0, fmt.Errorf("select expired leases: %w", err)
	}
	type expiredLease struct {
		id           string
		state        command.State
		token        int64
		attemptCount int32
	}
	var expired []expiredLease
	for rows.Next() {
		var item expiredLease
		if err := rows.Scan(&item.id, &item.state, &item.token, &item.attemptCount); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired lease: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired leases: %w", err)
	}
	rows.Close()

	retryRNG := s.retryRandom()
	for _, item := range expired {
		if s.retry.Exhausted(item.attemptCount) {
			if err := command.ValidateTransition(item.state, command.StateDeadLetter); err != nil {
				return 0, err
			}
			result, err := tx.Exec(ctx, `
				UPDATE orbit.commands
				SET state = 'DEAD_LETTER',
					lease_owner = NULL,
					lease_expires_at = NULL,
					failure_reason = $3
				WHERE id = $1 AND state = $2 AND lease_token = $4`,
				item.id,
				item.state,
				retryBudgetExhaustedReason,
				item.token,
			)
			if err != nil {
				return 0, fmt.Errorf("dead-letter expired lease %s: %w", item.id, err)
			}
			if result.RowsAffected() != 1 {
				return 0, command.ErrStaleLease
			}
			if _, err := tx.Exec(ctx, `
				UPDATE orbit.delivery_attempts
				SET finished_at = $3, outcome = 'LEASE_EXPIRED', reason = $4
				WHERE command_id = $1 AND lease_token = $2 AND finished_at IS NULL`,
				item.id,
				item.token,
				now,
				retryBudgetExhaustedReason,
			); err != nil {
				return 0, fmt.Errorf("close dead-letter delivery attempt: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO orbit.audit_events (
					command_id, old_state, new_state, actor, lease_token, occurred_at,
					correlation_id, details
				) VALUES ($1, $2, 'DEAD_LETTER', 'lease-sweeper', $3, $4, $5,
					'{"reason":"retry_budget_exhausted"}'::jsonb)`,
				item.id,
				item.state,
				item.token,
				now,
				correlationID,
			); err != nil {
				return 0, fmt.Errorf("insert dead-letter audit event: %w", err)
			}
			_ = metrics.RecordLeaseExpiration(metrics.LeaseExpiryOutcomeDeadLetter)
			continue
		}

		if err := command.ValidateTransition(item.state, command.StateRetryWait); err != nil {
			return 0, err
		}
		nextAttemptAt := s.retry.NextAttemptAt(item.attemptCount, now, retryRNG)
		result, err := tx.Exec(ctx, `
			UPDATE orbit.commands
			SET state = 'RETRY_WAIT',
				lease_owner = NULL,
				lease_expires_at = NULL,
				next_attempt_at = $3,
				failure_reason = $5
			WHERE id = $1 AND state = $2 AND lease_token = $4`,
			item.id,
			item.state,
			nextAttemptAt,
			item.token,
			leaseExpiredReason,
		)
		if err != nil {
			return 0, fmt.Errorf("release expired lease %s: %w", item.id, err)
		}
		if result.RowsAffected() != 1 {
			return 0, command.ErrStaleLease
		}
		if _, err := tx.Exec(ctx, `
			UPDATE orbit.delivery_attempts
			SET finished_at = $3, outcome = 'LEASE_EXPIRED', reason = 'lease expired'
			WHERE command_id = $1 AND lease_token = $2 AND finished_at IS NULL`,
			item.id,
			item.token,
			now,
		); err != nil {
			return 0, fmt.Errorf("close expired delivery attempt: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO orbit.audit_events (
				command_id, old_state, new_state, actor, lease_token, occurred_at,
				correlation_id, details
			) VALUES ($1, $2, 'RETRY_WAIT', 'lease-sweeper', $3, $4, $5,
				'{"reason":"lease_expired"}'::jsonb)`,
			item.id,
			item.state,
			item.token,
			now,
			correlationID,
		); err != nil {
			return 0, fmt.Errorf("insert expired lease audit event: %w", err)
		}
		_ = metrics.RecordLeaseExpiration(metrics.LeaseExpiryOutcomeRetryWait)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit lease sweep transaction: %w", err)
	}
	return len(expired), nil
}

// SweepExpiredCommands moves commands whose TTL elapsed before delivery into
// the terminal EXPIRED state. Lease selection already refuses to lease an
// expired command, but the per-device ordering guard treats any non-terminal
// predecessor as blocking, so an unswept expired command stalls every later
// command for that device indefinitely.
func (s *CommandStore) SweepExpiredCommands(ctx context.Context, limit int, correlationID string) (int, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("sweep expired commands: database pool is nil")
	}
	if limit < 1 || limit > command.MaxLeaseBatchSize {
		return 0, &command.ValidationError{
			Field:   "sweep_limit",
			Message: fmt.Sprintf("must be between 1 and %d", command.MaxLeaseBatchSize),
		}
	}
	now := s.clock.Now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("begin command expiry transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id::text, state::text, device_id, lease_token
		FROM orbit.commands
		WHERE state IN ('QUEUED', 'RETRY_WAIT') AND expires_at <= $1
		ORDER BY expires_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT $2`,
		now,
		limit,
	)
	if err != nil {
		return 0, fmt.Errorf("select expired commands: %w", err)
	}
	type expiredCommand struct {
		id       string
		state    command.State
		deviceID string
		token    int64
	}
	var expired []expiredCommand
	for rows.Next() {
		var item expiredCommand
		if err := rows.Scan(&item.id, &item.state, &item.deviceID, &item.token); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired command: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired commands: %w", err)
	}
	rows.Close()

	devices := make([]string, 0, len(expired))
	seenDevices := make(map[string]struct{}, len(expired))
	for _, item := range expired {
		if err := command.ValidateTransition(item.state, command.StateExpired); err != nil {
			return 0, err
		}
		result, err := tx.Exec(ctx, `
			UPDATE orbit.commands
			SET state = 'EXPIRED',
				lease_owner = NULL,
				lease_expires_at = NULL,
				failure_reason = 'command expired before delivery'
			WHERE id = $1 AND state = $2`,
			item.id,
			item.state,
		)
		if err != nil {
			return 0, fmt.Errorf("expire command %s: %w", item.id, err)
		}
		if result.RowsAffected() != 1 {
			return 0, fmt.Errorf("expire command %s: state changed concurrently", item.id)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO orbit.audit_events (
				command_id, old_state, new_state, actor, lease_token, occurred_at,
				correlation_id, details
			) VALUES ($1, $2, 'EXPIRED', 'expiry-sweeper', $3, $4, $5,
				'{"reason":"ttl_elapsed"}'::jsonb)`,
			item.id,
			item.state,
			item.token,
			now,
			correlationID,
		); err != nil {
			return 0, fmt.Errorf("insert command expiry audit event: %w", err)
		}
		if _, exists := seenDevices[item.deviceID]; !exists {
			seenDevices[item.deviceID] = struct{}{}
			devices = append(devices, item.deviceID)
		}
	}

	// Expiry is terminal, so the contiguous terminal cursor can advance and
	// unblock the successors that the expired commands were holding back.
	for _, deviceID := range devices {
		if err := advanceTerminalCursor(ctx, tx, deviceID); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit command expiry transaction: %w", err)
	}
	metrics.RecordCommandExpired(len(expired))
	return len(expired), nil
}

func (s *CommandStore) Acknowledge(
	ctx context.Context,
	acknowledgement command.Acknowledgement,
	correlationID string,
) (command.Command, error) {
	if s.pool == nil {
		return command.Command{}, fmt.Errorf("acknowledge command: database pool is nil")
	}
	now := s.clock.Now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return command.Command{}, fmt.Errorf("begin acknowledgement transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	acknowledged, err := scanCommand(tx.QueryRow(ctx, `
		UPDATE orbit.commands AS acknowledged
		SET state = 'ACKNOWLEDGED',
			acknowledged_at = $7,
			lease_owner = NULL,
			lease_expires_at = NULL,
			failure_reason = NULL
		FROM orbit.device_cursors AS cursor
		WHERE acknowledged.id = $1
		  AND acknowledged.device_id = $2
		  AND acknowledged.sequence_number = $3
		  AND acknowledged.state = 'IN_FLIGHT'
		  AND acknowledged.lease_owner = $4
		  AND acknowledged.lease_token = $5
		  AND cursor.device_id = acknowledged.device_id
		  AND cursor.active_gateway_id = $4
		  AND cursor.active_session_epoch = $6
		  AND EXISTS (
			SELECT 1
			FROM orbit.delivery_attempts AS attempt
			WHERE attempt.command_id = acknowledged.id
			  AND attempt.gateway_id = $4
			  AND attempt.lease_token = $5
			  AND attempt.session_epoch = $6
		  )
		RETURNING `+acknowledgedCommandColumns,
		acknowledgement.CommandID,
		acknowledgement.DeviceID,
		acknowledgement.SequenceNumber,
		acknowledgement.GatewayID,
		acknowledgement.LeaseToken,
		acknowledgement.SessionEpoch,
		now,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, getErr := scanCommand(tx.QueryRow(ctx,
			`SELECT `+commandColumns+`
			 FROM orbit.commands
			 WHERE id = $1 AND device_id = $2 AND sequence_number = $3`,
			acknowledgement.CommandID,
			acknowledgement.DeviceID,
			acknowledgement.SequenceNumber,
		))
		if getErr == nil && existing.State == command.StateAcknowledged && existing.LeaseToken == acknowledgement.LeaseToken {
			if err := tx.Commit(ctx); err != nil {
				return command.Command{}, fmt.Errorf("commit duplicate acknowledgement: %w", err)
			}
			return existing, nil
		}
		if getErr != nil && !errors.Is(getErr, pgx.ErrNoRows) {
			return command.Command{}, fmt.Errorf("check duplicate acknowledgement: %w", getErr)
		}
		_ = metrics.RecordStaleLeaseRejection(metrics.StaleLeaseOperationAcknowledge)
		return command.Command{}, command.ErrStaleLease
	}
	if err != nil {
		return command.Command{}, fmt.Errorf("acknowledge command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE orbit.delivery_attempts
		SET finished_at = $6,
			outcome = 'ACKNOWLEDGED',
			result_hash = $4,
			client_applied_at = $5
		WHERE command_id = $1
		  AND gateway_id = $2
		  AND session_epoch = $3
		  AND lease_token = $7
		  AND finished_at IS NULL`,
		acknowledgement.CommandID,
		acknowledgement.GatewayID,
		acknowledgement.SessionEpoch,
		acknowledgement.ResultHash[:],
		acknowledgement.ClientAppliedAt,
		now,
		acknowledgement.LeaseToken,
	); err != nil {
		return command.Command{}, fmt.Errorf("close acknowledged delivery attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO orbit.audit_events (
			command_id, old_state, new_state, actor, lease_token, occurred_at,
			correlation_id
		) VALUES ($1, 'IN_FLIGHT', 'ACKNOWLEDGED', $2, $3, $4, $5)`,
		acknowledgement.CommandID,
		acknowledgement.GatewayID,
		acknowledgement.LeaseToken,
		now,
		correlationID,
	); err != nil {
		return command.Command{}, fmt.Errorf("insert acknowledgement audit event: %w", err)
	}
	if err := advanceTerminalCursor(ctx, tx, acknowledged.DeviceID); err != nil {
		return command.Command{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return command.Command{}, fmt.Errorf("commit acknowledgement transaction: %w", err)
	}
	metrics.RecordCommandAcknowledged()
	metrics.ObserveCommandDeliveryDuration(now.Sub(acknowledged.CreatedAt).Seconds())
	return acknowledged, nil
}

func (s *CommandStore) RefreshQueueDepth(ctx context.Context) error {
	if s.pool == nil {
		return fmt.Errorf("refresh queue depth: database pool is nil")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT state::text, count(*)
		FROM orbit.commands
		WHERE state IN ('QUEUED', 'RETRY_WAIT', 'LEASED', 'IN_FLIGHT')
		GROUP BY state`)
	if err != nil {
		return fmt.Errorf("count queue depth: %w", err)
	}
	defer rows.Close()

	seen := map[string]struct{}{
		"QUEUED":     {},
		"RETRY_WAIT": {},
		"LEASED":     {},
		"IN_FLIGHT":  {},
	}
	counts := make(map[string]int, len(seen))
	for state := range seen {
		counts[state] = 0
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return fmt.Errorf("scan queue depth row: %w", err)
		}
		counts[state] = count
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate queue depth rows: %w", err)
	}
	for state, count := range counts {
		if err := metrics.SetQueueDepth(state, count); err != nil {
			return err
		}
	}
	return nil
}

func getByIdempotencyKey(
	ctx context.Context,
	tx pgx.Tx,
	producerID string,
	idempotencyKey string,
) (command.Command, error) {
	result, err := scanCommand(tx.QueryRow(ctx,
		`SELECT `+commandColumns+`
		 FROM orbit.commands
		 WHERE producer_id = $1 AND idempotency_key = $2`,
		producerID,
		idempotencyKey,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return command.Command{}, command.ErrNotFound
	}
	if err != nil {
		return command.Command{}, fmt.Errorf("get command by idempotency key: %w", err)
	}
	return result, nil
}

func advanceTerminalCursor(ctx context.Context, tx pgx.Tx, deviceID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE orbit.device_cursors AS cursor
		SET last_terminal_sequence = COALESCE(
			(
				SELECT MIN(sequence_number) - 1
				FROM orbit.commands
				WHERE device_id = cursor.device_id
				  AND sequence_number > cursor.last_terminal_sequence
				  AND state NOT IN ('ACKNOWLEDGED', 'EXPIRED', 'DEAD_LETTER', 'CANCELLED')
			),
			cursor.next_sequence_number - 1
		),
		updated_at = clock_timestamp()
		WHERE cursor.device_id = $1`,
		deviceID,
	); err != nil {
		return fmt.Errorf("advance terminal device cursor: %w", err)
	}
	return nil
}

const commandColumns = `
	id::text, producer_id, idempotency_key, device_id, sequence_number, priority,
	payload, payload_hash, request_hash, state::text, created_at, expires_at,
	next_attempt_at, attempt_count, COALESCE(lease_owner, ''), lease_token,
	lease_expires_at, acknowledged_at, COALESCE(failure_reason, '')`

const leasedCommandColumns = `
	leased.id::text, leased.producer_id, leased.idempotency_key, leased.device_id,
	leased.sequence_number, leased.priority, leased.payload, leased.payload_hash,
	leased.request_hash, leased.state::text, leased.created_at, leased.expires_at,
	leased.next_attempt_at, leased.attempt_count, COALESCE(leased.lease_owner, ''),
	leased.lease_token, leased.lease_expires_at, leased.acknowledged_at,
	COALESCE(leased.failure_reason, '')`

const acknowledgedCommandColumns = `
	acknowledged.id::text, acknowledged.producer_id, acknowledged.idempotency_key,
	acknowledged.device_id, acknowledged.sequence_number, acknowledged.priority,
	acknowledged.payload, acknowledged.payload_hash, acknowledged.request_hash,
	acknowledged.state::text, acknowledged.created_at, acknowledged.expires_at,
	acknowledged.next_attempt_at, acknowledged.attempt_count,
	COALESCE(acknowledged.lease_owner, ''), acknowledged.lease_token,
	acknowledged.lease_expires_at, acknowledged.acknowledged_at,
	COALESCE(acknowledged.failure_reason, '')`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCommand(row rowScanner) (command.Command, error) {
	var result command.Command
	var payloadHash []byte
	var requestHash []byte
	if err := row.Scan(
		&result.ID,
		&result.ProducerID,
		&result.IdempotencyKey,
		&result.DeviceID,
		&result.SequenceNumber,
		&result.Priority,
		&result.Payload,
		&payloadHash,
		&requestHash,
		&result.State,
		&result.CreatedAt,
		&result.ExpiresAt,
		&result.NextAttemptAt,
		&result.AttemptCount,
		&result.LeaseOwner,
		&result.LeaseToken,
		&result.LeaseExpiresAt,
		&result.AcknowledgedAt,
		&result.FailureReason,
	); err != nil {
		return command.Command{}, err
	}
	if len(payloadHash) != sha256.Size || len(requestHash) != sha256.Size {
		return command.Command{}, fmt.Errorf("database returned invalid command hash widths")
	}
	copy(result.PayloadHash[:], payloadHash)
	copy(result.RequestHash[:], requestHash)
	if !result.State.IsKnown() {
		return command.Command{}, fmt.Errorf("database returned unknown command state %q", result.State)
	}
	return result, nil
}
