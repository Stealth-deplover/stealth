CREATE TABLE instance_roles (
  account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT instance_roles_role_valid CHECK (role IN ('instance_owner', 'instance_admin'))
);
CREATE UNIQUE INDEX instance_roles_single_owner ON instance_roles (role) WHERE role = 'instance_owner';

CREATE TABLE instance_bootstrap (
  id BOOLEAN PRIMARY KEY DEFAULT TRUE,
  sealed_at TIMESTAMPTZ,
  sealed_reason TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT instance_bootstrap_singleton CHECK (id),
  CONSTRAINT instance_bootstrap_sealed_reason CHECK (sealed_at IS NOT NULL OR sealed_reason IS NULL)
);

INSERT INTO instance_bootstrap (id, sealed_at, sealed_reason)
SELECT TRUE,
       CASE WHEN EXISTS (SELECT 1 FROM accounts) THEN now() ELSE NULL END,
       CASE WHEN EXISTS (SELECT 1 FROM accounts) THEN 'legacy_installation' ELSE NULL END;

CREATE TABLE bootstrap_sessions (
  id UUID PRIMARY KEY,
  code_hash BYTEA NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at TIMESTAMPTZ,
  invalidated_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT bootstrap_sessions_code_hash_length CHECK (octet_length(code_hash) = 32)
);
CREATE INDEX bootstrap_sessions_active_idx ON bootstrap_sessions (expires_at DESC)
  WHERE used_at IS NULL AND invalidated_at IS NULL;
