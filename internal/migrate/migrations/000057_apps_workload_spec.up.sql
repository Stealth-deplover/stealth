-- Project Apps are durable desired-state resources. They do not create
-- containers, images, or routes; trusted runtime work is a later capability.

CREATE OR REPLACE FUNCTION stealth_api_key_scopes_canonical(input_values text[]) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  value text;
  previous text := '';
BEGIN
  IF input_values IS NULL OR cardinality(input_values) < 1 OR cardinality(input_values) > 17 OR array_position(input_values, NULL) IS NOT NULL THEN
    RETURN false;
  END IF;
  FOREACH value IN ARRAY input_values LOOP
    IF previous <> '' AND previous >= value THEN
      RETURN false;
    END IF;
    previous := value;
  END LOOP;
  RETURN true;
END;
$$;

ALTER TABLE project_api_keys
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_supported,
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_nonempty,
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_canonical;

ALTER TABLE project_api_keys
  ADD CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY[
    'users.read','users.write',
    'databases.read','databases.write',
    'storage.read','storage.write',
    'functions.read','functions.write',
    'sites.read','sites.write',
    'webhooks.read','webhooks.write',
    'realtime.read',
    'messaging.read','messaging.write',
    'apps.read','apps.write'
  ]::text[]),
  ADD CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 17),
  ADD CONSTRAINT project_api_keys_scopes_canonical CHECK (stealth_api_key_scopes_canonical(scopes));

-- This label primary key is the cross-resource uniqueness boundary shared by
-- static Sites and persistent Apps. Resource creation claims before insert in
-- one transaction and retries deterministic candidates on a conflict.
CREATE TABLE platform_hostname_claims (
  label TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL,
  resource_id UUID NOT NULL,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT platform_hostname_claims_type_check CHECK (resource_type IN ('site','app')),
  CONSTRAINT platform_hostname_claims_label_valid CHECK (
    char_length(label) BETWEEN 1 AND 63 AND
    label = btrim(label) AND
    label = lower(label) AND
    label ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'
  ),
  CONSTRAINT platform_hostname_claims_resource_unique UNIQUE (resource_type, resource_id)
);

CREATE INDEX platform_hostname_claims_project_idx
  ON platform_hostname_claims (project_id, resource_type, resource_id);

-- The existing Site labels are already globally unique and stable. Copy them
-- without changing their values; an unexpected collision aborts this
-- migration instead of silently dropping a hostname claim.
INSERT INTO platform_hostname_claims (label, resource_type, resource_id, project_id, created_at)
SELECT platform_label, 'site', id, project_id, created_at
FROM project_sites
ORDER BY platform_label, id;

CREATE TABLE project_apps (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  platform_label TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  workload_spec JSONB NOT NULL,
  workload_spec_sha256 TEXT NOT NULL,
  desired_generation BIGINT NOT NULL DEFAULT 1,
  observed_generation BIGINT NOT NULL DEFAULT 0,
  runtime_status TEXT NOT NULL DEFAULT 'not_deployed',
  runtime_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_apps_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_apps_name_valid CHECK (
    char_length(name) BETWEEN 2 AND 63 AND
    name = btrim(name) AND
    name = lower(name) AND
    name ~ '^[a-z0-9][a-z0-9-]{1,62}$'
  ),
  CONSTRAINT project_apps_platform_label_valid CHECK (
    char_length(platform_label) BETWEEN 1 AND 63 AND
    platform_label = btrim(platform_label) AND
    platform_label = lower(platform_label) AND
    platform_label ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'
  ),
  CONSTRAINT project_apps_workload_spec_object CHECK (jsonb_typeof(workload_spec) = 'object'),
  CONSTRAINT project_apps_workload_spec_sha256_valid CHECK (workload_spec_sha256 ~ '^[0-9a-f]{64}$'),
  CONSTRAINT project_apps_desired_generation_valid CHECK (desired_generation >= 1),
  CONSTRAINT project_apps_observed_generation_valid CHECK (
    observed_generation >= 0 AND observed_generation <= desired_generation
  ),
  CONSTRAINT project_apps_runtime_status_valid CHECK (
    runtime_status IN ('not_deployed','pending','running','degraded','stopped','failed')
  ),
  CONSTRAINT project_apps_runtime_error_valid CHECK (
    runtime_error IS NULL OR (char_length(runtime_error) <= 512 AND runtime_error !~ '[[:cntrl:]]')
  ),
  CONSTRAINT project_apps_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_apps_project_id_name_unique UNIQUE (project_id, name)
);

CREATE INDEX project_apps_project_enabled_idx
  ON project_apps (project_id, enabled, id);

ALTER TABLE project_service_layouts
  DROP CONSTRAINT project_service_layouts_type_check,
  ADD CONSTRAINT project_service_layouts_type_check
    CHECK (resource_type IN ('function','site','app','database','storage'));
