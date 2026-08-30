package history

import (
	"context"
	"fmt"
	"time"

	"github.com/JayYarlagadda/orbit/internal/scenario"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Record struct {
	ScenarioName string              `json:"scenario_name"`
	ScenarioSeed string              `json:"scenario_seed"`
	Schedule     scenario.Schedule   `json:"schedule"`
	StartedAt    time.Time           `json:"started_at"`
	FinishedAt   time.Time           `json:"finished_at"`
	AuditEvents  []AuditEvent        `json:"audit_events"`
	Commands     []CommandSnapshot   `json:"commands"`
	Attempts     []DeliveryAttempt   `json:"delivery_attempts"`
	Applications []ClientApplication `json:"client_applications"`
	Lifecycle    []LifecycleEvent    `json:"lifecycle_events"`
}

type AuditEvent struct {
	CommandID     string    `json:"command_id"`
	OldState      string    `json:"old_state"`
	NewState      string    `json:"new_state"`
	Actor         string    `json:"actor"`
	LeaseToken    int64     `json:"lease_token"`
	CorrelationID string    `json:"correlation_id"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type CommandSnapshot struct {
	ID             string    `json:"command_id"`
	DeviceID       string    `json:"device_id"`
	SequenceNumber int64     `json:"sequence_number"`
	State          string    `json:"state"`
	AttemptCount   int32     `json:"attempt_count"`
	LeaseToken     int64     `json:"lease_token"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type DeliveryAttempt struct {
	CommandID    string    `json:"command_id"`
	DeviceID     string    `json:"device_id"`
	LeaseToken   int64     `json:"lease_token"`
	SessionEpoch int64     `json:"session_epoch"`
	StartedAt    time.Time `json:"started_at"`
	Outcome      string    `json:"outcome"`
	Reason       string    `json:"reason"`
}

type ClientApplication struct {
	CommandID      string `json:"command_id"`
	DeviceID       string `json:"device_id"`
	SequenceNumber int64  `json:"sequence_number"`
}

type LifecycleEvent struct {
	AtMS      uint64 `json:"at_ms"`
	Component string `json:"component"`
	Action    string `json:"action"`
	Detail    string `json:"detail,omitempty"`
}

func Collect(ctx context.Context, pool *pgxpool.Pool, base Record) (Record, error) {
	if pool == nil {
		return Record{}, fmt.Errorf("database pool is required")
	}
	record := base
	record.FinishedAt = time.Now().UTC()

	auditRows, err := pool.Query(ctx, `
		SELECT command_id::text, COALESCE(old_state::text, ''), new_state::text,
		       actor, lease_token, correlation_id, occurred_at
		FROM orbit.audit_events
		ORDER BY occurred_at, event_id`)
	if err != nil {
		return Record{}, fmt.Errorf("query audit events: %w", err)
	}
	defer auditRows.Close()
	for auditRows.Next() {
		var item AuditEvent
		if err := auditRows.Scan(
			&item.CommandID,
			&item.OldState,
			&item.NewState,
			&item.Actor,
			&item.LeaseToken,
			&item.CorrelationID,
			&item.OccurredAt,
		); err != nil {
			return Record{}, fmt.Errorf("scan audit event: %w", err)
		}
		record.AuditEvents = append(record.AuditEvents, item)
	}
	if err := auditRows.Err(); err != nil {
		return Record{}, fmt.Errorf("iterate audit events: %w", err)
	}

	commandRows, err := pool.Query(ctx, `
		SELECT id::text, device_id, sequence_number, state::text,
		       attempt_count, lease_token, expires_at
		FROM orbit.commands
		ORDER BY device_id, sequence_number`)
	if err != nil {
		return Record{}, fmt.Errorf("query commands: %w", err)
	}
	defer commandRows.Close()
	for commandRows.Next() {
		var item CommandSnapshot
		if err := commandRows.Scan(
			&item.ID,
			&item.DeviceID,
			&item.SequenceNumber,
			&item.State,
			&item.AttemptCount,
			&item.LeaseToken,
			&item.ExpiresAt,
		); err != nil {
			return Record{}, fmt.Errorf("scan command: %w", err)
		}
		record.Commands = append(record.Commands, item)
	}
	if err := commandRows.Err(); err != nil {
		return Record{}, fmt.Errorf("iterate commands: %w", err)
	}

	attemptRows, err := pool.Query(ctx, `
		SELECT attempts.command_id::text, commands.device_id, attempts.lease_token,
		       attempts.session_epoch, attempts.started_at,
		       attempts.outcome::text, COALESCE(attempts.reason, '')
		FROM orbit.delivery_attempts attempts
		JOIN orbit.commands commands ON commands.id = attempts.command_id
		ORDER BY attempts.started_at, attempts.id`)
	if err != nil {
		return Record{}, fmt.Errorf("query delivery attempts: %w", err)
	}
	defer attemptRows.Close()
	for attemptRows.Next() {
		var item DeliveryAttempt
		if err := attemptRows.Scan(
			&item.CommandID,
			&item.DeviceID,
			&item.LeaseToken,
			&item.SessionEpoch,
			&item.StartedAt,
			&item.Outcome,
			&item.Reason,
		); err != nil {
			return Record{}, fmt.Errorf("scan delivery attempt: %w", err)
		}
		record.Attempts = append(record.Attempts, item)
	}
	if err := attemptRows.Err(); err != nil {
		return Record{}, fmt.Errorf("iterate delivery attempts: %w", err)
	}
	return record, nil
}
