-- Persistent App runtime bookkeeping is private worker state. The App row
-- remains the sole source of desired state; Docker identifiers never enter
-- the public App projection.
CREATE TABLE app_runtime_state (
  app_id UUID PRIMARY KEY,
  project_id UUID NOT NULL,
  worker_id TEXT,
  lease_token UUID,
  lease_expires_at TIMESTAMPTZ,
  container_id TEXT,
  container_name TEXT,
  image_id TEXT,
  image_digest TEXT,
  runtime_tag TEXT,
  applied_deployment_id UUID,
  applied_workload_spec_sha256 TEXT,
  applied_generation BIGINT,
  stop_grace_period_seconds INTEGER NOT NULL DEFAULT 15,
  failure_count INTEGER NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ,
  last_failure_at TIMESTAMPTZ,
  last_inspected_at TIMESTAMPTZ,
  next_inspection_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_transition_at TIMESTAMPTZ,
  last_started_at TIMESTAMPTZ,
  last_stopped_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT app_runtime_state_app_project_fk
    FOREIGN KEY (project_id, app_id) REFERENCES project_apps(project_id, id) ON DELETE CASCADE,
  CONSTRAINT app_runtime_state_worker_valid
    CHECK (worker_id IS NULL OR worker_id ~ '^[A-Za-z0-9._-]{1,128}$'),
  CONSTRAINT app_runtime_state_lease_consistent CHECK (
    (worker_id IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL) OR
    (worker_id IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
  ),
  CONSTRAINT app_runtime_state_container_id_valid
    CHECK (container_id IS NULL OR container_id ~ '^[0-9a-f]{12,64}$'),
  CONSTRAINT app_runtime_state_container_name_valid
    CHECK (container_name IS NULL OR (char_length(container_name) BETWEEN 1 AND 255 AND container_name !~ '[[:cntrl:]]')),
  CONSTRAINT app_runtime_state_image_id_valid
    CHECK (image_id IS NULL OR image_id ~ '^sha256:[0-9a-f]{64}$'),
  CONSTRAINT app_runtime_state_image_digest_valid
    CHECK (image_digest IS NULL OR image_digest ~ '^sha256:[0-9a-f]{64}$'),
  CONSTRAINT app_runtime_state_runtime_tag_valid
    CHECK (runtime_tag IS NULL OR (char_length(runtime_tag) BETWEEN 1 AND 255 AND runtime_tag ~ '^[a-z0-9][a-z0-9._:/-]*$')),
  CONSTRAINT app_runtime_state_spec_digest_valid
    CHECK (applied_workload_spec_sha256 IS NULL OR applied_workload_spec_sha256 ~ '^[0-9a-f]{64}$'),
  CONSTRAINT app_runtime_state_generation_valid
    CHECK (applied_generation IS NULL OR applied_generation >= 0),
  CONSTRAINT app_runtime_state_grace_valid
    CHECK (stop_grace_period_seconds BETWEEN 1 AND 120),
  CONSTRAINT app_runtime_state_failure_count_valid CHECK (failure_count >= 0)
);

CREATE INDEX app_runtime_state_due_idx
  ON app_runtime_state (next_retry_at, next_inspection_at, app_id)
  WHERE lease_token IS NULL;

-- This queue deliberately has no FK to projects or Apps. It must outlive
-- control-plane deletion so the worker can remove the external Moby object.
CREATE TABLE app_runtime_cleanup_jobs (
  id UUID PRIMARY KEY,
  project_id UUID,
  app_id UUID NOT NULL,
  container_id TEXT,
  container_name TEXT NOT NULL,
  stop_grace_period_seconds INTEGER NOT NULL DEFAULT 15,
  status TEXT NOT NULL DEFAULT 'pending',
  worker_id TEXT,
  lease_token UUID,
  lease_expires_at TIMESTAMPTZ,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  CONSTRAINT app_runtime_cleanup_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT app_runtime_cleanup_ids_valid CHECK (project_id IS NULL OR project_id <> '00000000-0000-0000-0000-000000000000'),
  CONSTRAINT app_runtime_cleanup_container_id_valid
    CHECK (container_id IS NULL OR container_id ~ '^[0-9a-f]{12,64}$'),
  CONSTRAINT app_runtime_cleanup_container_name_valid
    CHECK (char_length(container_name) BETWEEN 1 AND 255 AND container_name !~ '[[:cntrl:]]'),
  CONSTRAINT app_runtime_cleanup_grace_valid CHECK (stop_grace_period_seconds BETWEEN 1 AND 120),
  CONSTRAINT app_runtime_cleanup_status_valid CHECK (status IN ('pending','leased','completed','failed')),
  CONSTRAINT app_runtime_cleanup_worker_valid
    CHECK (worker_id IS NULL OR worker_id ~ '^[A-Za-z0-9._-]{1,128}$'),
  CONSTRAINT app_runtime_cleanup_lease_consistent CHECK (
    (status <> 'leased' AND worker_id IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL) OR
    (status = 'leased' AND worker_id IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
  ),
  CONSTRAINT app_runtime_cleanup_attempts_valid CHECK (attempt_count >= 0),
  CONSTRAINT app_runtime_cleanup_error_valid
    CHECK (last_error IS NULL OR (char_length(last_error) <= 512 AND last_error !~ '[[:cntrl:]]')),
  CONSTRAINT app_runtime_cleanup_completion_consistent CHECK (
    (status = 'completed' AND completed_at IS NOT NULL) OR
    (status <> 'completed' AND completed_at IS NULL)
  )
);

CREATE INDEX app_runtime_cleanup_due_idx
  ON app_runtime_cleanup_jobs (next_attempt_at, created_at, id)
  WHERE status IN ('pending','leased');

CREATE UNIQUE INDEX app_runtime_cleanup_active_target_idx
  ON app_runtime_cleanup_jobs (app_id,COALESCE(container_id,''),container_name)
  WHERE status IN ('pending','leased');
