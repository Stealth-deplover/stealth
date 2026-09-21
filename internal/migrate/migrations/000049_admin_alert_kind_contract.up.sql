-- The active alert-rule contract is limited to kinds with an implemented API
-- validator and evaluator. Older schemas admitted backup_failure/job_failure
-- even though no control-plane or worker path could create or evaluate them.
-- Preserve any such legacy definitions in a private retirement table before
-- tightening the active constraint; alert events and deliveries are already
-- independent history after migration 000047.
CREATE TABLE admin_alert_rule_retired (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  condition JSONB NOT NULL,
  severity TEXT NOT NULL,
  for_seconds INTEGER NOT NULL,
  enabled BOOLEAN NOT NULL,
  state TEXT NOT NULL,
  pending_since TIMESTAMPTZ,
  last_evaluated_at TIMESTAMPTZ,
  last_value DOUBLE PRECISION,
  last_error TEXT,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  retired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  retirement_reason TEXT NOT NULL DEFAULT 'unsupported alert kind'
);

INSERT INTO admin_alert_rule_retired (
  id,name,kind,condition,severity,for_seconds,enabled,state,pending_since,
  last_evaluated_at,last_value,last_error,created_by_account_id,created_at,updated_at
)
SELECT id,name,kind,condition,severity,for_seconds,enabled,state,pending_since,
       last_evaluated_at,last_value,last_error,created_by_account_id,created_at,updated_at
FROM admin_alert_rules
WHERE kind IN ('backup_failure','job_failure');

DELETE FROM admin_alert_rules
WHERE kind IN ('backup_failure','job_failure');

ALTER TABLE admin_alert_rules
  DROP CONSTRAINT IF EXISTS admin_alert_rules_kind_valid,
  ADD CONSTRAINT admin_alert_rules_kind_valid CHECK (kind IN (
    'metric_threshold',
    'error_rate',
    'latency',
    'log_match',
    'monitor_failure',
    'service_health',
    'disk_pressure',
    'heartbeat_failure',
    'certificate_expiry'
  ));

CREATE INDEX admin_alert_rule_retired_time_idx
  ON admin_alert_rule_retired (retired_at DESC, id DESC);
