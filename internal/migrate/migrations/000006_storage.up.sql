-- Storage keeps blob bytes on the configured local filesystem. These tables
-- contain only tenant-bound metadata and opaque UUID-derived paths.

CREATE OR REPLACE FUNCTION stealth_api_key_scopes_canonical(input_values text[]) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  value text;
  previous text := '';
BEGIN
  IF input_values IS NULL OR cardinality(input_values) < 1 OR cardinality(input_values) > 6 OR array_position(input_values, NULL) IS NOT NULL THEN
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

-- A composite creator reference must only clear the creator column. The
-- project_id is part of tenant identity and is intentionally NOT NULL.
ALTER TABLE database_rows DROP CONSTRAINT IF EXISTS database_rows_creator_project_fk;
ALTER TABLE database_rows
  ADD CONSTRAINT database_rows_creator_project_fk FOREIGN KEY (creator_project_user_id, project_id)
    REFERENCES project_users(id, project_id) ON DELETE SET NULL (creator_project_user_id);

ALTER TABLE project_api_keys
  DROP CONSTRAINT project_api_keys_scopes_nonempty,
  DROP CONSTRAINT project_api_keys_scopes_supported,
  DROP CONSTRAINT project_api_keys_scopes_canonical;

ALTER TABLE project_api_keys
  ADD CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 6),
  ADD CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY['users.read','users.write','databases.read','databases.write','storage.read','storage.write']::text[]),
  ADD CONSTRAINT project_api_keys_scopes_canonical CHECK (stealth_api_key_scopes_canonical(scopes));

CREATE TABLE storage_buckets (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  file_security BOOLEAN NOT NULL DEFAULT true,
  create_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  read_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  update_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  delete_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  max_file_size_bytes BIGINT NOT NULL,
  quota_bytes BIGINT NOT NULL,
  used_bytes BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT storage_buckets_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT storage_buckets_name_valid CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  CONSTRAINT storage_buckets_name_trimmed CHECK (name = btrim(name)),
  -- Match the composite FK column order used by storage_files. The primary
  -- key still makes bucket IDs globally unique.
  CONSTRAINT storage_buckets_id_project_id_unique UNIQUE (id, project_id),
  CONSTRAINT storage_buckets_quota_positive CHECK (quota_bytes > 0),
  CONSTRAINT storage_buckets_max_file_size_positive CHECK (max_file_size_bytes > 0),
  CONSTRAINT storage_buckets_max_file_size_within_quota CHECK (max_file_size_bytes <= quota_bytes),
  CONSTRAINT storage_buckets_used_valid CHECK (used_bytes >= 0 AND used_bytes <= quota_bytes),
  CONSTRAINT storage_buckets_create_permissions_valid CHECK (stealth_database_permissions_valid(create_permissions)),
  CONSTRAINT storage_buckets_read_permissions_valid CHECK (stealth_database_permissions_valid(read_permissions)),
  CONSTRAINT storage_buckets_update_permissions_valid CHECK (stealth_database_permissions_valid(update_permissions)),
  CONSTRAINT storage_buckets_delete_permissions_valid CHECK (stealth_database_permissions_valid(delete_permissions))
);

CREATE UNIQUE INDEX storage_buckets_project_name_unique ON storage_buckets (project_id, name);
CREATE INDEX storage_buckets_project_id_id_idx ON storage_buckets (project_id, id);

CREATE TABLE storage_files (
  id UUID PRIMARY KEY,
  bucket_id UUID NOT NULL,
  project_id UUID NOT NULL,
  name TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  storage_path TEXT NOT NULL,
  read_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  update_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  delete_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  creator_project_user_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT storage_files_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT storage_files_name_valid CHECK (char_length(name) BETWEEN 1 AND 255 AND name = btrim(name) AND name <> '.' AND name <> '..' AND position('/' IN name) = 0 AND position(chr(92) IN name) = 0 AND position(chr(13) IN name) = 0 AND position(chr(10) IN name) = 0),
  CONSTRAINT storage_files_mime_type_valid CHECK (char_length(mime_type) BETWEEN 1 AND 255 AND position(chr(13) IN mime_type) = 0 AND position(chr(10) IN mime_type) = 0),
  CONSTRAINT storage_files_size_valid CHECK (size_bytes >= 0),
  CONSTRAINT storage_files_checksum_valid CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
  CONSTRAINT storage_files_path_valid CHECK (storage_path ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
  CONSTRAINT storage_files_bucket_project_fk FOREIGN KEY (bucket_id, project_id)
    REFERENCES storage_buckets(id, project_id) ON DELETE CASCADE,
  CONSTRAINT storage_files_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT storage_files_creator_project_fk FOREIGN KEY (creator_project_user_id, project_id)
    REFERENCES project_users(id, project_id) ON DELETE SET NULL (creator_project_user_id),
  CONSTRAINT storage_files_read_permissions_valid CHECK (stealth_database_permissions_valid(read_permissions)),
  CONSTRAINT storage_files_update_permissions_valid CHECK (stealth_database_permissions_valid(update_permissions)),
  CONSTRAINT storage_files_delete_permissions_valid CHECK (stealth_database_permissions_valid(delete_permissions))
);

CREATE UNIQUE INDEX storage_files_bucket_id_id_unique ON storage_files (bucket_id, id);
CREATE INDEX storage_files_project_bucket_id_idx ON storage_files (project_id, bucket_id, id);
