-- Database relationships model a many-to-one reference from a text/varchar
-- column to a row id in another table. Row mutations enforce the reference in
-- the repository transaction; keeping the metadata in PostgreSQL lets the
-- Console and SDK inspect the same contract.
CREATE TABLE database_relationships (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL,
  database_id UUID NOT NULL,
  source_table_id UUID NOT NULL,
  source_column_key TEXT NOT NULL,
  target_table_id UUID NOT NULL,
  relationship_type TEXT NOT NULL DEFAULT 'many_to_one',
  on_delete TEXT NOT NULL DEFAULT 'restrict',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT database_relationships_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT database_relationships_project_database_fk FOREIGN KEY (project_id, database_id)
    REFERENCES project_databases(project_id, id) ON DELETE CASCADE,
  CONSTRAINT database_relationships_source_table_fk FOREIGN KEY (project_id, source_table_id)
    REFERENCES database_tables(project_id, id) ON DELETE CASCADE,
  CONSTRAINT database_relationships_target_table_fk FOREIGN KEY (project_id, target_table_id)
    REFERENCES database_tables(project_id, id) ON DELETE CASCADE,
  CONSTRAINT database_relationships_source_column_valid CHECK (source_column_key ~ '^[A-Za-z_][A-Za-z0-9_]{0,119}$'),
  CONSTRAINT database_relationships_type_valid CHECK (relationship_type IN ('many_to_one')),
  CONSTRAINT database_relationships_on_delete_valid CHECK (on_delete IN ('restrict')),
  CONSTRAINT database_relationships_project_id_id_unique UNIQUE (project_id, id)
);

CREATE UNIQUE INDEX database_relationships_source_column_unique
  ON database_relationships (database_id, source_table_id, source_column_key);
CREATE INDEX database_relationships_target_table_idx
  ON database_relationships (database_id, target_table_id);
CREATE INDEX database_relationships_project_id_id_idx
  ON database_relationships (project_id, id);
