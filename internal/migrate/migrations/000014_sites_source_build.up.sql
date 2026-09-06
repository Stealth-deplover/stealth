-- Sites can optionally be deployed from an uploaded source archive. The API
-- stores that archive opaquely and a trusted worker produces the immutable
-- public directory. Existing pre-built deployments are marked as already
-- succeeded so this migration is backwards compatible.

ALTER TABLE project_sites
  ADD COLUMN artifact_reserved_bytes BIGINT NOT NULL DEFAULT 0;

ALTER TABLE project_sites
  ADD CONSTRAINT project_sites_reserved_valid CHECK (
    artifact_reserved_bytes >= 0 AND
    artifact_used_bytes + artifact_reserved_bytes <= artifact_quota_bytes
  );

ALTER TABLE site_deployments
  ADD COLUMN source_path TEXT,
  ADD COLUMN build_runtime TEXT NOT NULL DEFAULT 'node-22',
  ADD COLUMN build_command TEXT NOT NULL DEFAULT '',
  ADD COLUMN output_directory TEXT NOT NULL DEFAULT '.',
  ADD COLUMN reserved_bytes BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN build_status TEXT NOT NULL DEFAULT 'succeeded',
  ADD COLUMN build_started_at TIMESTAMPTZ,
  ADD COLUMN built_at TIMESTAMPTZ,
  ADD COLUMN build_worker_id TEXT,
  ADD COLUMN activate_requested BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE site_deployments
  ADD CONSTRAINT site_deployments_source_path_valid CHECK (
    source_path IS NULL OR source_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
  ),
  ADD CONSTRAINT site_deployments_build_runtime_valid CHECK (
    build_runtime IN ('node-22','python-3.13','go-1.24')
  ),
  ADD CONSTRAINT site_deployments_build_command_valid CHECK (
    char_length(build_command) <= 4000
  ),
  ADD CONSTRAINT site_deployments_output_directory_valid CHECK (
    char_length(output_directory) BETWEEN 1 AND 255 AND
    output_directory = btrim(output_directory) AND
    output_directory ~ '^(\.|[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*)$' AND
    (output_directory = '.' OR output_directory !~ '(^|/)\.\.?(/|$)')
  ),
  ADD CONSTRAINT site_deployments_reserved_valid CHECK (reserved_bytes >= 0),
  ADD CONSTRAINT site_deployments_build_status_valid CHECK (
    build_status IN ('queued','running','deferred','succeeded','failed')
  ),
  ADD CONSTRAINT site_deployments_build_worker_id_valid CHECK (
    build_worker_id IS NULL OR build_worker_id ~ '^[A-Za-z0-9._-]{1,128}$'
  );

CREATE INDEX site_deployments_build_claim_idx
  ON site_deployments (build_status, queued_at, id)
  WHERE build_status IN ('queued','deferred');
