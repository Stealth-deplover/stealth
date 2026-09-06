ALTER TABLE project_users
  ADD CONSTRAINT project_users_id_project_id_unique UNIQUE (id, project_id);

CREATE TABLE project_auth_settings (
  project_id UUID PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  registration_enabled BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO project_auth_settings (project_id)
SELECT id FROM projects
ON CONFLICT (project_id) DO NOTHING;

CREATE TABLE project_user_sessions (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  project_user_id UUID NOT NULL,
  token_hash BYTEA NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_user_sessions_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_user_sessions_token_hash_length CHECK (octet_length(token_hash) = 32),
  CONSTRAINT project_user_sessions_user_project_fk FOREIGN KEY (project_user_id, project_id)
    REFERENCES project_users(id, project_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX project_user_sessions_token_hash_unique ON project_user_sessions (token_hash);
CREATE INDEX project_user_sessions_user_expires_at_idx ON project_user_sessions (project_user_id, expires_at DESC);
CREATE INDEX project_user_sessions_project_expires_at_idx ON project_user_sessions (project_id, expires_at DESC);
