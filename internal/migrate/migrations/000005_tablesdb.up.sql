-- Database core foundation. Advanced features are added through forward-only
-- migrations; this base migration owns the typed table/row primitives.

-- Existing API keys remain valid.  Expand the canonical scope constraint in a
-- forward-only migration rather than rewriting the already-issued secrets.
CREATE FUNCTION stealth_api_key_scopes_canonical(input_values text[]) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  value text;
  previous text := '';
BEGIN
  IF input_values IS NULL OR cardinality(input_values) < 1 OR cardinality(input_values) > 4 OR array_position(input_values, NULL) IS NOT NULL THEN
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
  ADD CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 4),
  ADD CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY['users.read','users.write','databases.read','databases.write']::text[]),
  ADD CONSTRAINT project_api_keys_scopes_canonical CHECK (stealth_api_key_scopes_canonical(scopes));

-- PostgreSQL CHECK expressions cannot contain a subquery, so permission
-- validation lives in one immutable function shared by all permission arrays.
CREATE FUNCTION stealth_database_permissions_valid(input_values text[]) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  value text;
  previous text := '';
BEGIN
  IF input_values IS NULL OR array_position(input_values, NULL) IS NOT NULL THEN
    RETURN false;
  END IF;
  FOREACH value IN ARRAY input_values LOOP
    IF value <> 'any' AND value <> 'users' AND value !~ '^user:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
      RETURN false;
    END IF;
    IF previous <> '' AND previous >= value THEN
      RETURN false;
    END IF;
    previous := value;
  END LOOP;
  RETURN true;
END;
$$;

-- 000003 normally creates this key. Keep the dependency explicit for
-- installations that adopted the earlier project-user migration manually.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'project_users_id_project_id_unique'
      AND conrelid = 'project_users'::regclass
  ) THEN
    ALTER TABLE project_users ADD CONSTRAINT project_users_id_project_id_unique UNIQUE (id, project_id);
  END IF;
END
$$;

CREATE TABLE project_databases (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_databases_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_databases_name_length CHECK (char_length(name) BETWEEN 2 AND 120),
  CONSTRAINT project_databases_name_trimmed CHECK (name = btrim(name)),
  CONSTRAINT project_databases_project_id_id_unique UNIQUE (project_id, id)
);

CREATE UNIQUE INDEX project_databases_project_name_unique ON project_databases (project_id, name);
CREATE INDEX project_databases_project_id_id_idx ON project_databases (project_id, id);

CREATE TABLE database_tables (
  id UUID PRIMARY KEY,
  database_id UUID NOT NULL,
  project_id UUID NOT NULL,
  name TEXT NOT NULL,
  row_security BOOLEAN NOT NULL DEFAULT true,
  create_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  read_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  update_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  delete_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT database_tables_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT database_tables_name_length CHECK (char_length(name) BETWEEN 2 AND 120),
  CONSTRAINT database_tables_name_trimmed CHECK (name = btrim(name)),
  CONSTRAINT database_tables_project_database_fk FOREIGN KEY (database_id, project_id)
    REFERENCES project_databases(id, project_id) ON DELETE CASCADE,
  CONSTRAINT database_tables_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT database_tables_create_permissions_valid CHECK (stealth_database_permissions_valid(create_permissions)),
  CONSTRAINT database_tables_read_permissions_valid CHECK (stealth_database_permissions_valid(read_permissions)),
  CONSTRAINT database_tables_update_permissions_valid CHECK (stealth_database_permissions_valid(update_permissions)),
  CONSTRAINT database_tables_delete_permissions_valid CHECK (stealth_database_permissions_valid(delete_permissions))
);

CREATE UNIQUE INDEX database_tables_database_name_unique ON database_tables (database_id, name);
CREATE INDEX database_tables_project_id_id_idx ON database_tables (project_id, id);

CREATE TABLE database_columns (
  id UUID PRIMARY KEY,
  table_id UUID NOT NULL REFERENCES database_tables(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  column_type TEXT NOT NULL,
  required BOOLEAN NOT NULL DEFAULT false,
  varchar_size INTEGER,
  default_value JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT database_columns_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT database_columns_key_valid CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]{0,119}$'),
  CONSTRAINT database_columns_type_valid CHECK (column_type IN ('varchar','text','integer','double','boolean','datetime','json')),
  CONSTRAINT database_columns_varchar_size_valid CHECK (
    (column_type = 'varchar' AND varchar_size BETWEEN 1 AND 10000) OR
    (column_type <> 'varchar' AND varchar_size IS NULL)
  ),
  CONSTRAINT database_columns_default_object_valid CHECK (default_value IS NULL OR jsonb_typeof(default_value) IS NOT NULL),
  CONSTRAINT database_columns_key_not_system CHECK (lower(key) NOT IN ('id','table_id','project_id','created_at','updated_at'))
);

CREATE UNIQUE INDEX database_columns_table_key_unique ON database_columns (table_id, key);
CREATE INDEX database_columns_table_id_id_idx ON database_columns (table_id, id);

CREATE TABLE database_rows (
  id UUID PRIMARY KEY,
  table_id UUID NOT NULL,
  project_id UUID NOT NULL,
  data JSONB NOT NULL DEFAULT '{}'::jsonb,
  read_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  update_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  delete_permissions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  creator_project_user_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT database_rows_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT database_rows_data_object CHECK (jsonb_typeof(data) = 'object'),
  CONSTRAINT database_rows_table_project_fk FOREIGN KEY (table_id, project_id)
    REFERENCES database_tables(id, project_id) ON DELETE CASCADE,
  CONSTRAINT database_rows_creator_project_fk FOREIGN KEY (creator_project_user_id, project_id)
    REFERENCES project_users(id, project_id) ON DELETE SET NULL,
  CONSTRAINT database_rows_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT database_rows_read_permissions_valid CHECK (stealth_database_permissions_valid(read_permissions)),
  CONSTRAINT database_rows_update_permissions_valid CHECK (stealth_database_permissions_valid(update_permissions)),
  CONSTRAINT database_rows_delete_permissions_valid CHECK (stealth_database_permissions_valid(delete_permissions))
);

CREATE INDEX database_rows_table_id_id_idx ON database_rows (table_id, id);
CREATE INDEX database_rows_project_id_id_idx ON database_rows (project_id, id);

CREATE TABLE database_indexes (
  id UUID PRIMARY KEY,
  table_id UUID NOT NULL REFERENCES database_tables(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  index_type TEXT NOT NULL,
  column_keys TEXT[] NOT NULL,
  directions TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT database_indexes_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT database_indexes_name_length CHECK (char_length(name) BETWEEN 2 AND 120),
  CONSTRAINT database_indexes_name_trimmed CHECK (name = btrim(name)),
  CONSTRAINT database_indexes_type_valid CHECK (index_type IN ('key','unique')),
  CONSTRAINT database_indexes_column_keys_valid CHECK (cardinality(column_keys) BETWEEN 1 AND 16 AND array_position(column_keys, NULL) IS NULL),
  CONSTRAINT database_indexes_directions_valid CHECK (cardinality(directions) = cardinality(column_keys) AND directions <@ ARRAY['asc','desc']::text[] AND array_position(directions, NULL) IS NULL)
);

CREATE UNIQUE INDEX database_indexes_table_name_unique ON database_indexes (table_id, name);
CREATE INDEX database_indexes_table_id_id_idx ON database_indexes (table_id, id);
