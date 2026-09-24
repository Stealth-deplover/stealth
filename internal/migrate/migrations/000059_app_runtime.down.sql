DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM app_runtime_cleanup_jobs WHERE status <> 'completed') OR
     EXISTS (SELECT 1 FROM app_runtime_state WHERE lease_token IS NOT NULL OR container_id IS NOT NULL) OR
     EXISTS (SELECT 1 FROM project_apps WHERE runtime_status = 'running') THEN
    RAISE EXCEPTION 'refusing App runtime rollback while cleanup, leases, or running containers may remain; stop and remove managed Apps first';
  END IF;
END;
$$;

DROP TABLE app_runtime_cleanup_jobs;
DROP TABLE app_runtime_state;
