-- Platform labels are stable Site identity, not a copy of the instance
-- workload domain. The full public hostname is derived at read/reconcile
-- time so changing the instance domain never rewrites Site identity.
ALTER TABLE project_sites
  ADD COLUMN platform_label TEXT;

ALTER TABLE project_sites
  ADD CONSTRAINT project_sites_platform_label_valid CHECK (
    platform_label IS NULL OR (
      char_length(platform_label) BETWEEN 1 AND 63 AND
      platform_label = btrim(platform_label) AND
      platform_label = lower(platform_label) AND
      platform_label ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'
    )
  );

-- Existing Sites are assigned in a stable order. A name wins the bare label
-- only when it is not infrastructure-reserved and has not already been
-- claimed; collisions use the complete immutable UUID so the result does not
-- depend on retry timing or process-local state.
DO $$
DECLARE
  site_record RECORD;
  uuid_suffix TEXT;
  candidate TEXT;
BEGIN
  FOR site_record IN
    SELECT id, name
    FROM project_sites
    WHERE platform_label IS NULL
    ORDER BY created_at ASC, id ASC
  LOOP
    uuid_suffix := replace(site_record.id::text, '-', '');

    IF site_record.name NOT IN ('api', 'admin', 'console', 'status', 'www')
       AND site_record.name !~ '-$'
       AND NOT EXISTS (
         SELECT 1 FROM project_sites WHERE platform_label = site_record.name
       ) THEN
      candidate := site_record.name;
    ELSE
      candidate := rtrim(left(site_record.name, 30), '-') || '-' || uuid_suffix;
      IF EXISTS (SELECT 1 FROM project_sites WHERE platform_label = candidate) THEN
        candidate := 'site-' || uuid_suffix;
      END IF;
      IF EXISTS (SELECT 1 FROM project_sites WHERE platform_label = candidate) THEN
        candidate := uuid_suffix;
      END IF;
      IF EXISTS (SELECT 1 FROM project_sites WHERE platform_label = candidate) THEN
        RAISE EXCEPTION 'could not deterministically allocate platform label for Site %', site_record.id;
      END IF;
    END IF;

    UPDATE project_sites
    SET platform_label = candidate
    WHERE id = site_record.id;
  END LOOP;
END $$;

ALTER TABLE project_sites
  ALTER COLUMN platform_label SET NOT NULL;

CREATE UNIQUE INDEX project_sites_platform_label_unique
  ON project_sites (platform_label);

CREATE INDEX project_sites_platform_label_active_idx
  ON project_sites (platform_label)
  WHERE enabled AND status = 'active';
