-- Durable project metering. Rows are monotonic time buckets that can be
-- rolled up for billing or retention jobs without rebuilding usage from logs.
-- The current API exposes a bounded rolling window; a future billing service
-- may archive older buckets without changing the write contract.
CREATE TABLE project_usage_daily (
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  usage_date DATE NOT NULL,
  api_request_count BIGINT NOT NULL DEFAULT 0,
  api_egress_bytes BIGINT NOT NULL DEFAULT 0,
  function_invocation_count BIGINT NOT NULL DEFAULT 0,
  function_failure_count BIGINT NOT NULL DEFAULT 0,
  function_compute_ms BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, usage_date),
  CONSTRAINT project_usage_daily_api_requests_valid CHECK (api_request_count >= 0),
  CONSTRAINT project_usage_daily_api_egress_valid CHECK (api_egress_bytes >= 0),
  CONSTRAINT project_usage_daily_function_invocations_valid CHECK (function_invocation_count >= 0),
  CONSTRAINT project_usage_daily_function_failures_nonnegative CHECK (function_failure_count >= 0),
  CONSTRAINT project_usage_daily_function_compute_valid CHECK (function_compute_ms >= 0)
);

CREATE INDEX project_usage_daily_project_date_idx
  ON project_usage_daily (project_id, usage_date DESC);

-- Preserve usage that was already durable before this migration was applied.
-- Both source tables are bounded projections with the same tenant key, so the
-- backfill does not need to inspect payloads or secrets.
INSERT INTO project_usage_daily (project_id, usage_date, api_request_count, api_egress_bytes)
SELECT project_id, started_at::date, count(*), COALESCE(sum(response_bytes), 0)
FROM http_traces
WHERE project_id IS NOT NULL
GROUP BY project_id, started_at::date
ON CONFLICT (project_id, usage_date) DO UPDATE
SET api_request_count = project_usage_daily.api_request_count + EXCLUDED.api_request_count,
    api_egress_bytes = project_usage_daily.api_egress_bytes + EXCLUDED.api_egress_bytes,
    updated_at = now();

INSERT INTO project_usage_daily (project_id, usage_date, function_invocation_count, function_failure_count, function_compute_ms)
SELECT project_id,
       COALESCE(started_at, created_at)::date,
       count(*),
       count(*) FILTER (WHERE status = 'failed'),
       COALESCE(sum(GREATEST(0, EXTRACT(EPOCH FROM (finished_at - started_at)) * 1000)::bigint), 0)
FROM function_executions
GROUP BY project_id, COALESCE(started_at, created_at)::date
ON CONFLICT (project_id, usage_date) DO UPDATE
SET function_invocation_count = project_usage_daily.function_invocation_count + EXCLUDED.function_invocation_count,
    function_failure_count = project_usage_daily.function_failure_count + EXCLUDED.function_failure_count,
    function_compute_ms = project_usage_daily.function_compute_ms + EXCLUDED.function_compute_ms,
    updated_at = now();
