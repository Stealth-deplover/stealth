-- Root request traces are a small durable index for the Console. Full span
-- payloads remain in the private OpenTelemetry backend; this table stores only
-- bounded request metadata and the tenant scope needed for authorization.
CREATE TABLE http_traces (
  id UUID PRIMARY KEY,
  trace_id TEXT NOT NULL,
  span_id TEXT,
  organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
  project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
  account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  method TEXT NOT NULL,
  route TEXT NOT NULL,
  status INTEGER NOT NULL,
  duration_ms BIGINT NOT NULL,
  response_bytes BIGINT NOT NULL DEFAULT 0,
  started_at TIMESTAMPTZ NOT NULL,
  finished_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT http_traces_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT http_traces_trace_id_length CHECK (char_length(trace_id) BETWEEN 16 AND 64),
  CONSTRAINT http_traces_span_id_length CHECK (span_id IS NULL OR char_length(span_id) BETWEEN 8 AND 32),
  CONSTRAINT http_traces_scope_check CHECK (organization_id IS NOT NULL OR project_id IS NOT NULL),
  CONSTRAINT http_traces_method_length CHECK (char_length(method) BETWEEN 3 AND 16),
  CONSTRAINT http_traces_route_length CHECK (char_length(route) BETWEEN 1 AND 240),
  CONSTRAINT http_traces_status_valid CHECK (status BETWEEN 100 AND 599),
  CONSTRAINT http_traces_duration_valid CHECK (duration_ms >= 0),
  CONSTRAINT http_traces_response_bytes_valid CHECK (response_bytes >= 0),
  CONSTRAINT http_traces_time_order CHECK (finished_at >= started_at)
);

CREATE INDEX http_traces_organization_created_idx
  ON http_traces (organization_id, id DESC);
CREATE INDEX http_traces_project_created_idx
  ON http_traces (project_id, id DESC);
CREATE INDEX http_traces_created_idx
  ON http_traces (created_at DESC, id DESC);
