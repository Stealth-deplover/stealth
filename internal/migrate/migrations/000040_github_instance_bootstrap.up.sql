-- Existing accounts may not have an email/password pair when their identity
-- is provided by GitHub. Existing password accounts remain unchanged.
ALTER TABLE accounts ALTER COLUMN email DROP NOT NULL;
ALTER TABLE accounts ALTER COLUMN password_hash DROP NOT NULL;

CREATE TABLE account_identities (
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  provider_user_id TEXT NOT NULL,
  provider_login TEXT NOT NULL,
  provider_email TEXT,
  display_name TEXT,
  avatar_url TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (account_id, provider),
  CONSTRAINT account_identities_provider_valid CHECK (provider IN ('github')),
  CONSTRAINT account_identities_provider_user_id_length CHECK (char_length(provider_user_id) BETWEEN 1 AND 80),
  CONSTRAINT account_identities_provider_login_length CHECK (char_length(provider_login) BETWEEN 1 AND 120),
  CONSTRAINT account_identities_provider_email_length CHECK (provider_email IS NULL OR char_length(provider_email) BETWEEN 3 AND 320),
  CONSTRAINT account_identities_display_name_length CHECK (display_name IS NULL OR char_length(display_name) BETWEEN 1 AND 240),
  CONSTRAINT account_identities_avatar_url_length CHECK (avatar_url IS NULL OR char_length(avatar_url) BETWEEN 1 AND 2048)
);
CREATE UNIQUE INDEX account_identities_provider_user_unique
  ON account_identities (provider, provider_user_id);

ALTER TABLE bootstrap_sessions
  ADD COLUMN github_device_code_ciphertext BYTEA,
  ADD COLUMN github_user_code TEXT,
  ADD COLUMN github_verification_uri TEXT,
  ADD COLUMN github_expires_at TIMESTAMPTZ,
  ADD COLUMN github_interval_seconds INTEGER,
  ADD COLUMN github_next_poll_at TIMESTAMPTZ,
  ADD COLUMN github_status TEXT NOT NULL DEFAULT 'none';

ALTER TABLE bootstrap_sessions
  ADD CONSTRAINT bootstrap_sessions_github_status_valid
    CHECK (github_status IN ('none', 'pending', 'denied', 'expired', 'failed')),
  ADD CONSTRAINT bootstrap_sessions_github_interval_valid
    CHECK (github_interval_seconds IS NULL OR github_interval_seconds BETWEEN 1 AND 300),
  ADD CONSTRAINT bootstrap_sessions_github_device_state_consistent
    CHECK (
      github_status = 'none'
      OR (
        github_device_code_ciphertext IS NOT NULL
        AND github_user_code IS NOT NULL
        AND github_verification_uri IS NOT NULL
        AND github_expires_at IS NOT NULL
        AND github_interval_seconds IS NOT NULL
        AND github_next_poll_at IS NOT NULL
      )
    );
CREATE INDEX bootstrap_sessions_github_pending_idx
  ON bootstrap_sessions (github_next_poll_at)
  WHERE github_status = 'pending' AND used_at IS NULL AND invalidated_at IS NULL;

-- Version 1 of the onboarding migration assigned the oldest account as an
-- owner. If that migration was applied before this correction, remove only
-- that explicitly marked compatibility assignment. An owner created through
-- the old local setup endpoint has a different reason and is preserved.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM instance_bootstrap WHERE id = TRUE AND sealed_reason = 'legacy_first_account'
  ) THEN
    DELETE FROM instance_roles
    WHERE role = 'instance_owner'
      AND account_id = (
        SELECT account_id FROM instance_roles WHERE role = 'instance_owner' LIMIT 1
      );
    UPDATE instance_bootstrap
    SET sealed_reason = 'legacy_installation'
    WHERE id = TRUE;
  END IF;
END $$;
