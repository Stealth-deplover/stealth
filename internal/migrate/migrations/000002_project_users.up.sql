CREATE TABLE project_users (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  email TEXT NOT NULL,
  display_name TEXT,
  status TEXT NOT NULL DEFAULT 'active',
  email_verified BOOLEAN NOT NULL DEFAULT false,
  password_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_users_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_users_email_normalized CHECK (email = lower(email) AND email = btrim(email)),
  CONSTRAINT project_users_email_length CHECK (char_length(email) BETWEEN 3 AND 320),
  CONSTRAINT project_users_display_name_length CHECK (display_name IS NULL OR char_length(display_name) BETWEEN 2 AND 120),
  CONSTRAINT project_users_display_name_trimmed CHECK (display_name IS NULL OR display_name = btrim(display_name)),
  CONSTRAINT project_users_status_valid CHECK (status IN ('active', 'blocked'))
);

CREATE UNIQUE INDEX project_users_project_email_unique ON project_users (project_id, email);
CREATE INDEX project_users_project_id_id_idx ON project_users (project_id, id);
