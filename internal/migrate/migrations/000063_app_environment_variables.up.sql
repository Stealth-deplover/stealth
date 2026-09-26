-- App runtime environment metadata is tenant-scoped, while all configured
-- values are authenticated ciphertext under the dedicated operator key.
CREATE TABLE app_environment_variables (
  id UUID PRIMARY KEY,
  app_id UUID NOT NULL,
  project_id UUID NOT NULL,
  key TEXT NOT NULL,
  is_secret BOOLEAN NOT NULL DEFAULT FALSE,
  value_ciphertext BYTEA,
  description TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT app_environment_variables_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT app_environment_variables_app_fk
    FOREIGN KEY (project_id, app_id) REFERENCES project_apps(project_id, id) ON DELETE CASCADE,
  CONSTRAINT app_environment_variables_key_valid CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]{0,119}$'),
  CONSTRAINT app_environment_variables_ciphertext_valid
    CHECK (value_ciphertext IS NULL OR octet_length(value_ciphertext) BETWEEN 29 AND 65565),
  CONSTRAINT app_environment_variables_description_valid
    CHECK (description IS NULL OR octet_length(description) <= 2000),
  CONSTRAINT app_environment_variables_app_key_unique UNIQUE (app_id, key)
);

CREATE INDEX app_environment_variables_project_app_key_idx
  ON app_environment_variables (project_id, app_id, key);
