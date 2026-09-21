-- Durable, bounded invalidation events for the instance-admin control room.
-- These rows are notifications, not a second source of truth. The API reads
-- the current PostgreSQL state after receiving an event.
CREATE TABLE admin_realtime_events (
  id UUID PRIMARY KEY,
  event_name TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id UUID,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '24 hours'),
  CONSTRAINT admin_realtime_events_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_realtime_events_name_valid CHECK (event_name ~ '^admin\.[a-z0-9][a-z0-9._-]{1,159}$'),
  CONSTRAINT admin_realtime_events_target_type_valid CHECK (char_length(target_type) BETWEEN 3 AND 80 AND target_type !~ '[[:cntrl:]]'),
  CONSTRAINT admin_realtime_events_payload_valid CHECK (octet_length(payload::text) <= 16384),
  CONSTRAINT admin_realtime_events_expiry_valid CHECK (expires_at > occurred_at)
);

CREATE INDEX admin_realtime_events_expiry_idx
  ON admin_realtime_events (expires_at, id);
