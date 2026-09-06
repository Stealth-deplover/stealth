-- Keep each counter monotonic even though failure and invocation totals may
-- live in different calendar buckets when an invocation crosses midnight.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conrelid = 'project_usage_daily'::regclass
      AND conname = 'project_usage_daily_function_failures_nonnegative'
  ) THEN
    ALTER TABLE project_usage_daily
      ADD CONSTRAINT project_usage_daily_function_failures_nonnegative
      CHECK (function_failure_count >= 0);
  END IF;
END
$$;
