-- Execution input/output is kept separate from source artifacts. The runner
-- treats input as untrusted JSON and persists only bounded output metadata.
ALTER TABLE function_executions
  ADD COLUMN input_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN response_status INTEGER,
  ADD COLUMN output_json JSONB,
  ADD COLUMN output_content_type TEXT,
  ADD COLUMN claimed_at TIMESTAMPTZ,
  ADD COLUMN worker_id TEXT;

ALTER TABLE function_executions
  ADD CONSTRAINT function_executions_input_size_valid
    CHECK (octet_length(input_json::text) <= 65536),
  ADD CONSTRAINT function_executions_output_size_valid
    CHECK (output_json IS NULL OR octet_length(output_json::text) <= 1048576),
  ADD CONSTRAINT function_executions_response_status_valid
    CHECK (response_status IS NULL OR response_status BETWEEN 100 AND 599),
  ADD CONSTRAINT function_executions_output_content_type_valid
    CHECK (output_content_type IS NULL OR char_length(output_content_type) BETWEEN 1 AND 255),
  ADD CONSTRAINT function_executions_worker_id_valid
    CHECK (worker_id IS NULL OR worker_id ~ '^[A-Za-z0-9._-]{1,128}$');

CREATE INDEX function_executions_claim_idx
  ON function_executions (status, claimed_at, created_at, id)
  WHERE status IN ('accepted','running');
