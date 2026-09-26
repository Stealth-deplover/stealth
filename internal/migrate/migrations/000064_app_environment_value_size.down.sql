ALTER TABLE app_environment_variables
  DROP CONSTRAINT app_environment_variables_plaintext_size_consistent,
  DROP COLUMN value_plaintext_bytes;
