CREATE TABLE accounts (
  id UUID PRIMARY KEY,
  email TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT accounts_email_normalized CHECK (email = lower(email) AND email = btrim(email)),
  CONSTRAINT accounts_email_length CHECK (char_length(email) BETWEEN 3 AND 320)
);
CREATE UNIQUE INDEX accounts_email_unique ON accounts (email);

CREATE TABLE sessions (
  id UUID PRIMARY KEY,
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  token_hash BYTEA NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT sessions_token_hash_length CHECK (octet_length(token_hash) = 32)
);
CREATE UNIQUE INDEX sessions_token_hash_unique ON sessions (token_hash);
CREATE INDEX sessions_account_id_expires_at_idx ON sessions (account_id, expires_at DESC);

CREATE TABLE organizations (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  slug TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT organizations_name_length CHECK (char_length(name) BETWEEN 2 AND 120),
  CONSTRAINT organizations_slug_valid CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$')
);
CREATE UNIQUE INDEX organizations_slug_unique ON organizations (slug);

CREATE TABLE organization_memberships (
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id, account_id),
  CONSTRAINT organization_memberships_role_valid CHECK (role IN ('owner', 'admin', 'developer', 'viewer', 'billing'))
);
CREATE INDEX organization_memberships_account_id_idx ON organization_memberships (account_id, organization_id);

CREATE TABLE projects (
  id UUID PRIMARY KEY,
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT projects_name_valid CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,62}$')
);
CREATE UNIQUE INDEX projects_organization_name_unique ON projects (organization_id, name);
CREATE INDEX projects_organization_id_created_at_idx ON projects (organization_id, created_at DESC);

CREATE TABLE audit_events (
  id UUID PRIMARY KEY,
  organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
  actor_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id UUID,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT audit_events_action_length CHECK (char_length(action) BETWEEN 3 AND 120),
  CONSTRAINT audit_events_target_type_length CHECK (char_length(target_type) BETWEEN 3 AND 80)
);
CREATE INDEX audit_events_organization_created_at_idx ON audit_events (organization_id, created_at DESC);
CREATE INDEX audit_events_actor_created_at_idx ON audit_events (actor_account_id, created_at DESC);
