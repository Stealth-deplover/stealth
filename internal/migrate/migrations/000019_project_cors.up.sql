-- Browser applications need an explicit origin allowlist. A project keeps
-- this separate from the Console session and never treats '*' as a valid
-- credentialed origin.
ALTER TABLE project_auth_settings
  ADD COLUMN cors_origins TEXT[] NOT NULL DEFAULT '{}'::text[];

ALTER TABLE project_auth_settings
  ADD CONSTRAINT project_auth_settings_cors_origins_max
  CHECK (cardinality(cors_origins) <= 32);

ALTER TABLE project_auth_settings
  ADD CONSTRAINT project_auth_settings_cors_origins_no_null
  CHECK (array_position(cors_origins, NULL) IS NULL);
