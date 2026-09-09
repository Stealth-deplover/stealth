-- Extend the existing transactional webhook event table into the durable
-- notification outbox. Existing webhook delivery semantics remain intact;
-- these fields track publication to the optional Redis fanout only.

ALTER TABLE webhook_events
  ADD COLUMN organization_id UUID,
  ADD COLUMN event_version INTEGER NOT NULL DEFAULT 1,
  ADD COLUMN occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN correlation_id TEXT,
  ADD COLUMN available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN publish_attempts INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN publish_status TEXT NOT NULL DEFAULT 'pending',
  ADD COLUMN published_at TIMESTAMPTZ,
  ADD COLUMN publish_leased_at TIMESTAMPTZ,
  ADD COLUMN publish_worker_id TEXT,
  ADD COLUMN last_publish_error TEXT;

UPDATE webhook_events e
SET organization_id = p.organization_id,
    occurred_at = COALESCE(e.created_at, now())
FROM projects p
WHERE p.id = e.project_id AND e.organization_id IS NULL;

ALTER TABLE webhook_events
  ALTER COLUMN organization_id SET NOT NULL,
  ADD CONSTRAINT webhook_events_organization_fk FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE,
  ADD CONSTRAINT webhook_events_version_valid CHECK (event_version BETWEEN 1 AND 100),
  ADD CONSTRAINT webhook_events_publish_status_valid CHECK (publish_status IN ('pending','published','failed')),
  ADD CONSTRAINT webhook_events_publish_attempts_valid CHECK (publish_attempts BETWEEN 0 AND 100),
  ADD CONSTRAINT webhook_events_correlation_length CHECK (correlation_id IS NULL OR char_length(correlation_id) <= 128),
  ADD CONSTRAINT webhook_events_publish_error_length CHECK (last_publish_error IS NULL OR char_length(last_publish_error) <= 4000);

CREATE INDEX webhook_events_publish_ready_idx
  ON webhook_events (available_at, id)
  WHERE publish_status = 'pending';

CREATE INDEX webhook_events_publish_lease_idx
  ON webhook_events (publish_leased_at)
  WHERE publish_status = 'pending' AND publish_leased_at IS NOT NULL;
