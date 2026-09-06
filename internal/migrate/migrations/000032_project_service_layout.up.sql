CREATE TABLE project_service_layouts (
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  resource_type TEXT NOT NULL,
  resource_id UUID NOT NULL,
  x INTEGER NOT NULL DEFAULT 0,
  y INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, resource_type, resource_id),
  CONSTRAINT project_service_layouts_type_check CHECK (resource_type IN ('function', 'site', 'database', 'storage')),
  CONSTRAINT project_service_layouts_x_check CHECK (x BETWEEN -100000 AND 100000),
  CONSTRAINT project_service_layouts_y_check CHECK (y BETWEEN -100000 AND 100000)
);

CREATE INDEX project_service_layouts_project_updated_idx
  ON project_service_layouts (project_id, updated_at DESC);
