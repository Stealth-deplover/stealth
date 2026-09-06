-- Messaging is a durable project control plane. Provider credentials and
-- subscriber addresses are encrypted at rest; only safe metadata is returned
-- by the Console/API. Delivery is intentionally a separate trusted-worker
-- concern so configuring a provider can never imply a sent message.

ALTER TABLE project_api_keys
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_supported,
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_nonempty,
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_canonical;

ALTER TABLE project_api_keys
  ADD CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY[
    'users.read','users.write',
    'databases.read','databases.write',
    'storage.read','storage.write',
    'functions.read','functions.write',
    'sites.read','sites.write',
    'webhooks.read','webhooks.write',
    'realtime.read',
    'messaging.read','messaging.write'
  ]::text[]),
  ADD CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 15),
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
    (cardinality(scopes) < 12 OR scopes[11] < scopes[12]) AND
    (cardinality(scopes) < 13 OR scopes[12] < scopes[13]) AND
    (cardinality(scopes) < 14 OR scopes[13] < scopes[14]) AND
    (cardinality(scopes) < 15 OR scopes[14] < scopes[15])
  );

CREATE TABLE project_messaging_providers (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  channel TEXT NOT NULL,
  provider TEXT NOT NULL,
  credentials_ciphertext BYTEA NOT NULL,
  credentials_present BOOLEAN NOT NULL DEFAULT false,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_messaging_providers_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_messaging_providers_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_messaging_providers_name_valid CHECK (char_length(name) BETWEEN 2 AND 120 AND name = btrim(name)),
  CONSTRAINT project_messaging_providers_channel_valid CHECK (channel IN ('email','sms','push')),
  CONSTRAINT project_messaging_providers_provider_valid CHECK (char_length(provider) BETWEEN 1 AND 64 AND provider = btrim(provider) AND provider !~ '[[:cntrl:]]'),
  CONSTRAINT project_messaging_providers_credentials_valid CHECK (octet_length(credentials_ciphertext) BETWEEN 29 AND 32768)
);

CREATE UNIQUE INDEX project_messaging_providers_project_name_unique
  ON project_messaging_providers (project_id, name);
CREATE INDEX project_messaging_providers_project_channel_idx
  ON project_messaging_providers (project_id, channel, id);

CREATE TABLE project_messaging_topics (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_messaging_topics_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_messaging_topics_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_messaging_topics_name_valid CHECK (char_length(name) BETWEEN 2 AND 120 AND name = btrim(name)),
  CONSTRAINT project_messaging_topics_description_valid CHECK (char_length(description) <= 2000)
);

CREATE UNIQUE INDEX project_messaging_topics_project_name_unique
  ON project_messaging_topics (project_id, name);
CREATE INDEX project_messaging_topics_project_enabled_idx
  ON project_messaging_topics (project_id, enabled, id);

CREATE TABLE project_messaging_subscribers (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  topic_id UUID NOT NULL REFERENCES project_messaging_topics(id) ON DELETE CASCADE,
  channel TEXT NOT NULL,
  address_ciphertext BYTEA NOT NULL,
  address_hash BYTEA NOT NULL,
  address_preview TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_messaging_subscribers_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_messaging_subscribers_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_messaging_subscribers_channel_valid CHECK (channel IN ('email','sms','push')),
  CONSTRAINT project_messaging_subscribers_address_ciphertext_valid CHECK (octet_length(address_ciphertext) BETWEEN 29 AND 32768),
  CONSTRAINT project_messaging_subscribers_address_hash_valid CHECK (octet_length(address_hash) = 32),
  CONSTRAINT project_messaging_subscribers_preview_valid CHECK (char_length(address_preview) BETWEEN 1 AND 255),
  CONSTRAINT project_messaging_subscribers_topic_project_fk FOREIGN KEY (project_id, topic_id)
    REFERENCES project_messaging_topics (project_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX project_messaging_subscribers_topic_address_unique
  ON project_messaging_subscribers (topic_id, channel, address_hash);
CREATE INDEX project_messaging_subscribers_project_topic_idx
  ON project_messaging_subscribers (project_id, topic_id, id);
