-- Runtime container DNS names are route identities as well as cleanup
-- targets. Rotating them changes the name Docker publishes on the private
-- bridge, so a stale Traefik snapshot cannot resolve a restarted process.
ALTER TABLE app_runtime_state
  ADD COLUMN route_identity UUID NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN health_route_identity UUID;

UPDATE app_runtime_state
SET container_name='st-'||replace(app_id::text,'-','')||'-'||substring(replace(route_identity::text,'-','') FROM 1 FOR 24),
    health_status='pending',
    health_generation=NULL,
    health_deployment_id=NULL,
    health_container_id=NULL,
    health_route_identity=NULL,
    health_failure_count=0,
    health_checked_at=NULL,
    next_health_check_at=NULL,
    container_address=NULL,
    last_inspected_at=NULL,
    next_inspection_at=now(),
    updated_at=now();

ALTER TABLE app_runtime_state
  ALTER COLUMN container_name SET NOT NULL,
  ADD CONSTRAINT app_runtime_state_route_identity_valid
    CHECK (route_identity <> '00000000-0000-0000-0000-000000000000'),
  ADD CONSTRAINT app_runtime_state_route_target_matches_identity
    CHECK (container_name='st-'||replace(app_id::text,'-','')||'-'||substring(replace(route_identity::text,'-','') FROM 1 FOR 24)),
  ADD CONSTRAINT app_runtime_state_health_route_identity_consistent
    CHECK (
      health_route_identity IS NULL OR health_route_identity=route_identity
    );

CREATE UNIQUE INDEX app_runtime_state_route_identity_idx
  ON app_runtime_state (route_identity);

CREATE UNIQUE INDEX app_runtime_state_container_name_idx
  ON app_runtime_state (container_name);
