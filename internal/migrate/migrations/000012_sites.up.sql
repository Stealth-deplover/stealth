-- Sites publish pre-built static archives. The API extracts only safe regular
-- files; it never executes uploaded content. Final artifacts live in a
-- private immutable directory addressed by UUIDv7 path segments.

CREATE OR REPLACE FUNCTION stealth_api_key_scopes_canonical(input_values text[]) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  value text;
  previous text := '';
BEGIN
  IF input_values IS NULL OR cardinality(input_values) < 1 OR cardinality(input_values) > 10 OR array_position(input_values, NULL) IS NOT NULL THEN
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
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_nonempty,
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_supported,
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_canonical;

ALTER TABLE project_api_keys
  ADD CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 10),
  ADD CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY[
    'users.read','users.write',
    'databases.read','databases.write',
    'storage.read','storage.write',
    'functions.read','functions.write',
    'sites.read','sites.write'
  ]::text[]),
  ADD CONSTRAINT project_api_keys_scopes_canonical CHECK (stealth_api_key_scopes_canonical(scopes));

CREATE TABLE project_sites (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  framework TEXT NOT NULL DEFAULT 'static',
  enabled BOOLEAN NOT NULL DEFAULT true,
  status TEXT NOT NULL DEFAULT 'active',
  artifact_quota_bytes BIGINT NOT NULL DEFAULT 1073741824,
  artifact_used_bytes BIGINT NOT NULL DEFAULT 0,
  active_deployment_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_sites_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_sites_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_sites_name_valid CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  CONSTRAINT project_sites_name_trimmed CHECK (name = btrim(name)),
  CONSTRAINT project_sites_framework_valid CHECK (framework IN ('static')),
  CONSTRAINT project_sites_status_valid CHECK (status IN ('active','disabled')),
  CONSTRAINT project_sites_status_enabled_consistent CHECK ((status = 'active') = enabled),
  CONSTRAINT project_sites_quota_positive CHECK (artifact_quota_bytes > 0),
  CONSTRAINT project_sites_used_valid CHECK (artifact_used_bytes >= 0 AND artifact_used_bytes <= artifact_quota_bytes)
);

CREATE UNIQUE INDEX project_sites_project_name_unique ON project_sites (project_id, name);
CREATE INDEX project_sites_project_id_id_idx ON project_sites (project_id, id);

CREATE TABLE site_deployments (
  id UUID PRIMARY KEY,
  site_id UUID NOT NULL,
  project_id UUID NOT NULL,
  version BIGINT NOT NULL,
  source TEXT NOT NULL DEFAULT 'upload',
  source_name TEXT,
  size_bytes BIGINT NOT NULL,
  archive_size_bytes BIGINT NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  artifact_path TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'ready',
  error_message TEXT,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  activated_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT site_deployments_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT site_deployments_site_project_fk FOREIGN KEY (site_id, project_id)
    REFERENCES project_sites(id, project_id) ON DELETE CASCADE,
  CONSTRAINT site_deployments_id_site_project_unique UNIQUE (id, site_id, project_id),
  CONSTRAINT site_deployments_site_version_unique UNIQUE (site_id, project_id, version),
  CONSTRAINT site_deployments_version_positive CHECK (version > 0),
  CONSTRAINT site_deployments_source_valid CHECK (source ~ '^[a-z0-9][a-z0-9._-]{0,31}$'),
  CONSTRAINT site_deployments_source_name_valid CHECK (
    source_name IS NULL OR (
      char_length(source_name) BETWEEN 1 AND 255 AND source_name = btrim(source_name) AND
      source_name <> '.' AND source_name <> '..' AND position('/' IN source_name) = 0 AND
      position(chr(92) IN source_name) = 0 AND
      position(chr(13) IN source_name) = 0 AND position(chr(10) IN source_name) = 0
    )
  ),
  CONSTRAINT site_deployments_size_valid CHECK (size_bytes > 0 AND archive_size_bytes > 0),
  CONSTRAINT site_deployments_checksum_valid CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
  CONSTRAINT site_deployments_path_valid CHECK (
    artifact_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
  ),
  CONSTRAINT site_deployments_status_valid CHECK (status IN ('queued','ready','active','superseded','failed','cancelled')),
  CONSTRAINT site_deployments_error_valid CHECK (error_message IS NULL OR char_length(error_message) <= 4000)
);

CREATE INDEX site_deployments_project_site_id_idx ON site_deployments (project_id, site_id, id);
CREATE INDEX site_deployments_project_created_at_idx ON site_deployments (project_id, created_at DESC, id DESC);

ALTER TABLE project_sites
  ADD CONSTRAINT project_sites_active_deployment_fk
  FOREIGN KEY (active_deployment_id, id, project_id)
  REFERENCES site_deployments (id, site_id, project_id)
  ON DELETE SET NULL (active_deployment_id);
