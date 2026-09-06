-- Organization invitations are one-time, email-bound capabilities. Only a
-- SHA-256 token hash is persisted; the opaque token is delivered once through
-- the configured mailer and is never returned by the API.
CREATE TABLE organization_invitations (
  id UUID PRIMARY KEY,
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  email TEXT NOT NULL,
  role TEXT NOT NULL,
  token_hash BYTEA NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  invited_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  accepted_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT organization_invitations_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT organization_invitations_email_normalized CHECK (email = lower(email) AND email = btrim(email)),
  CONSTRAINT organization_invitations_email_length CHECK (char_length(email) BETWEEN 3 AND 320),
  CONSTRAINT organization_invitations_role_valid CHECK (role IN ('admin', 'developer', 'viewer', 'billing')),
  CONSTRAINT organization_invitations_hash_length CHECK (octet_length(token_hash) = 32),
  CONSTRAINT organization_invitations_expiry_valid CHECK (expires_at > created_at),
  CONSTRAINT organization_invitations_accepted_valid CHECK (accepted_at IS NULL OR accepted_at >= created_at),
  CONSTRAINT organization_invitations_revoked_valid CHECK (revoked_at IS NULL OR revoked_at >= created_at),
  CONSTRAINT organization_invitations_terminal_exclusive CHECK (accepted_at IS NULL OR revoked_at IS NULL)
);

CREATE UNIQUE INDEX organization_invitations_token_hash_unique ON organization_invitations (token_hash);
CREATE UNIQUE INDEX organization_invitations_active_email_unique
  ON organization_invitations (organization_id, email)
  WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX organization_invitations_organization_created_idx
  ON organization_invitations (organization_id, id DESC);
CREATE INDEX organization_invitations_inviter_idx
  ON organization_invitations (invited_by_account_id, created_at DESC);
