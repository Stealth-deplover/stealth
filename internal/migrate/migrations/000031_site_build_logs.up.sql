-- Site source builds emit bounded lifecycle logs that can be streamed by the
-- Console without exposing worker paths or cross-project deployments.

CREATE TABLE site_build_logs (
  id UUID PRIMARY KEY,
  deployment_id UUID NOT NULL,
  site_id UUID NOT NULL,
  project_id UUID NOT NULL,
  sequence BIGINT NOT NULL,
  level TEXT NOT NULL DEFAULT 'info',
  message TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT site_build_logs_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT site_build_logs_deployment_fk FOREIGN KEY (deployment_id, site_id, project_id)
    REFERENCES site_deployments(id, site_id, project_id) ON DELETE CASCADE,
  CONSTRAINT site_build_logs_sequence_positive CHECK (sequence > 0),
  CONSTRAINT site_build_logs_level_valid CHECK (level IN ('debug','info','warn','error')),
  CONSTRAINT site_build_logs_message_valid CHECK (char_length(message) BETWEEN 1 AND 16000),
  CONSTRAINT site_build_logs_deployment_sequence_unique UNIQUE (deployment_id, sequence)
);

CREATE INDEX site_build_logs_project_site_deployment_sequence_idx
  ON site_build_logs (project_id, site_id, deployment_id, sequence);
