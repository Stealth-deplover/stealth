-- App build artifacts are private, separately namespaced durable objects.
-- Existing Apps receive a migration-safe quota without changing desired state.
ALTER TABLE project_apps
  ADD COLUMN artifact_quota_bytes BIGINT NOT NULL DEFAULT 5368709120,
  ADD COLUMN artifact_used_bytes BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN artifact_reserved_bytes BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN next_deployment_version BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN desired_deployment_id UUID,
  ADD CONSTRAINT project_apps_artifact_quota_valid CHECK (
    artifact_quota_bytes > 0 AND artifact_used_bytes >= 0 AND artifact_reserved_bytes >= 0 AND
    artifact_used_bytes <= artifact_quota_bytes - artifact_reserved_bytes AND next_deployment_version >= 1
  );

CREATE TABLE app_deployments (
  id UUID PRIMARY KEY,
  app_id UUID NOT NULL,
  project_id UUID NOT NULL,
  version BIGINT NOT NULL,
  source TEXT NOT NULL DEFAULT 'upload',
  source_name TEXT NOT NULL,
  source_size_bytes BIGINT NOT NULL,
  source_checksum_sha256 TEXT NOT NULL,
  source_path TEXT NOT NULL,
  dockerfile_path TEXT NOT NULL,
  context_directory TEXT NOT NULL,
  target TEXT,
  platform TEXT NOT NULL,
  workload_spec JSONB NOT NULL,
  workload_spec_sha256 TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  build_status TEXT NOT NULL DEFAULT 'queued',
  build_worker_id TEXT,
  error_message TEXT,
  image_digest TEXT,
  image_archive_sha256 TEXT,
  image_size_bytes BIGINT,
  image_path TEXT,
  select_requested BOOLEAN NOT NULL DEFAULT false,
  selection_base_deployment_id UUID,
  reserved_image_bytes BIGINT NOT NULL DEFAULT 0,
  reserved_image_path TEXT,
  next_log_sequence BIGINT NOT NULL DEFAULT 1,
  created_by_account_id UUID,
  queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  build_started_at TIMESTAMPTZ,
  built_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT app_deployments_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT app_deployments_app_project_fk FOREIGN KEY (project_id, app_id)
    REFERENCES project_apps(project_id, id) ON DELETE CASCADE,
  CONSTRAINT app_deployments_version_valid CHECK (version >= 1),
  CONSTRAINT app_deployments_source_valid CHECK (source = 'upload'),
  CONSTRAINT app_deployments_source_name_valid CHECK (
    char_length(source_name) BETWEEN 1 AND 255 AND source_name = btrim(source_name) AND
    source_name !~ '[[:cntrl:]/]' AND position(chr(92) in source_name)=0
  ),
  CONSTRAINT app_deployments_source_size_valid CHECK (source_size_bytes > 0),
  CONSTRAINT app_deployments_source_checksum_valid CHECK (source_checksum_sha256 ~ '^[0-9a-f]{64}$'),
  CONSTRAINT app_deployments_source_path_valid CHECK (
    source_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
  ),
  CONSTRAINT app_deployments_dockerfile_valid CHECK (char_length(dockerfile_path) BETWEEN 1 AND 512 AND dockerfile_path !~ '[[:cntrl:]]' AND position(chr(92) in dockerfile_path)=0),
  CONSTRAINT app_deployments_context_valid CHECK (char_length(context_directory) BETWEEN 1 AND 512 AND context_directory !~ '[[:cntrl:]]' AND position(chr(92) in context_directory)=0),
  CONSTRAINT app_deployments_target_valid CHECK (target IS NULL OR target ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
  CONSTRAINT app_deployments_platform_valid CHECK (platform IN ('linux/amd64','linux/arm64')),
  CONSTRAINT app_deployments_workload_object CHECK (jsonb_typeof(workload_spec) = 'object'),
  CONSTRAINT app_deployments_workload_sha256_valid CHECK (workload_spec_sha256 ~ '^[0-9a-f]{64}$'),
  CONSTRAINT app_deployments_status_valid CHECK (status IN ('queued','building','ready','failed')),
  CONSTRAINT app_deployments_build_status_valid CHECK (build_status IN ('queued','running','deferred','succeeded','failed')),
  CONSTRAINT app_deployments_status_consistent CHECK (
    (status='queued' AND build_status IN ('queued','deferred')) OR
    (status='building' AND build_status='running') OR
    (status='ready' AND build_status='succeeded') OR
    (status='failed' AND build_status='failed')
  ),
  CONSTRAINT app_deployments_worker_valid CHECK (build_worker_id IS NULL OR build_worker_id ~ '^[A-Za-z0-9._-]{1,128}$'),
  CONSTRAINT app_deployments_error_valid CHECK (error_message IS NULL OR (char_length(error_message) <= 512 AND error_message !~ '[[:cntrl:]]')),
  CONSTRAINT app_deployments_digest_valid CHECK (image_digest IS NULL OR image_digest ~ '^sha256:[0-9a-f]{64}$'),
  CONSTRAINT app_deployments_image_archive_checksum_valid CHECK (image_archive_sha256 IS NULL OR image_archive_sha256 ~ '^[0-9a-f]{64}$'),
  CONSTRAINT app_deployments_image_size_valid CHECK (image_size_bytes IS NULL OR image_size_bytes > 0),
  CONSTRAINT app_deployments_image_path_valid CHECK (
    image_path IS NULL OR image_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
  ),
  CONSTRAINT app_deployments_image_complete CHECK (
    (build_status='succeeded' AND image_digest IS NOT NULL AND image_archive_sha256 IS NOT NULL AND image_size_bytes IS NOT NULL AND image_path IS NOT NULL) OR
    (build_status<>'succeeded' AND image_digest IS NULL AND image_archive_sha256 IS NULL AND image_size_bytes IS NULL AND image_path IS NULL)
  ),
  CONSTRAINT app_deployments_reserved_image_valid CHECK (
    (reserved_image_bytes=0 AND reserved_image_path IS NULL) OR
    (reserved_image_bytes>0 AND reserved_image_path IS NOT NULL AND reserved_image_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')
  ),
  CONSTRAINT app_deployments_next_log_sequence_valid CHECK (next_log_sequence >= 1),
  CONSTRAINT app_deployments_version_unique UNIQUE (app_id, version),
  CONSTRAINT app_deployments_id_app_project_unique UNIQUE (id, app_id, project_id)
);

CREATE INDEX app_deployments_project_app_page_idx ON app_deployments(project_id, app_id, version DESC);
CREATE INDEX app_deployments_build_queue_idx ON app_deployments(queued_at, id)
  WHERE status='queued' AND build_status IN ('queued','deferred');
CREATE INDEX project_apps_desired_deployment_idx ON project_apps(desired_deployment_id)
  WHERE desired_deployment_id IS NOT NULL;

ALTER TABLE project_apps
  ADD CONSTRAINT project_apps_desired_deployment_fk
  FOREIGN KEY (desired_deployment_id, id, project_id)
  REFERENCES app_deployments(id, app_id, project_id)
  DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION prevent_app_deployment_input_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.id, NEW.app_id, NEW.project_id, NEW.version, NEW.source, NEW.source_name,
      NEW.source_size_bytes, NEW.source_checksum_sha256, NEW.source_path,
      NEW.dockerfile_path, NEW.context_directory, NEW.target, NEW.platform,
      NEW.workload_spec, NEW.workload_spec_sha256, NEW.select_requested, NEW.selection_base_deployment_id,
      NEW.created_by_account_id)
     IS DISTINCT FROM
     (OLD.id, OLD.app_id, OLD.project_id, OLD.version, OLD.source, OLD.source_name,
      OLD.source_size_bytes, OLD.source_checksum_sha256, OLD.source_path,
      OLD.dockerfile_path, OLD.context_directory, OLD.target, OLD.platform,
      OLD.workload_spec, OLD.workload_spec_sha256, OLD.select_requested, OLD.selection_base_deployment_id,
      OLD.created_by_account_id) THEN
    RAISE EXCEPTION 'AppDeployment build inputs are immutable';
  END IF;
  IF OLD.image_digest IS NOT NULL AND
     (NEW.image_digest, NEW.image_archive_sha256, NEW.image_size_bytes, NEW.image_path)
     IS DISTINCT FROM
     (OLD.image_digest, OLD.image_archive_sha256, OLD.image_size_bytes, OLD.image_path) THEN
    RAISE EXCEPTION 'AppDeployment build output is immutable';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER app_deployments_immutable_inputs
  BEFORE UPDATE ON app_deployments
  FOR EACH ROW EXECUTE FUNCTION prevent_app_deployment_input_mutation();

CREATE TABLE app_build_logs (
  id UUID PRIMARY KEY,
  deployment_id UUID NOT NULL,
  app_id UUID NOT NULL,
  project_id UUID NOT NULL,
  sequence BIGINT NOT NULL,
  level TEXT NOT NULL,
  message TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT app_build_logs_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT app_build_logs_deployment_fk FOREIGN KEY (deployment_id, app_id, project_id)
    REFERENCES app_deployments(id, app_id, project_id) ON DELETE CASCADE,
  CONSTRAINT app_build_logs_sequence_valid CHECK (sequence >= 1),
  CONSTRAINT app_build_logs_level_valid CHECK (level IN ('info','warn','error')),
  CONSTRAINT app_build_logs_message_valid CHECK (char_length(message) BETWEEN 1 AND 4096 AND message !~ '[[:cntrl:]]'),
  CONSTRAINT app_build_logs_deployment_sequence_unique UNIQUE (deployment_id, sequence)
);

CREATE INDEX app_build_logs_page_idx ON app_build_logs(deployment_id, sequence);

ALTER TABLE artifact_cleanup_jobs
  DROP CONSTRAINT artifact_cleanup_jobs_store_valid,
  ADD CONSTRAINT artifact_cleanup_jobs_store_valid CHECK (store_kind IN ('storage','functions','site_archives','sites','app_sources','app_images'));

ALTER TABLE artifact_cleanup_jobs
  ADD COLUMN quota_app_id UUID,
  ADD COLUMN quota_reserved_bytes BIGINT NOT NULL DEFAULT 0,
  ADD CONSTRAINT artifact_cleanup_jobs_app_quota_reservation_valid CHECK (
    (quota_app_id IS NULL AND quota_reserved_bytes=0) OR
    (quota_app_id IS NOT NULL AND quota_reserved_bytes > 0 AND store_kind IN ('app_sources','app_images') AND operation='relative')
  ),
  ADD CONSTRAINT artifact_cleanup_jobs_app_quota_id_valid CHECK (
    quota_app_id IS NULL OR substring(quota_app_id::text FROM 15 FOR 1) = '7'
  );
