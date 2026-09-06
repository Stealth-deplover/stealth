-- Coding-agent configuration is a project resource. Run output, provider
-- credentials, and source changes are intentionally modelled separately so a
-- configuration read can never expose a secret or pretend a run completed.
CREATE TABLE project_agents (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'General',
  status TEXT NOT NULL DEFAULT 'idle',
  branch TEXT NOT NULL DEFAULT 'main',
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  current_task TEXT,
  last_active_at TIMESTAMPTZ,
  tools TEXT[] NOT NULL DEFAULT ARRAY[]::text[],
  instructions TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT project_agents_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT project_agents_project_id_id_unique UNIQUE (project_id, id),
  CONSTRAINT project_agents_name_valid CHECK (char_length(name) BETWEEN 2 AND 120 AND name = btrim(name)),
  CONSTRAINT project_agents_description_valid CHECK (char_length(description) <= 2000),
  CONSTRAINT project_agents_role_valid CHECK (role IN ('General','Frontend','Reviewer','Documentation')),
  CONSTRAINT project_agents_status_valid CHECK (status IN ('active','running','idle')),
  CONSTRAINT project_agents_branch_valid CHECK (char_length(branch) BETWEEN 1 AND 255 AND branch = btrim(branch) AND branch !~ '[[:cntrl:]]'),
  CONSTRAINT project_agents_provider_valid CHECK (char_length(provider) BETWEEN 1 AND 64 AND provider = btrim(provider) AND provider !~ '[[:cntrl:]]'),
  CONSTRAINT project_agents_model_valid CHECK (char_length(model) BETWEEN 1 AND 128 AND model = btrim(model) AND model !~ '[[:cntrl:]]'),
  CONSTRAINT project_agents_current_task_valid CHECK (current_task IS NULL OR char_length(current_task) <= 500),
  CONSTRAINT project_agents_instructions_valid CHECK (instructions IS NULL OR char_length(instructions) <= 10000),
  CONSTRAINT project_agents_tools_valid CHECK (
    cardinality(tools) BETWEEN 0 AND 6 AND
    array_position(tools, NULL) IS NULL AND
    tools <@ ARRAY['Read files','Search code','Edit files','Terminal','Run tests','Git diff']::text[]
  )
);

CREATE UNIQUE INDEX project_agents_project_name_unique ON project_agents (project_id, name);
CREATE INDEX project_agents_project_id_id_idx ON project_agents (project_id, id);
CREATE INDEX project_agents_project_status_idx ON project_agents (project_id, status, id);
