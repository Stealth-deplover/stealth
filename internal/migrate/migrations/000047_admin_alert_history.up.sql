-- Alert rules are live configuration. Alert events and their deliveries are
-- durable history, so deleting a rule must retain the event that created each
-- normal delivery. Snapshots make that history understandable without a live
-- rule row and keep retrying a queued delivery independent of the rule.
ALTER TABLE admin_alert_events
  ADD COLUMN IF NOT EXISTS rule_id_snapshot UUID,
  ADD COLUMN IF NOT EXISTS rule_name_snapshot TEXT,
  ADD COLUMN IF NOT EXISTS rule_kind_snapshot TEXT,
  ADD COLUMN IF NOT EXISTS severity_snapshot TEXT,
  ADD COLUMN IF NOT EXISTS condition_snapshot JSONB;

UPDATE admin_alert_events e
SET rule_id_snapshot = e.rule_id,
    rule_name_snapshot = COALESCE(r.name, 'Deleted alert rule'),
    rule_kind_snapshot = COALESCE(r.kind, 'historical'),
    severity_snapshot = COALESCE(r.severity, 'warning'),
    condition_snapshot = COALESCE(r.condition, '{}'::jsonb)
FROM admin_alert_rules r
WHERE r.id = e.rule_id
  AND (e.rule_id_snapshot IS NULL
       OR e.rule_name_snapshot IS NULL
       OR e.rule_kind_snapshot IS NULL
       OR e.severity_snapshot IS NULL
       OR e.condition_snapshot IS NULL);

-- The released schema made rule_id mandatory and referentially valid, so the
-- fallback path is only for rows a privileged operator may previously have
-- repaired outside that contract. It keeps the forward migration recoverable
-- without inventing live rule state.
UPDATE admin_alert_events
SET rule_id_snapshot = COALESCE(rule_id_snapshot, rule_id),
    rule_name_snapshot = COALESCE(rule_name_snapshot, 'Deleted alert rule'),
    rule_kind_snapshot = COALESCE(rule_kind_snapshot, 'historical'),
    severity_snapshot = COALESCE(severity_snapshot, 'warning'),
    condition_snapshot = COALESCE(condition_snapshot, '{}'::jsonb)
WHERE rule_id_snapshot IS NULL
   OR rule_name_snapshot IS NULL
   OR rule_kind_snapshot IS NULL
   OR severity_snapshot IS NULL
   OR condition_snapshot IS NULL;

ALTER TABLE admin_alert_events
  ALTER COLUMN rule_id_snapshot SET NOT NULL,
  ALTER COLUMN rule_name_snapshot SET NOT NULL,
  ALTER COLUMN rule_kind_snapshot SET NOT NULL,
  ALTER COLUMN severity_snapshot SET NOT NULL,
  ALTER COLUMN condition_snapshot SET NOT NULL;

ALTER TABLE admin_alert_events
  ADD CONSTRAINT admin_alert_events_rule_name_snapshot_valid
    CHECK (char_length(rule_name_snapshot) BETWEEN 1 AND 120 AND rule_name_snapshot !~ '[[:cntrl:]]'),
  ADD CONSTRAINT admin_alert_events_rule_kind_snapshot_valid
    CHECK (char_length(rule_kind_snapshot) BETWEEN 1 AND 120 AND rule_kind_snapshot !~ '[[:cntrl:]]'),
  ADD CONSTRAINT admin_alert_events_severity_snapshot_valid
    CHECK (severity_snapshot IN ('info','warning','critical'));

ALTER TABLE admin_alert_events
  DROP CONSTRAINT IF EXISTS admin_alert_events_rule_id_fkey;

ALTER TABLE admin_alert_events
  ALTER COLUMN rule_id DROP NOT NULL,
  ADD CONSTRAINT admin_alert_events_rule_id_fkey
    FOREIGN KEY (rule_id) REFERENCES admin_alert_rules(id) ON DELETE SET NULL;

CREATE INDEX admin_alert_events_rule_snapshot_time_idx
  ON admin_alert_events (rule_id_snapshot, occurred_at DESC, id DESC);
