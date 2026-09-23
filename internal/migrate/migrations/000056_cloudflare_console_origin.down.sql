ALTER TABLE cloudflare_connections
  DROP CONSTRAINT IF EXISTS cloudflare_connections_console_public_verified_origin_valid,
  DROP CONSTRAINT IF EXISTS cloudflare_connections_console_origin_error_length,
  DROP CONSTRAINT IF EXISTS cloudflare_connections_console_origin_status_valid,
  DROP CONSTRAINT IF EXISTS cloudflare_connections_console_origin_observed_valid,
  DROP CONSTRAINT IF EXISTS cloudflare_connections_console_origin_desired_valid,
  DROP COLUMN IF EXISTS console_public_verified_origin,
  DROP COLUMN IF EXISTS console_public_verified_at,
  DROP COLUMN IF EXISTS console_origin_updated_at,
  DROP COLUMN IF EXISTS console_origin_last_error,
  DROP COLUMN IF EXISTS console_origin_status,
  DROP COLUMN IF EXISTS console_origin_observed,
  DROP COLUMN IF EXISTS console_origin_desired;
