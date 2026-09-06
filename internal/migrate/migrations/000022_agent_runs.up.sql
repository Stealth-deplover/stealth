-- Agent runs are durable queue records. A run is accepted by the Console
-- immediately, then a trusted worker may claim it and later persist output.
-- Keeping this state separate from project_agents prevents the UI from
-- presenting a local simulation as completed work.
CREATE TABLE agent_runs (
  id UUID PRIMARY KEY,
  agent_id UUID NOT NULL,
  project_id UUID NOT NULL,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  prompt TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  output_text TEXT,
  error_message TEXT,
  steps JSONB NOT NULL DEFAULT '[]'::jsonb,
  changes JSONB NOT NULL DEFAULT '[]'::jsonb,
  queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  claimed_at TIMESTAMPTZ,
  worker_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT agent_runs_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT agent_runs_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT agent_runs_agent_project_fk FOREIGN KEY (project_id, agent_id)
    REFERENCES project_agents(project_id, id) ON DELETE CASCADE,
  CONSTRAINT agent_runs_prompt_valid CHECK (char_length(prompt) BETWEEN 1 AND 20000 AND prompt = btrim(prompt)),
  CONSTRAINT agent_runs_status_valid CHECK (status IN ('queued','running','completed','failed','cancelled')),
  CONSTRAINT agent_runs_output_valid CHECK (output_text IS NULL OR char_length(output_text) <= 100000),
  CONSTRAINT agent_runs_error_valid CHECK (error_message IS NULL OR char_length(error_message) <= 4000),
  CONSTRAINT agent_runs_steps_array CHECK (jsonb_typeof(steps) = 'array'),
  CONSTRAINT agent_runs_changes_array CHECK (jsonb_typeof(changes) = 'array'),
  CONSTRAINT agent_runs_worker_valid CHECK (worker_id IS NULL OR (char_length(worker_id) BETWEEN 1 AND 128 AND worker_id !~ '[[:cntrl:]]'))
);

CREATE INDEX agent_runs_agent_created_idx ON agent_runs (agent_id, id DESC);
CREATE INDEX agent_runs_project_status_idx ON agent_runs (project_id, status, id DESC);
CREATE INDEX agent_runs_queue_idx ON agent_runs (queued_at, id) WHERE status = 'queued';
CREATE INDEX agent_runs_claimed_idx ON agent_runs (claimed_at) WHERE status = 'running';

CREATE TABLE agent_run_logs (
  id UUID PRIMARY KEY,
  run_id UUID NOT NULL,
  project_id UUID NOT NULL,
  sequence BIGINT NOT NULL,
  level TEXT NOT NULL,
  message TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT agent_run_logs_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT agent_run_logs_project_run_fk FOREIGN KEY (project_id, run_id)
    REFERENCES agent_runs(project_id, id) ON DELETE CASCADE,
  CONSTRAINT agent_run_logs_sequence_valid CHECK (sequence >= 1),
  CONSTRAINT agent_run_logs_level_valid CHECK (level IN ('debug','info','warn','error')),
  CONSTRAINT agent_run_logs_message_valid CHECK (char_length(message) BETWEEN 1 AND 16000),
  CONSTRAINT agent_run_logs_run_sequence_unique UNIQUE (run_id, sequence)
);

CREATE INDEX agent_run_logs_project_run_sequence_idx ON agent_run_logs (project_id, run_id, sequence);
