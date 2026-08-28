package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/JayYarlagadda/orbit/internal/session"
	"github.com/jackc/pgx/v5"
)

func (s *CommandStore) AcquireSession(ctx context.Context, acquisition session.Acquisition) (session.Session, error) {
	if s.pool == nil {
		return session.Session{}, fmt.Errorf("acquire session: database pool is nil")
	}
	validated, err := session.NewAcquisition(acquisition.DeviceID, acquisition.GatewayID, acquisition.ClientInstanceID)
	if err != nil {
		return session.Session{}, err
	}
	now := s.clock.Now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return session.Session{}, fmt.Errorf("begin session transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO orbit.device_cursors (device_id) VALUES ($1) ON CONFLICT DO NOTHING`,
		validated.DeviceID,
	); err != nil {
		return session.Session{}, fmt.Errorf("ensure session device cursor: %w", err)
	}
	var result session.Session
	if err := tx.QueryRow(ctx, `
		UPDATE orbit.device_cursors
		SET active_session_epoch = active_session_epoch + 1,
			active_gateway_id = $2,
			active_client_instance_id = $3,
			updated_at = $4
		WHERE device_id = $1
		RETURNING device_id, active_gateway_id, active_client_instance_id,
			active_session_epoch, updated_at`,
		validated.DeviceID,
		validated.GatewayID,
		validated.ClientInstanceID,
		now,
	).Scan(
		&result.DeviceID,
		&result.GatewayID,
		&result.ClientInstanceID,
		&result.Epoch,
		&result.AcquiredAt,
	); err != nil {
		return session.Session{}, fmt.Errorf("advance session epoch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return session.Session{}, fmt.Errorf("commit session transaction: %w", err)
	}
	return result, nil
}

func (s *CommandStore) ReleaseSession(ctx context.Context, deviceID, gatewayID string, epoch int64) error {
	if s.pool == nil {
		return fmt.Errorf("release session: database pool is nil")
	}
	result, err := s.pool.Exec(ctx, `
		UPDATE orbit.device_cursors
		SET active_gateway_id = NULL,
			active_client_instance_id = NULL,
			updated_at = $4
		WHERE device_id = $1
		  AND active_gateway_id = $2
		  AND active_session_epoch = $3`,
		deviceID,
		gatewayID,
		epoch,
		s.clock.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("release session: %w", err)
	}
	if result.RowsAffected() != 1 {
		return session.ErrStale
	}
	return nil
}

func (s *CommandStore) AssertSession(ctx context.Context, deviceID, gatewayID string, epoch int64) error {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT true
		FROM orbit.device_cursors
		WHERE device_id = $1 AND active_gateway_id = $2 AND active_session_epoch = $3`,
		deviceID,
		gatewayID,
		epoch,
	).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.ErrStale
	}
	if err != nil {
		return fmt.Errorf("assert session: %w", err)
	}
	return nil
}
