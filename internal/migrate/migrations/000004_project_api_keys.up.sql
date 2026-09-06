CREATE TABLE project_api_keys (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL,
  secret_hash BYTEA NOT NULL,
  scopes TEXT[] NOT NULL,
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_api_keys_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_api_keys_name_length CHECK (char_length(name) BETWEEN 2 AND 120),
  CONSTRAINT project_api_keys_name_trimmed CHECK (name = btrim(name)),
  CONSTRAINT project_api_keys_prefix_valid CHECK (prefix ~ '^stl_key_[A-Za-z0-9_-]{8}$'),
  CONSTRAINT project_api_keys_secret_hash_length CHECK (octet_length(secret_hash) = 32),
  CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 2),
  CONSTRAINT project_api_keys_scopes_nonnull CHECK (array_position(scopes, NULL) IS NULL),
  CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY['users.read','users.write']::text[]),
  CONSTRAINT project_api_keys_scopes_canonical CHECK (
    cardinality(scopes) = 1 OR (cardinality(scopes) = 2 AND scopes[1] < scopes[2])
  )
);

CREATE UNIQUE INDEX project_api_keys_secret_hash_unique ON project_api_keys (secret_hash);
CREATE INDEX project_api_keys_project_id_id_idx ON project_api_keys (project_id, id);
CREATE INDEX project_api_keys_project_active_idx ON project_api_keys (project_id, revoked_at, expires_at);
