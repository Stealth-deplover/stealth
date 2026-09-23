-- Rolling back the schema while private source or OCI artifacts still exist
-- would discard their only durable cleanup locators. Drain/delete deployments
-- and their App artifact cleanup jobs before applying this down migration.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM app_deployments) OR
     EXISTS (SELECT 1 FROM artifact_cleanup_jobs WHERE store_kind IN ('app_sources','app_images')) THEN
    RAISE EXCEPTION 'cannot roll back App deployments while App artifact metadata or cleanup jobs remain';
  END IF;
END;
$$;

-- The desired pointer is control-plane metadata from this migration only.
ALTER TABLE project_apps DROP CONSTRAINT IF EXISTS project_apps_desired_deployment_fk;
UPDATE project_apps SET desired_deployment_id=NULL;

DROP TABLE IF EXISTS app_build_logs;
DROP TRIGGER IF EXISTS app_deployments_immutable_inputs ON app_deployments;
DROP FUNCTION IF EXISTS prevent_app_deployment_input_mutation();
DROP TABLE IF EXISTS app_deployments;

ALTER TABLE project_apps
  DROP CONSTRAINT IF EXISTS project_apps_artifact_quota_valid,
  DROP COLUMN IF EXISTS desired_deployment_id,
  DROP COLUMN IF EXISTS artifact_reserved_bytes,
  DROP COLUMN IF EXISTS artifact_used_bytes,
  DROP COLUMN IF EXISTS next_deployment_version,
  DROP COLUMN IF EXISTS artifact_quota_bytes;

ALTER TABLE artifact_cleanup_jobs
  DROP CONSTRAINT IF EXISTS artifact_cleanup_jobs_app_quota_reservation_valid,
  DROP CONSTRAINT IF EXISTS artifact_cleanup_jobs_app_quota_id_valid,
  DROP COLUMN IF EXISTS quota_app_id,
  DROP COLUMN IF EXISTS quota_reserved_bytes,
  DROP CONSTRAINT IF EXISTS artifact_cleanup_jobs_store_valid,
  ADD CONSTRAINT artifact_cleanup_jobs_store_valid CHECK (store_kind IN ('storage','functions','site_archives','sites'));
