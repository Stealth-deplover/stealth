-- Function deployments keep the uploaded source immutable and store a
-- second, worker-produced archive for runtime invocations. The build path is
-- private metadata; it is never selected by an HTTP projection.
ALTER TABLE function_deployments
  ADD COLUMN build_path TEXT,
  ADD COLUMN build_size_bytes BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN build_checksum_sha256 TEXT,
  ADD COLUMN build_worker_id TEXT,
  ADD COLUMN runtime_snapshot TEXT,
  ADD COLUMN entrypoint_snapshot TEXT,
  ADD COLUMN commands_snapshot TEXT NOT NULL DEFAULT '',
  ADD COLUMN timeout_seconds_snapshot INTEGER NOT NULL DEFAULT 15,
  ADD COLUMN logging_snapshot BOOLEAN NOT NULL DEFAULT true;

UPDATE function_deployments d
SET runtime_snapshot=f.runtime,
    entrypoint_snapshot=f.entrypoint,
    commands_snapshot=f.commands,
    timeout_seconds_snapshot=f.timeout_seconds,
    logging_snapshot=f.logging
FROM project_functions f
WHERE f.id=d.function_id AND f.project_id=d.project_id;

ALTER TABLE function_deployments
  ALTER COLUMN runtime_snapshot SET NOT NULL,
  ALTER COLUMN entrypoint_snapshot SET NOT NULL;

ALTER TABLE function_deployments
  ADD CONSTRAINT function_deployments_build_path_valid CHECK (
    build_path IS NULL OR build_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
  ),
  ADD CONSTRAINT function_deployments_build_size_valid CHECK (build_size_bytes >= 0),
  ADD CONSTRAINT function_deployments_build_checksum_valid CHECK (
    build_checksum_sha256 IS NULL OR build_checksum_sha256 ~ '^[0-9a-f]{64}$'
  ),
  ADD CONSTRAINT function_deployments_build_worker_id_valid CHECK (
    build_worker_id IS NULL OR build_worker_id ~ '^[A-Za-z0-9._-]{1,128}$'
  ),
  ADD CONSTRAINT function_deployments_runtime_snapshot_valid CHECK (
    runtime_snapshot IN ('node-22','python-3.13','go-1.24')
  ),
  ADD CONSTRAINT function_deployments_entrypoint_snapshot_valid CHECK (
    char_length(entrypoint_snapshot) BETWEEN 1 AND 255 AND
    entrypoint_snapshot = btrim(entrypoint_snapshot) AND
    left(entrypoint_snapshot, 1) <> '/' AND
    position(chr(92) IN entrypoint_snapshot) = 0 AND
    position(chr(13) IN entrypoint_snapshot) = 0 AND position(chr(10) IN entrypoint_snapshot) = 0 AND
    entrypoint_snapshot !~ '(^|/)\.\.?(/|$)'
  ),
  ADD CONSTRAINT function_deployments_commands_snapshot_valid CHECK (char_length(commands_snapshot) <= 4000),
  ADD CONSTRAINT function_deployments_timeout_snapshot_valid CHECK (timeout_seconds_snapshot BETWEEN 1 AND 900);
