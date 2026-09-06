-- Project webhooks use a transactional outbox. API mutations write an event
-- and its initial delivery rows in the same PostgreSQL transaction, so a
-- process crash cannot acknowledge a mutation while losing its notification.

ALTER TABLE project_api_keys
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_supported;

ALTER TABLE project_api_keys
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_nonempty,
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_canonical;

ALTER TABLE project_api_keys
  ADD CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY[
    'users.read','users.write',
    'databases.read','databases.write',
    'storage.read','storage.write',
    'functions.read','functions.write',
    'sites.read','sites.write',
    'webhooks.read','webhooks.write'
  ]::text[]);

ALTER TABLE project_api_keys
  ADD CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 12),
  ADD CONSTRAINT project_api_keys_scopes_canonical CHECK (
    (cardinality(scopes) < 2 OR scopes[1] < scopes[2]) AND
    (cardinality(scopes) < 3 OR scopes[2] < scopes[3]) AND
    (cardinality(scopes) < 4 OR scopes[3] < scopes[4]) AND
    (cardinality(scopes) < 5 OR scopes[4] < scopes[5]) AND
    (cardinality(scopes) < 6 OR scopes[5] < scopes[6]) AND
    (cardinality(scopes) < 7 OR scopes[6] < scopes[7]) AND
    (cardinality(scopes) < 8 OR scopes[7] < scopes[8]) AND
    (cardinality(scopes) < 9 OR scopes[8] < scopes[9]) AND
    (cardinality(scopes) < 10 OR scopes[9] < scopes[10]) AND
    (cardinality(scopes) < 11 OR scopes[10] < scopes[11]) AND
    (cardinality(scopes) < 12 OR scopes[11] < scopes[12])
  );

CREATE TABLE project_webhooks (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  secret_ciphertext BYTEA NOT NULL,
  events TEXT[] NOT NULL DEFAULT ARRAY['*']::text[],
  enabled BOOLEAN NOT NULL DEFAULT true,
  failure_count INTEGER NOT NULL DEFAULT 0,
  last_delivery_at TIMESTAMPTZ,
  last_failure_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_webhooks_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_webhooks_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_webhooks_name_valid CHECK (char_length(name) BETWEEN 2 AND 120 AND name = btrim(name)),
  CONSTRAINT project_webhooks_url_valid CHECK (char_length(url) BETWEEN 12 AND 2048 AND url ~ '^https://[^[:space:]#]+$'),
  CONSTRAINT project_webhooks_secret_ciphertext_valid CHECK (octet_length(secret_ciphertext) BETWEEN 29 AND 4096),
  CONSTRAINT project_webhooks_events_valid CHECK (cardinality(events) BETWEEN 1 AND 64 AND array_position(events, NULL) IS NULL),
  CONSTRAINT project_webhooks_failure_count_valid CHECK (failure_count >= 0)
);

CREATE UNIQUE INDEX project_webhooks_project_name_unique ON project_webhooks (project_id, name);
CREATE INDEX project_webhooks_project_enabled_idx ON project_webhooks (project_id, enabled, id);

CREATE TABLE webhook_events (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  event_name TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id UUID,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '7 days'),
  CONSTRAINT webhook_events_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT webhook_events_event_name_valid CHECK (char_length(event_name) BETWEEN 3 AND 160),
  CONSTRAINT webhook_events_target_type_valid CHECK (char_length(target_type) BETWEEN 3 AND 80),
  CONSTRAINT webhook_events_payload_valid CHECK (octet_length(payload::text) <= 262144),
  CONSTRAINT webhook_events_expiry_valid CHECK (expires_at > created_at)
);

CREATE INDEX webhook_events_project_created_idx ON webhook_events (project_id, created_at, id);

CREATE TABLE webhook_deliveries (
  id UUID PRIMARY KEY,
  event_id UUID NOT NULL REFERENCES webhook_events(id) ON DELETE CASCADE,
  webhook_id UUID NOT NULL REFERENCES project_webhooks(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending',
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  leased_at TIMESTAMPTZ,
  worker_id TEXT,
  last_status_code INTEGER,
  last_error TEXT,
  delivered_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT webhook_deliveries_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT webhook_deliveries_status_valid CHECK (status IN ('pending','running','succeeded','failed')),
  CONSTRAINT webhook_deliveries_attempt_count_valid CHECK (attempt_count >= 0 AND attempt_count <= 100),
  CONSTRAINT webhook_deliveries_status_code_valid CHECK (last_status_code IS NULL OR last_status_code BETWEEN 100 AND 599),
  CONSTRAINT webhook_deliveries_error_length CHECK (last_error IS NULL OR char_length(last_error) <= 4000),
  CONSTRAINT webhook_deliveries_terminal_consistency CHECK ((status = 'succeeded') = (delivered_at IS NOT NULL)),
  CONSTRAINT webhook_deliveries_event_webhook_unique UNIQUE (event_id, webhook_id)
);

CREATE INDEX webhook_deliveries_ready_idx ON webhook_deliveries (next_attempt_at, id) WHERE status = 'pending';
CREATE INDEX webhook_deliveries_lease_idx ON webhook_deliveries (leased_at) WHERE status = 'running';
CREATE INDEX webhook_deliveries_webhook_created_idx ON webhook_deliveries (webhook_id, created_at DESC, id);
