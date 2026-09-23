-- Edge certificate inventory is provider state; persist only the bounded
-- readiness decision needed by API status and restart behavior.
ALTER TABLE cloudflare_connections
  ADD COLUMN edge_tls_status TEXT NOT NULL DEFAULT 'not_applicable',
  ADD COLUMN edge_tls_error TEXT;

ALTER TABLE cloudflare_connections
  ADD CONSTRAINT cloudflare_connections_edge_tls_status_valid
    CHECK (edge_tls_status IN ('not_applicable','pending','ready','action_required','error')),
  ADD CONSTRAINT cloudflare_connections_edge_tls_error_length
    CHECK (edge_tls_error IS NULL OR char_length(edge_tls_error) <= 512);

UPDATE cloudflare_connections c
SET edge_tls_status = CASE
  WHEN d.workload_base_domain IS NULL OR c.api_token_ciphertext IS NULL THEN 'not_applicable'
  ELSE 'pending'
END
FROM instance_domain_settings d
WHERE c.id=TRUE AND d.id=TRUE;
