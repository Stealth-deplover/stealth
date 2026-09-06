-- Logical database backups are immutable JSON snapshots stored through the
-- configured BlobStore. PostgreSQL keeps only ownership, checksum, and the
-- opaque storage path so local and S3 deployments share the same contract.
CREATE TABLE database_backups (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL,
  database_id UUID NOT NULL,
  storage_path TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT database_backups_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT database_backups_project_database_fk FOREIGN KEY (project_id, database_id)
    REFERENCES project_databases(project_id, id) ON DELETE CASCADE,
  CONSTRAINT database_backups_storage_path_valid CHECK (storage_path <> '' AND storage_path NOT LIKE '%..%'),
  CONSTRAINT database_backups_size_valid CHECK (size_bytes BETWEEN 1 AND 52428800),
  CONSTRAINT database_backups_checksum_valid CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
  CONSTRAINT database_backups_project_id_id_unique UNIQUE (project_id, id)
);

CREATE INDEX database_backups_project_database_id_idx
  ON database_backups (project_id, database_id, id);
