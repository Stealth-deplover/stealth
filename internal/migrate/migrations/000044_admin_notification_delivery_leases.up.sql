ALTER TABLE admin_notification_deliveries
  DROP CONSTRAINT IF EXISTS admin_notification_deliveries_status_valid;

ALTER TABLE admin_notification_deliveries
  ADD COLUMN IF NOT EXISTS leased_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS worker_id TEXT;

ALTER TABLE admin_notification_deliveries
  ADD CONSTRAINT admin_notification_deliveries_status_valid
  CHECK (status IN ('pending','running','delivered','failed'));

ALTER TABLE admin_notification_deliveries
  ADD CONSTRAINT admin_notification_deliveries_worker_valid
  CHECK (worker_id IS NULL OR (char_length(worker_id) BETWEEN 1 AND 128 AND worker_id !~ '[[:cntrl:]]'));

CREATE INDEX IF NOT EXISTS admin_notification_deliveries_lease_idx
  ON admin_notification_deliveries (leased_at, id)
  WHERE status = 'running';
