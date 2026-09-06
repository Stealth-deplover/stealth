-- Add PostgreSQL-backed full-text indexes to the typed database module.
-- The index metadata remains in database_indexes so it is managed and audited
-- through the existing database API.
ALTER TABLE database_indexes
  DROP CONSTRAINT database_indexes_type_valid,
  ADD CONSTRAINT database_indexes_type_valid CHECK (index_type IN ('key','unique','fulltext'));

ALTER TABLE database_indexes
  ADD CONSTRAINT database_indexes_fulltext_columns_valid CHECK (
    index_type <> 'fulltext' OR cardinality(column_keys) = 1
  );
