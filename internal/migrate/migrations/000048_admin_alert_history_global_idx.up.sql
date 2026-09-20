-- Global alert history uses occurred_at/id ordering independently of the
-- per-rule snapshot index introduced by migration 000047.
CREATE INDEX admin_alert_events_time_id_idx
  ON admin_alert_events (occurred_at DESC, id DESC);
