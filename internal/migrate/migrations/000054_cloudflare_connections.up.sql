-- Cloudflare is an instance-level provider connection. Keep its credential
-- encrypted by the application and keep workload_base_domain authoritative
-- in instance_domain_settings.
CREATE TABLE cloudflare_connections (
  id BOOLEAN PRIMARY KEY DEFAULT TRUE,
  account_id TEXT,
  console_zone_id TEXT,
  console_hostname TEXT,
  tunnel_id TEXT,
  tunnel_name TEXT,
  console_record_id TEXT,
  api_token_ciphertext BYTEA,
  workload_zone_id TEXT,
  workload_zone_name TEXT,
  wildcard_hostname TEXT,
  wildcard_record_id TEXT,
  status TEXT NOT NULL DEFAULT 'unconfigured',
  last_reconciled_at TIMESTAMPTZ,
  last_error TEXT,
  configured_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT cloudflare_connections_singleton CHECK (id),
  CONSTRAINT cloudflare_connections_status_valid CHECK (status IN ('unconfigured','pending','ready','error')),
  CONSTRAINT cloudflare_connections_error_length CHECK (last_error IS NULL OR char_length(last_error) <= 512),
  CONSTRAINT cloudflare_connections_identity_complete CHECK (
    api_token_ciphertext IS NULL OR (
      account_id IS NOT NULL AND account_id <> '' AND
      console_zone_id IS NOT NULL AND console_zone_id <> '' AND
      console_hostname IS NOT NULL AND console_hostname <> '' AND
      tunnel_id IS NOT NULL AND tunnel_id <> '' AND
      tunnel_name IS NOT NULL AND tunnel_name <> '' AND
      console_record_id IS NOT NULL AND console_record_id <> '' AND
      configured_at IS NOT NULL
    )
  )
);

INSERT INTO cloudflare_connections (id, status)
VALUES (TRUE, 'unconfigured')
ON CONFLICT (id) DO NOTHING;

-- A domain change may leave a known, Stealth-managed record to remove after
-- the replacement route is live. Keep every pending deletion durable so a
-- later owner change or worker restart cannot lose its exact provider ID.
CREATE TABLE cloudflare_retiring_wildcard_dns (
  record_id TEXT PRIMARY KEY,
  zone_id TEXT NOT NULL,
  hostname TEXT NOT NULL,
  target TEXT NOT NULL,
  queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT cloudflare_retiring_wildcard_dns_identity_nonempty CHECK (
    zone_id <> '' AND hostname <> '' AND target <> ''
  )
);
