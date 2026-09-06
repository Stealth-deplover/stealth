-- A zero-byte index.html is still a valid static publication. Keep the
-- compressed upload non-empty while allowing expanded artifact size 0.
ALTER TABLE site_deployments
  DROP CONSTRAINT IF EXISTS site_deployments_size_valid;

ALTER TABLE site_deployments
  ADD CONSTRAINT site_deployments_size_valid CHECK (size_bytes >= 0 AND archive_size_bytes > 0);
