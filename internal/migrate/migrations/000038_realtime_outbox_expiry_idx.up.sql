-- Expiry maintenance selects the oldest retained rows globally. Keep that
-- bounded scan indexed without changing the seven-day retention policy.
CREATE INDEX webhook_events_expiry_idx ON webhook_events (expires_at, id);
