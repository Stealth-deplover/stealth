-- A publish reservation is a durable intent created before a physical
-- artifact becomes visible. It is promoted to the normal cleanup queue only
-- after the reservation has been stale long enough to rule out an in-flight
-- upload/build. Metadata transactions delete the reservation before commit.
ALTER TABLE artifact_cleanup_jobs
  DROP CONSTRAINT artifact_cleanup_jobs_status_valid,
  ADD CONSTRAINT artifact_cleanup_jobs_status_valid CHECK (status IN ('reserved','pending','failed'));

CREATE INDEX artifact_cleanup_jobs_reservation_idx
  ON artifact_cleanup_jobs (updated_at, id)
  WHERE status = 'reserved';
