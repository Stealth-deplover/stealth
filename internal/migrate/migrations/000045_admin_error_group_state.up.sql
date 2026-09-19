-- Error groups are a low-volume control-plane projection of ClickHouse
-- occurrences. The occurrences and messages remain in ClickHouse; this table
-- stores only the operator's lifecycle state for a safe fingerprint.
CREATE TABLE admin_error_group_states (
  fingerprint TEXT PRIMARY KEY,
  status TEXT NOT NULL DEFAULT 'open',
  acknowledged_at TIMESTAMPTZ,
  resolved_at TIMESTAMPTZ,
  ignored_at TIMESTAMPTZ,
  updated_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_error_group_fingerprint_valid CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
  CONSTRAINT admin_error_group_status_valid CHECK (status IN ('open','acknowledged','resolved','ignored')),
  CONSTRAINT admin_error_group_ack_time_valid CHECK ((status = 'acknowledged') = (acknowledged_at IS NOT NULL)),
  CONSTRAINT admin_error_group_resolved_time_valid CHECK ((status = 'resolved') = (resolved_at IS NOT NULL)),
  CONSTRAINT admin_error_group_ignored_time_valid CHECK ((status = 'ignored') = (ignored_at IS NOT NULL))
);
CREATE INDEX admin_error_group_state_updated_idx ON admin_error_group_states (updated_at DESC, fingerprint);
