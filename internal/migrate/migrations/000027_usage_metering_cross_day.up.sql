-- A Function invocation can start on one UTC date and finish on another.
-- Failure and invocation counters are independent daily dimensions, so a
-- per-day failure<=invocation check would reject valid cross-day usage.
ALTER TABLE project_usage_daily
  DROP CONSTRAINT IF EXISTS project_usage_daily_function_failures_valid;
