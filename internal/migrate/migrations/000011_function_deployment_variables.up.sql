-- A deployment owns the exact encrypted variable values that its build and
-- invocations use. Copying ciphertext (rather than plaintext) preserves the
-- existing application-key encryption boundary while preventing later edits
-- to function_variables from changing an immutable deployment.
CREATE TABLE function_deployment_variables (
  id UUID PRIMARY KEY,
  deployment_id UUID NOT NULL,
  function_id UUID NOT NULL,
  project_id UUID NOT NULL,
  key TEXT NOT NULL,
  kind TEXT NOT NULL,
  is_secret BOOLEAN NOT NULL,
  value_ciphertext BYTEA,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT function_deployment_variables_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT function_deployment_variables_deployment_fk FOREIGN KEY (deployment_id, function_id, project_id)
    REFERENCES function_deployments(id, function_id, project_id) ON DELETE CASCADE,
  CONSTRAINT function_deployment_variables_key_valid CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]{0,119}$'),
  CONSTRAINT function_deployment_variables_kind_valid CHECK (kind IN ('variable','secret')),
  CONSTRAINT function_deployment_variables_secret_kind_consistent CHECK (is_secret = (kind = 'secret')),
  CONSTRAINT function_deployment_variables_value_valid CHECK (value_ciphertext IS NULL OR octet_length(value_ciphertext) > 0),
  CONSTRAINT function_deployment_variables_deployment_key_unique UNIQUE (deployment_id, key)
);

CREATE INDEX function_deployment_variables_project_deployment_key_idx
  ON function_deployment_variables (project_id, deployment_id, key);

-- Preserve deployability for installations upgraded after Functions already
-- existed. A deployment with no variables intentionally receives no rows.
INSERT INTO function_deployment_variables (id,deployment_id,function_id,project_id,key,kind,is_secret,value_ciphertext)
SELECT overlay(gen_random_uuid()::text placing '7' from 15 for 1)::uuid,d.id,d.function_id,d.project_id,v.key,v.kind,v.is_secret,v.value_ciphertext
FROM function_deployments d
JOIN function_variables v ON v.function_id=d.function_id AND v.project_id=d.project_id
ON CONFLICT (deployment_id,key) DO NOTHING;
