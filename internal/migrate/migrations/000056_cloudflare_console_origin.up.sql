ALTER TABLE cloudflare_connections
  ADD COLUMN console_origin_desired TEXT NOT NULL DEFAULT 'proxy',
  ADD COLUMN console_origin_observed TEXT NOT NULL DEFAULT 'unknown',
  ADD COLUMN console_origin_status TEXT NOT NULL DEFAULT 'pending',
  ADD COLUMN console_origin_last_error TEXT,
  ADD COLUMN console_origin_updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN console_public_verified_at TIMESTAMPTZ,
  ADD COLUMN console_public_verified_origin TEXT;

ALTER TABLE cloudflare_connections
  ADD CONSTRAINT cloudflare_connections_console_origin_desired_valid
    CHECK (console_origin_desired IN ('proxy','traefik')),
  ADD CONSTRAINT cloudflare_connections_console_origin_observed_valid
    CHECK (console_origin_observed IN ('proxy','traefik','unknown')),
  ADD CONSTRAINT cloudflare_connections_console_origin_status_valid
    CHECK (console_origin_status IN ('pending','ready','error')),
  ADD CONSTRAINT cloudflare_connections_console_origin_error_length
    CHECK (console_origin_last_error IS NULL OR char_length(console_origin_last_error) <= 512),
  ADD CONSTRAINT cloudflare_connections_console_public_verified_origin_valid
    CHECK (console_public_verified_origin IS NULL OR console_public_verified_origin IN ('proxy','traefik'));

-- The migration records the established production origin as desired state.
-- The worker will inspect and converge the existing tunnel after upgrade.
-- No provider API call is made by this migration.
UPDATE cloudflare_connections
SET console_origin_desired = 'proxy',
    console_origin_observed = 'unknown',
    console_origin_status = 'pending',
    console_public_verified_at = NULL,
    console_public_verified_origin = NULL
WHERE id = TRUE;
