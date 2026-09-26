-- Persist exact plaintext value sizes so App environment mutations can enforce
-- one App-wide limit without decrypting all configured secrets.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM app_environment_variables
    WHERE value_ciphertext IS NOT NULL
      AND (
        octet_length(value_ciphertext) < 29
        OR octet_length(value_ciphertext) > 65565
        OR get_byte(value_ciphertext, 0) <> 1
      )
  ) THEN
    RAISE EXCEPTION 'unsupported or truncated App environment ciphertext';
  END IF;
END;
$$;

ALTER TABLE app_environment_variables
  ADD COLUMN value_plaintext_bytes INTEGER NOT NULL DEFAULT 0;

UPDATE app_environment_variables
SET value_plaintext_bytes = CASE
  WHEN value_ciphertext IS NULL THEN 0
  ELSE octet_length(value_ciphertext) - 29
END;

ALTER TABLE app_environment_variables
  ADD CONSTRAINT app_environment_variables_plaintext_size_consistent CHECK (
    (value_ciphertext IS NULL AND value_plaintext_bytes = 0)
    OR (
      value_ciphertext IS NOT NULL
      AND get_byte(value_ciphertext, 0) = 1
      AND value_plaintext_bytes BETWEEN 0 AND 65536
      AND octet_length(value_ciphertext) - 29 = value_plaintext_bytes
    )
  );
