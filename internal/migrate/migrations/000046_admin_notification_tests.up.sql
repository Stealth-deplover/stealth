ALTER TABLE admin_notification_deliveries
  ADD COLUMN IF NOT EXISTS test_message TEXT;

ALTER TABLE admin_notification_deliveries
  DROP CONSTRAINT IF EXISTS admin_notification_deliveries_source_valid;

ALTER TABLE admin_notification_deliveries
  ADD CONSTRAINT admin_notification_deliveries_source_valid
  CHECK (alert_event_id IS NOT NULL OR test_message IS NOT NULL);

ALTER TABLE admin_notification_deliveries
  ADD CONSTRAINT admin_notification_deliveries_test_message_valid
  CHECK (test_message IS NULL OR (char_length(test_message) BETWEEN 1 AND 1000 AND test_message !~ '[[:cntrl:]]'));
