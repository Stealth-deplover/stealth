-- Messaging delivery is a trusted-worker queue. The API only creates an
-- encrypted message payload and recipient snapshots; it never performs a
-- provider network call in the request path.

CREATE TABLE project_messaging_messages (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  topic_id UUID REFERENCES project_messaging_topics(id) ON DELETE SET NULL,
  channel TEXT NOT NULL,
  payload_ciphertext BYTEA NOT NULL,
  request_hash BYTEA NOT NULL,
  idempotency_key TEXT,
  status TEXT NOT NULL DEFAULT 'queued',
  recipient_count INTEGER NOT NULL DEFAULT 0,
  succeeded_count INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  cancelled_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_messaging_messages_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_messaging_messages_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_messaging_messages_channel_valid CHECK (channel IN ('email','sms','push')),
  CONSTRAINT project_messaging_messages_payload_valid CHECK (octet_length(payload_ciphertext) BETWEEN 29 AND 262144),
  CONSTRAINT project_messaging_messages_request_hash_valid CHECK (octet_length(request_hash) = 32),
  CONSTRAINT project_messaging_messages_idempotency_key_valid CHECK (idempotency_key IS NULL OR (char_length(idempotency_key) BETWEEN 1 AND 128 AND idempotency_key !~ '[[:cntrl:]]')),
  CONSTRAINT project_messaging_messages_status_valid CHECK (status IN ('queued','processing','succeeded','failed','cancelled')),
  CONSTRAINT project_messaging_messages_counts_valid CHECK (recipient_count BETWEEN 1 AND 10000 AND succeeded_count >= 0 AND failed_count >= 0 AND succeeded_count + failed_count <= recipient_count),
  CONSTRAINT project_messaging_messages_cancelled_consistency CHECK ((status = 'cancelled') = (cancelled_at IS NOT NULL))
);

CREATE UNIQUE INDEX project_messaging_messages_project_idempotency_unique
  ON project_messaging_messages (project_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
CREATE INDEX project_messaging_messages_project_created_idx
  ON project_messaging_messages (project_id, created_at DESC, id);
CREATE INDEX project_messaging_messages_queue_idx
  ON project_messaging_messages (status, updated_at, id)
  WHERE status IN ('queued','processing');

CREATE TABLE project_messaging_deliveries (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  message_id UUID NOT NULL REFERENCES project_messaging_messages(id) ON DELETE CASCADE,
  subscriber_id UUID REFERENCES project_messaging_subscribers(id) ON DELETE SET NULL,
  provider_id UUID REFERENCES project_messaging_providers(id) ON DELETE SET NULL,
  channel TEXT NOT NULL,
  address_ciphertext BYTEA NOT NULL,
  address_preview TEXT NOT NULL,
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
  CONSTRAINT project_messaging_deliveries_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_messaging_deliveries_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_messaging_deliveries_message_project_fk FOREIGN KEY (project_id, message_id)
    REFERENCES project_messaging_messages (project_id, id) ON DELETE CASCADE,
  CONSTRAINT project_messaging_deliveries_channel_valid CHECK (channel IN ('email','sms','push')),
  CONSTRAINT project_messaging_deliveries_address_ciphertext_valid CHECK (octet_length(address_ciphertext) BETWEEN 29 AND 32768),
  CONSTRAINT project_messaging_deliveries_preview_valid CHECK (char_length(address_preview) BETWEEN 1 AND 255),
  CONSTRAINT project_messaging_deliveries_status_valid CHECK (status IN ('pending','running','succeeded','failed','cancelled')),
  CONSTRAINT project_messaging_deliveries_attempt_count_valid CHECK (attempt_count >= 0 AND attempt_count <= 100),
  CONSTRAINT project_messaging_deliveries_status_code_valid CHECK (last_status_code IS NULL OR last_status_code BETWEEN 100 AND 599),
  CONSTRAINT project_messaging_deliveries_error_length CHECK (last_error IS NULL OR char_length(last_error) <= 4000),
  CONSTRAINT project_messaging_deliveries_terminal_consistency CHECK ((status = 'succeeded') = (delivered_at IS NOT NULL)),
  CONSTRAINT project_messaging_deliveries_message_subscriber_unique UNIQUE (message_id, subscriber_id)
);

CREATE INDEX project_messaging_deliveries_ready_idx
  ON project_messaging_deliveries (next_attempt_at, id)
  WHERE status = 'pending';
CREATE INDEX project_messaging_deliveries_lease_idx
  ON project_messaging_deliveries (leased_at)
  WHERE status = 'running';
CREATE INDEX project_messaging_deliveries_message_created_idx
  ON project_messaging_deliveries (message_id, created_at, id);
