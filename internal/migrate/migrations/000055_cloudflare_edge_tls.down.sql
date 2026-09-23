ALTER TABLE cloudflare_connections
  DROP CONSTRAINT IF EXISTS cloudflare_connections_edge_tls_error_length,
  DROP CONSTRAINT IF EXISTS cloudflare_connections_edge_tls_status_valid,
  DROP COLUMN IF EXISTS edge_tls_error,
  DROP COLUMN IF EXISTS edge_tls_status;
