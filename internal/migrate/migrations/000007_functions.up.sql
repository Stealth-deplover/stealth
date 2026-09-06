-- Functions keeps source bytes on a private local filesystem and stores only
-- opaque UUID-derived paths in PostgreSQL. No source archive is extracted or
-- executed by the API; trusted worker processes own that work.

CREATE OR REPLACE FUNCTION stealth_api_key_scopes_canonical(input_values text[]) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  value text;
  previous text := '';
BEGIN
  IF input_values IS NULL OR cardinality(input_values) < 1 OR cardinality(input_values) > 8 OR array_position(input_values, NULL) IS NOT NULL THEN
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
  DROP CONSTRAINT project_api_keys_scopes_nonempty,
  DROP CONSTRAINT project_api_keys_scopes_supported,
  DROP CONSTRAINT project_api_keys_scopes_canonical;

ALTER TABLE project_api_keys
  ADD CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 8),
  ADD CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY[
    'users.read','users.write',
    'databases.read','databases.write',
    'storage.read','storage.write',
    'functions.read','functions.write'
  ]::text[]),
  ADD CONSTRAINT project_api_keys_scopes_canonical CHECK (stealth_api_key_scopes_canonical(scopes));

CREATE TABLE project_functions (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  runtime TEXT NOT NULL DEFAULT 'node-22',
  entrypoint TEXT NOT NULL DEFAULT 'src/main.js',
  commands TEXT NOT NULL DEFAULT '',
  timeout_seconds INTEGER NOT NULL DEFAULT 15,
  enabled BOOLEAN NOT NULL DEFAULT true,
  logging BOOLEAN NOT NULL DEFAULT true,
  execute_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  description TEXT,
  status TEXT NOT NULL DEFAULT 'active',
  artifact_quota_bytes BIGINT NOT NULL DEFAULT 1073741824,
  artifact_used_bytes BIGINT NOT NULL DEFAULT 0,
  active_deployment_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_functions_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_functions_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_functions_name_valid CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  CONSTRAINT project_functions_runtime_valid CHECK (runtime IN ('node-22','python-3.13','go-1.24')),
  CONSTRAINT project_functions_commands_valid CHECK (char_length(commands) <= 4000),
  CONSTRAINT project_functions_timeout_valid CHECK (timeout_seconds BETWEEN 1 AND 900),
  CONSTRAINT project_functions_status_enabled_consistent CHECK ((status = 'active') = enabled),
  CONSTRAINT project_functions_execute_permissions_valid CHECK (stealth_database_permissions_valid(execute_permissions)),
  CONSTRAINT project_functions_entrypoint_valid CHECK (
    char_length(entrypoint) BETWEEN 1 AND 255 AND
    entrypoint = btrim(entrypoint) AND
    entrypoint <> '.' AND entrypoint <> '..' AND
    position(chr(13) IN entrypoint) = 0 AND position(chr(10) IN entrypoint) = 0
  ),
  CONSTRAINT project_functions_description_valid CHECK (
    description IS NULL OR char_length(description) <= 2000
  ),
  CONSTRAINT project_functions_status_valid CHECK (status IN ('active','disabled')),
  CONSTRAINT project_functions_quota_positive CHECK (artifact_quota_bytes > 0),
  CONSTRAINT project_functions_used_valid CHECK (artifact_used_bytes >= 0 AND artifact_used_bytes <= artifact_quota_bytes)
);

CREATE UNIQUE INDEX project_functions_project_name_unique ON project_functions (project_id, name);
CREATE INDEX project_functions_project_id_id_idx ON project_functions (project_id, id);

CREATE TABLE function_deployments (
  id UUID PRIMARY KEY,
  function_id UUID NOT NULL,
  project_id UUID NOT NULL,
  version BIGINT NOT NULL,
  source TEXT NOT NULL DEFAULT 'upload',
  source_name TEXT,
  size_bytes BIGINT NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  source_path TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'ready',
  build_status TEXT NOT NULL DEFAULT 'deferred',
  error_message TEXT,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  build_started_at TIMESTAMPTZ,
  built_at TIMESTAMPTZ,
  activated_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT function_deployments_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT function_deployments_function_project_fk FOREIGN KEY (function_id, project_id)
    REFERENCES project_functions(id, project_id) ON DELETE CASCADE,
  CONSTRAINT function_deployments_id_function_project_unique UNIQUE (id, function_id, project_id),
  CONSTRAINT function_deployments_function_version_unique UNIQUE (function_id, project_id, version),
  CONSTRAINT function_deployments_version_positive CHECK (version > 0),
  CONSTRAINT function_deployments_source_valid CHECK (source ~ '^[a-z0-9][a-z0-9._-]{0,31}$'),
  CONSTRAINT function_deployments_source_name_valid CHECK (
    source_name IS NULL OR (
      char_length(source_name) BETWEEN 1 AND 255 AND source_name = btrim(source_name) AND
      source_name <> '.' AND source_name <> '..' AND position('/' IN source_name) = 0 AND
      position(chr(92) IN source_name) = 0 AND
      position(chr(13) IN source_name) = 0 AND position(chr(10) IN source_name) = 0
    )
  ),
  CONSTRAINT function_deployments_size_valid CHECK (size_bytes >= 0),
  CONSTRAINT function_deployments_checksum_valid CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
  CONSTRAINT function_deployments_path_valid CHECK (
    source_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
  ),
  CONSTRAINT function_deployments_status_valid CHECK (status IN ('queued','building','ready','active','superseded','failed','cancelled')),
  CONSTRAINT function_deployments_build_status_valid CHECK (build_status IN ('queued','running','deferred','succeeded','failed')),
  CONSTRAINT function_deployments_error_valid CHECK (error_message IS NULL OR char_length(error_message) <= 4000)
);

CREATE INDEX function_deployments_project_function_id_idx ON function_deployments (project_id, function_id, id);
CREATE INDEX function_deployments_project_created_at_idx ON function_deployments (project_id, created_at DESC, id DESC);

-- Only active_deployment_id is nulled when a function is cascaded. The
-- function/project identity columns are not nullable and must remain intact.
ALTER TABLE project_functions
  ADD CONSTRAINT project_functions_active_deployment_fk
  FOREIGN KEY (active_deployment_id, id, project_id)
  REFERENCES function_deployments (id, function_id, project_id)
  ON DELETE SET NULL (active_deployment_id);

CREATE TABLE function_variables (
  id UUID PRIMARY KEY,
  function_id UUID NOT NULL,
  project_id UUID NOT NULL,
  key TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'variable',
  is_secret BOOLEAN NOT NULL DEFAULT false,
  value_ciphertext BYTEA,
  description TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT function_variables_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT function_variables_function_project_fk FOREIGN KEY (function_id, project_id)
    REFERENCES project_functions(id, project_id) ON DELETE CASCADE,
  CONSTRAINT function_variables_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT function_variables_function_key_unique UNIQUE (function_id, project_id, key),
  CONSTRAINT function_variables_key_valid CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]{0,119}$'),
  CONSTRAINT function_variables_kind_valid CHECK (kind IN ('variable','secret')),
  CONSTRAINT function_variables_secret_kind_consistent CHECK (is_secret = (kind = 'secret')),
  CONSTRAINT function_variables_description_valid CHECK (
    description IS NULL OR char_length(description) <= 2000
  )
);

CREATE INDEX function_variables_project_function_id_idx ON function_variables (project_id, function_id, id);

CREATE TABLE function_build_logs (
  id UUID PRIMARY KEY,
  deployment_id UUID NOT NULL,
  function_id UUID NOT NULL,
  project_id UUID NOT NULL,
  sequence BIGINT NOT NULL,
  level TEXT NOT NULL DEFAULT 'info',
  message TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT function_build_logs_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT function_build_logs_deployment_fk FOREIGN KEY (deployment_id, function_id, project_id)
    REFERENCES function_deployments(id, function_id, project_id) ON DELETE CASCADE,
  CONSTRAINT function_build_logs_sequence_positive CHECK (sequence > 0),
  CONSTRAINT function_build_logs_level_valid CHECK (level IN ('debug','info','warn','error')),
  CONSTRAINT function_build_logs_message_valid CHECK (char_length(message) BETWEEN 1 AND 16000),
  CONSTRAINT function_build_logs_deployment_sequence_unique UNIQUE (deployment_id, sequence)
);

CREATE INDEX function_build_logs_project_deployment_sequence_idx ON function_build_logs (project_id, deployment_id, sequence);

CREATE TABLE function_executions (
  id UUID PRIMARY KEY,
  deployment_id UUID NOT NULL,
  function_id UUID NOT NULL,
  project_id UUID NOT NULL,
  status TEXT NOT NULL DEFAULT 'accepted',
  trigger TEXT NOT NULL DEFAULT 'manual',
  error_message TEXT,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT function_executions_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT function_executions_id_function_project_unique UNIQUE (id, function_id, project_id),
  CONSTRAINT function_executions_deployment_fk FOREIGN KEY (deployment_id, function_id, project_id)
    REFERENCES function_deployments(id, function_id, project_id) ON DELETE CASCADE,
  CONSTRAINT function_executions_status_valid CHECK (status IN ('accepted','running','succeeded','failed','cancelled')),
  CONSTRAINT function_executions_trigger_valid CHECK (trigger ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
  CONSTRAINT function_executions_error_valid CHECK (error_message IS NULL OR char_length(error_message) <= 4000)
);

CREATE INDEX function_executions_project_function_created_at_idx ON function_executions (project_id, function_id, created_at DESC, id DESC);
CREATE INDEX function_executions_project_deployment_created_at_idx ON function_executions (project_id, deployment_id, created_at DESC, id DESC);

CREATE TABLE function_execution_logs (
  id UUID PRIMARY KEY,
  execution_id UUID NOT NULL,
  function_id UUID NOT NULL,
  project_id UUID NOT NULL,
  sequence BIGINT NOT NULL,
  level TEXT NOT NULL DEFAULT 'info',
  message TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT function_execution_logs_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT function_execution_logs_execution_fk FOREIGN KEY (execution_id, function_id, project_id)
    REFERENCES function_executions(id, function_id, project_id) ON DELETE CASCADE,
  CONSTRAINT function_execution_logs_sequence_positive CHECK (sequence > 0),
  CONSTRAINT function_execution_logs_level_valid CHECK (level IN ('debug','info','warn','error')),
  CONSTRAINT function_execution_logs_message_valid CHECK (char_length(message) BETWEEN 1 AND 16000),
  CONSTRAINT function_execution_logs_execution_sequence_unique UNIQUE (execution_id, sequence)
);

CREATE INDEX function_execution_logs_project_execution_sequence_idx ON function_execution_logs (project_id, execution_id, sequence);
