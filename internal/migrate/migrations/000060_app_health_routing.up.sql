-- Application health is separate from process liveness. A health result is
-- tied to the exact selected generation, deployment, and managed container.
ALTER TABLE app_runtime_state
  ADD COLUMN container_address INET,
  ADD COLUMN health_status TEXT NOT NULL DEFAULT 'pending',
  ADD COLUMN health_generation BIGINT,
  ADD COLUMN health_deployment_id UUID,
  ADD COLUMN health_container_id TEXT,
  ADD COLUMN health_failure_count INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN health_checked_at TIMESTAMPTZ,
  ADD COLUMN next_health_check_at TIMESTAMPTZ;

ALTER TABLE app_runtime_state
  ADD CONSTRAINT app_runtime_state_container_address_valid
    CHECK (container_address IS NULL OR family(container_address)=4),
  ADD CONSTRAINT app_runtime_state_health_status_valid
    CHECK (health_status IN ('pending','healthy','unhealthy')),
  ADD CONSTRAINT app_runtime_state_health_generation_valid
    CHECK (health_generation IS NULL OR health_generation >= 1),
  ADD CONSTRAINT app_runtime_state_health_container_id_valid
    CHECK (health_container_id IS NULL OR health_container_id ~ '^[0-9a-f]{12,64}$'),
  ADD CONSTRAINT app_runtime_state_health_failures_valid
    CHECK (health_failure_count >= 0),
  ADD CONSTRAINT app_runtime_state_health_identity_consistent CHECK (
    (health_generation IS NULL AND health_deployment_id IS NULL AND health_container_id IS NULL) OR
    (health_generation IS NOT NULL AND health_deployment_id IS NOT NULL AND health_container_id IS NOT NULL)
  ),
  ADD CONSTRAINT app_runtime_state_health_result_consistent CHECK (
    health_status='pending' OR
    (health_generation IS NOT NULL AND health_deployment_id IS NOT NULL AND health_container_id IS NOT NULL AND health_checked_at IS NOT NULL)
  ),
  ADD CONSTRAINT app_runtime_state_healthy_route_identity CHECK (
    health_status<>'healthy' OR container_address IS NOT NULL
  ),
  ADD CONSTRAINT app_runtime_state_health_deployment_fk
    FOREIGN KEY (health_deployment_id,app_id,project_id)
    REFERENCES app_deployments(id,app_id,project_id);

CREATE INDEX app_runtime_state_health_due_idx
  ON app_runtime_state (next_health_check_at, app_id)
  WHERE health_status IN ('pending','healthy','unhealthy') AND lease_token IS NULL;
