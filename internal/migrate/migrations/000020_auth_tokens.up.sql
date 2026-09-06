-- Auth recovery and verification secrets are stored only as SHA-256 hashes.
-- The plaintext value is delivered once through the configured mailer and is
-- never persisted or returned by the API.

ALTER TABLE accounts
  ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE account_auth_tokens (
  id UUID PRIMARY KEY,
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  token_hash BYTEA NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT account_auth_tokens_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT account_auth_tokens_kind_valid CHECK (kind IN ('email_verification', 'password_reset')),
  CONSTRAINT account_auth_tokens_hash_length CHECK (octet_length(token_hash) = 32),
  CONSTRAINT account_auth_tokens_expiry_valid CHECK (expires_at > created_at),
  CONSTRAINT account_auth_tokens_consumed_valid CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);

CREATE UNIQUE INDEX account_auth_tokens_hash_unique ON account_auth_tokens (token_hash);
CREATE UNIQUE INDEX account_auth_tokens_active_kind_unique
  ON account_auth_tokens (account_id, kind) WHERE consumed_at IS NULL;
CREATE INDEX account_auth_tokens_account_kind_created_idx
  ON account_auth_tokens (account_id, kind, created_at DESC);

CREATE TABLE project_user_auth_tokens (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  project_user_id UUID NOT NULL,
  kind TEXT NOT NULL,
  token_hash BYTEA NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_user_auth_tokens_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_user_auth_tokens_kind_valid CHECK (kind IN ('email_verification', 'password_reset')),
  CONSTRAINT project_user_auth_tokens_hash_length CHECK (octet_length(token_hash) = 32),
  CONSTRAINT project_user_auth_tokens_expiry_valid CHECK (expires_at > created_at),
  CONSTRAINT project_user_auth_tokens_consumed_valid CHECK (consumed_at IS NULL OR consumed_at >= created_at),
  CONSTRAINT project_user_auth_tokens_user_project_fk FOREIGN KEY (project_user_id, project_id)
    REFERENCES project_users(id, project_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX project_user_auth_tokens_hash_unique ON project_user_auth_tokens (token_hash);
CREATE UNIQUE INDEX project_user_auth_tokens_active_kind_unique
  ON project_user_auth_tokens (project_user_id, kind) WHERE consumed_at IS NULL;
CREATE INDEX project_user_auth_tokens_project_user_kind_created_idx
  ON project_user_auth_tokens (project_user_id, kind, created_at DESC);
