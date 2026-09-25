-- App runtime logs remain in ClickHouse. PostgreSQL stores only the verified
-- App-to-container association used to authorize exact-container queries.
CREATE TABLE app_runtime_log_sources (
  app_id UUID NOT NULL,
  project_id UUID NOT NULL,
  container_id TEXT NOT NULL,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT app_runtime_log_sources_app_project_fk
    FOREIGN KEY (project_id, app_id) REFERENCES project_apps(project_id, id) ON DELETE CASCADE,
  CONSTRAINT app_runtime_log_sources_container_id_valid
    CHECK (container_id ~ '^[0-9a-f]{12,64}$'),
  CONSTRAINT app_runtime_log_sources_seen_ordered
    CHECK (last_seen_at >= first_seen_at),
  CONSTRAINT app_runtime_log_sources_app_container_pk
    PRIMARY KEY (app_id, container_id),
  CONSTRAINT app_runtime_log_sources_container_unique
    UNIQUE (container_id)
);

CREATE INDEX app_runtime_log_sources_project_app_idx
  ON app_runtime_log_sources (project_id, app_id, first_seen_at, container_id);

-- A migration can verify only the container currently recorded in runtime
-- state. It deliberately does not invent records for retired containers.
INSERT INTO app_runtime_log_sources (app_id, project_id, container_id, first_seen_at, last_seen_at)
SELECT runtime.app_id, runtime.project_id, runtime.container_id,
       COALESCE(runtime.last_inspected_at, runtime.created_at, now()),
       COALESCE(runtime.last_inspected_at, runtime.created_at, now())
FROM app_runtime_state runtime
JOIN project_apps app ON app.id=runtime.app_id AND app.project_id=runtime.project_id
WHERE runtime.container_id ~ '^[0-9a-f]{12,64}$';
