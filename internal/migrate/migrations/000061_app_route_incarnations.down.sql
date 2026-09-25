DROP INDEX IF EXISTS app_runtime_state_route_identity_idx;
DROP INDEX IF EXISTS app_runtime_state_container_name_idx;

ALTER TABLE app_runtime_state
  DROP CONSTRAINT IF EXISTS app_runtime_state_health_route_identity_consistent,
  DROP CONSTRAINT IF EXISTS app_runtime_state_route_target_matches_identity,
  DROP CONSTRAINT IF EXISTS app_runtime_state_route_identity_valid,
  DROP COLUMN IF EXISTS health_route_identity,
  DROP COLUMN IF EXISTS route_identity;
