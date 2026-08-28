CREATE SCHEMA orbit;

COMMENT ON SCHEMA orbit IS
  'Authoritative state for Orbit command delivery and audit history';

CREATE TYPE orbit.command_state AS ENUM (
  'QUEUED',
  'LEASED',
  'IN_FLIGHT',
  'RETRY_WAIT',
  'ACKNOWLEDGED',
  'EXPIRED',
  'DEAD_LETTER',
  'CANCELLED'
);

CREATE TABLE orbit.device_cursors (
  device_id varchar(64) PRIMARY KEY,
  next_sequence_number bigint NOT NULL DEFAULT 1
    CHECK (next_sequence_number > 0),
  last_terminal_sequence bigint NOT NULL DEFAULT 0
    CHECK (last_terminal_sequence >= 0),
  active_session_epoch bigint NOT NULL DEFAULT 0
    CHECK (active_session_epoch >= 0),
  active_gateway_id varchar(64),
	active_client_instance_id varchar(64),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
	CHECK ((active_gateway_id IS NULL) = (active_client_instance_id IS NULL)),
  CHECK (last_terminal_sequence < next_sequence_number)
);

CREATE TABLE orbit.commands (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  producer_id varchar(64) NOT NULL,
  idempotency_key varchar(128) NOT NULL,
  device_id varchar(64) NOT NULL REFERENCES orbit.device_cursors(device_id),
  sequence_number bigint NOT NULL CHECK (sequence_number > 0),
  priority smallint NOT NULL CHECK (priority BETWEEN 0 AND 9),
  payload bytea NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 65536),
  payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
  request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
  state orbit.command_state NOT NULL DEFAULT 'QUEUED',
  created_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  next_attempt_at timestamptz NOT NULL,
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  lease_owner varchar(128),
  lease_token bigint NOT NULL DEFAULT 0 CHECK (lease_token >= 0),
  lease_expires_at timestamptz,
  acknowledged_at timestamptz,
  failure_reason text,
  UNIQUE (producer_id, idempotency_key),
  UNIQUE (device_id, sequence_number),
  CHECK (expires_at > created_at),
  CHECK (
    (state IN ('LEASED', 'IN_FLIGHT')) =
    (lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL AND lease_token > 0)
  ),
  CHECK ((state = 'ACKNOWLEDGED') = (acknowledged_at IS NOT NULL)),
  CHECK (state <> 'DEAD_LETTER' OR failure_reason IS NOT NULL)
);

CREATE INDEX commands_eligible_idx
  ON orbit.commands (next_attempt_at, priority DESC, created_at, id)
  WHERE state IN ('QUEUED', 'RETRY_WAIT');

CREATE INDEX commands_expired_lease_idx
  ON orbit.commands (lease_expires_at, id)
  WHERE state IN ('LEASED', 'IN_FLIGHT');

CREATE TABLE orbit.delivery_attempts (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  command_id uuid NOT NULL REFERENCES orbit.commands(id),
  attempt_number integer NOT NULL CHECK (attempt_number > 0),
  gateway_id varchar(64) NOT NULL,
  session_epoch bigint NOT NULL CHECK (session_epoch > 0),
  lease_token bigint NOT NULL CHECK (lease_token > 0),
  started_at timestamptz NOT NULL,
  finished_at timestamptz,
  outcome varchar(32),
  reason text,
	result_hash bytea CHECK (result_hash IS NULL OR octet_length(result_hash) = 32),
	client_applied_at timestamptz,
  UNIQUE (command_id, attempt_number),
  CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE INDEX delivery_attempts_command_idx
  ON orbit.delivery_attempts (command_id, attempt_number);

CREATE TABLE orbit.audit_events (
  event_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  command_id uuid NOT NULL REFERENCES orbit.commands(id),
  old_state orbit.command_state,
  new_state orbit.command_state NOT NULL,
  actor varchar(128) NOT NULL,
  lease_token bigint,
  occurred_at timestamptz NOT NULL,
  correlation_id varchar(128) NOT NULL,
  details jsonb NOT NULL DEFAULT '{}'::jsonb,
  CHECK (actor <> ''),
  CHECK (correlation_id <> ''),
  CHECK (old_state IS NULL OR old_state <> new_state),
  CHECK (lease_token IS NULL OR lease_token >= 0)
);

CREATE INDEX audit_events_command_idx
  ON orbit.audit_events (command_id, event_id);
