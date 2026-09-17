-- Physical artifacts outlive their metadata deletion until a trusted worker
-- confirms cleanup. The queue is written in the same transaction as the
-- metadata mutation so a process crash cannot lose the cleanup request.
CREATE TABLE artifact_cleanup_jobs (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL,
  store_kind TEXT NOT NULL,
  operation TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  leased_at TIMESTAMPTZ,
  worker_id TEXT,
  last_error TEXT,
  failed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT artifact_cleanup_jobs_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT artifact_cleanup_jobs_project_uuidv7 CHECK (substring(project_id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT artifact_cleanup_jobs_store_valid CHECK (store_kind IN ('storage','functions','site_archives','sites')),
  CONSTRAINT artifact_cleanup_jobs_operation_valid CHECK (operation IN ('relative','project')),
  CONSTRAINT artifact_cleanup_jobs_status_valid CHECK (status IN ('pending','failed')),
  CONSTRAINT artifact_cleanup_jobs_attempts_valid CHECK (attempts BETWEEN 0 AND 2147483647),
  CONSTRAINT artifact_cleanup_jobs_error_valid CHECK (last_error IS NULL OR char_length(last_error) <= 4000),
  CONSTRAINT artifact_cleanup_jobs_worker_valid CHECK (worker_id IS NULL OR (char_length(worker_id) BETWEEN 1 AND 128 AND worker_id !~ '[[:cntrl:]]')),
  CONSTRAINT artifact_cleanup_jobs_path_valid CHECK (
    (operation = 'relative' AND relative_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')
    OR (operation = 'project' AND relative_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')
  )
);

CREATE UNIQUE INDEX artifact_cleanup_jobs_locator_unique
  ON artifact_cleanup_jobs (store_kind, operation, relative_path);

CREATE INDEX artifact_cleanup_jobs_ready_idx
  ON artifact_cleanup_jobs (available_at, id)
  WHERE status = 'pending';

CREATE INDEX artifact_cleanup_jobs_lease_idx
  ON artifact_cleanup_jobs (leased_at)
  WHERE status = 'pending' AND leased_at IS NOT NULL;
