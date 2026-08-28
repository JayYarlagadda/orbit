-- Supports the terminal expiration sweep, which selects non-terminal commands
-- whose TTL has elapsed. Without this index the sweep degrades to a sequential
-- scan of orbit.commands as the backlog grows.
CREATE INDEX commands_expiry_idx
  ON orbit.commands (expires_at, id)
  WHERE state IN ('QUEUED', 'RETRY_WAIT');

-- Lease selection joins device_cursors and filters on the owning gateway. The
-- primary key is device_id, so gateway-scoped selection had no usable index.
CREATE INDEX device_cursors_active_gateway_idx
  ON orbit.device_cursors (active_gateway_id)
  WHERE active_gateway_id IS NOT NULL;
