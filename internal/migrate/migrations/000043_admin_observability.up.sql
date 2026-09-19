-- Instance-owner observability control plane. High-volume signal data stays
-- in ClickHouse; these tables contain only configuration, bounded state, and
-- operator history.
CREATE TABLE admin_monitors (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  target TEXT NOT NULL,
  interval_seconds INTEGER NOT NULL DEFAULT 60,
  timeout_ms INTEGER NOT NULL DEFAULT 5000,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  public_config JSONB NOT NULL DEFAULT '{}'::jsonb,
  config_encrypted BYTEA NOT NULL,
  heartbeat_token_hash BYTEA,
  status TEXT NOT NULL DEFAULT 'unknown',
  last_checked_at TIMESTAMPTZ,
  last_success_at TIMESTAMPTZ,
  last_failure_at TIMESTAMPTZ,
  last_latency_ms BIGINT,
  last_status_code INTEGER,
  last_error TEXT,
  last_heartbeat_at TIMESTAMPTZ,
  next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  leased_at TIMESTAMPTZ,
  worker_id TEXT,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_monitors_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_monitors_name_valid CHECK (char_length(name) BETWEEN 1 AND 120 AND name !~ '[[:cntrl:]]'),
  CONSTRAINT admin_monitors_kind_valid CHECK (kind IN ('http','tcp','dns','tls','heartbeat')),
  CONSTRAINT admin_monitors_target_valid CHECK (char_length(target) BETWEEN 1 AND 2048 AND target !~ '[[:cntrl:]]'),
  CONSTRAINT admin_monitors_interval_valid CHECK (interval_seconds BETWEEN 5 AND 86400),
  CONSTRAINT admin_monitors_timeout_valid CHECK (timeout_ms BETWEEN 100 AND 120000),
  CONSTRAINT admin_monitors_status_valid CHECK (status IN ('unknown','healthy','degraded','failing','paused')),
  CONSTRAINT admin_monitors_error_valid CHECK (last_error IS NULL OR char_length(last_error) <= 1000),
  CONSTRAINT admin_monitors_status_code_valid CHECK (last_status_code IS NULL OR last_status_code BETWEEN 100 AND 599),
  CONSTRAINT admin_monitors_latency_valid CHECK (last_latency_ms IS NULL OR last_latency_ms >= 0),
  CONSTRAINT admin_monitors_worker_valid CHECK (worker_id IS NULL OR (char_length(worker_id) BETWEEN 1 AND 128 AND worker_id !~ '[[:cntrl:]]')),
  CONSTRAINT admin_monitors_heartbeat_hash_valid CHECK (heartbeat_token_hash IS NULL OR octet_length(heartbeat_token_hash) = 32)
);
CREATE INDEX admin_monitors_due_idx ON admin_monitors (next_check_at, id) WHERE enabled;
CREATE INDEX admin_monitors_status_idx ON admin_monitors (status, updated_at DESC);

CREATE TABLE admin_monitor_checks (
  id UUID PRIMARY KEY,
  monitor_id UUID NOT NULL REFERENCES admin_monitors(id) ON DELETE CASCADE,
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  success BOOLEAN NOT NULL,
  latency_ms BIGINT NOT NULL,
  status_code INTEGER,
  error TEXT,
  details JSONB NOT NULL DEFAULT '{}'::jsonb,
  CONSTRAINT admin_monitor_checks_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_monitor_checks_latency_valid CHECK (latency_ms >= 0),
  CONSTRAINT admin_monitor_checks_status_code_valid CHECK (status_code IS NULL OR status_code BETWEEN 100 AND 599),
  CONSTRAINT admin_monitor_checks_error_valid CHECK (error IS NULL OR char_length(error) <= 1000)
);
CREATE INDEX admin_monitor_checks_monitor_time_idx ON admin_monitor_checks (monitor_id, checked_at DESC, id DESC);

CREATE TABLE admin_alert_rules (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  condition JSONB NOT NULL,
  severity TEXT NOT NULL DEFAULT 'warning',
  for_seconds INTEGER NOT NULL DEFAULT 0,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  state TEXT NOT NULL DEFAULT 'normal',
  pending_since TIMESTAMPTZ,
  last_evaluated_at TIMESTAMPTZ,
  last_value DOUBLE PRECISION,
  last_error TEXT,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_alert_rules_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_alert_rules_name_valid CHECK (char_length(name) BETWEEN 1 AND 120 AND name !~ '[[:cntrl:]]'),
  CONSTRAINT admin_alert_rules_kind_valid CHECK (kind IN ('metric_threshold','error_rate','latency','log_match','monitor_failure','service_health','disk_pressure','backup_failure','job_failure','heartbeat_failure','certificate_expiry')),
  CONSTRAINT admin_alert_rules_severity_valid CHECK (severity IN ('info','warning','critical')),
  CONSTRAINT admin_alert_rules_for_valid CHECK (for_seconds BETWEEN 0 AND 86400),
  CONSTRAINT admin_alert_rules_state_valid CHECK (state IN ('normal','pending','firing','resolved','muted')),
  CONSTRAINT admin_alert_rules_error_valid CHECK (last_error IS NULL OR char_length(last_error) <= 1000)
);
CREATE INDEX admin_alert_rules_state_idx ON admin_alert_rules (state, updated_at DESC);

CREATE TABLE admin_alert_events (
  id UUID PRIMARY KEY,
  rule_id UUID NOT NULL REFERENCES admin_alert_rules(id) ON DELETE CASCADE,
  state TEXT NOT NULL,
  value DOUBLE PRECISION,
  message TEXT NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_alert_events_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_alert_events_state_valid CHECK (state IN ('firing','resolved')),
  CONSTRAINT admin_alert_events_message_valid CHECK (char_length(message) BETWEEN 1 AND 1000)
);
CREATE INDEX admin_alert_events_rule_time_idx ON admin_alert_events (rule_id, occurred_at DESC, id DESC);

CREATE TABLE admin_notification_channels (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  config_encrypted BYTEA NOT NULL,
  last_delivery_at TIMESTAMPTZ,
  last_delivery_status TEXT,
  last_error TEXT,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_notification_channels_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_notification_channels_name_valid CHECK (char_length(name) BETWEEN 1 AND 120 AND name !~ '[[:cntrl:]]'),
  CONSTRAINT admin_notification_channels_kind_valid CHECK (kind IN ('email','webhook','slack','discord','telegram')),
  CONSTRAINT admin_notification_channels_status_valid CHECK (last_delivery_status IS NULL OR last_delivery_status IN ('success','failed')),
  CONSTRAINT admin_notification_channels_error_valid CHECK (last_error IS NULL OR char_length(last_error) <= 1000)
);

CREATE TABLE admin_notification_deliveries (
  id UUID PRIMARY KEY,
  channel_id UUID NOT NULL REFERENCES admin_notification_channels(id) ON DELETE CASCADE,
  alert_event_id UUID REFERENCES admin_alert_events(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error TEXT,
  delivered_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_notification_deliveries_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_notification_deliveries_status_valid CHECK (status IN ('pending','delivered','failed')),
  CONSTRAINT admin_notification_deliveries_attempts_valid CHECK (attempts >= 0),
  CONSTRAINT admin_notification_deliveries_error_valid CHECK (last_error IS NULL OR char_length(last_error) <= 1000)
);
CREATE INDEX admin_notification_deliveries_due_idx ON admin_notification_deliveries (available_at, id) WHERE status = 'pending';

CREATE TABLE admin_incidents (
  id UUID PRIMARY KEY,
  title TEXT NOT NULL,
  severity TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'investigating',
  services JSONB NOT NULL DEFAULT '[]'::jsonb,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_incidents_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_incidents_title_valid CHECK (char_length(title) BETWEEN 1 AND 240 AND title !~ '[[:cntrl:]]'),
  CONSTRAINT admin_incidents_severity_valid CHECK (severity IN ('info','warning','critical')),
  CONSTRAINT admin_incidents_status_valid CHECK (status IN ('investigating','identified','monitoring','resolved')),
  CONSTRAINT admin_incidents_resolution_valid CHECK ((status = 'resolved') = (resolved_at IS NOT NULL))
);
CREATE INDEX admin_incidents_status_time_idx ON admin_incidents (status, started_at DESC, id DESC);

CREATE TABLE admin_incident_events (
  id UUID PRIMARY KEY,
  incident_id UUID NOT NULL REFERENCES admin_incidents(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  message TEXT NOT NULL,
  actor_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_incident_events_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_incident_events_kind_valid CHECK (kind IN ('alert','deployment','restart','backup','configuration','monitor','note')),
  CONSTRAINT admin_incident_events_message_valid CHECK (char_length(message) BETWEEN 1 AND 2000 AND message !~ '[[:cntrl:]]')
);
CREATE INDEX admin_incident_events_time_idx ON admin_incident_events (incident_id, created_at ASC, id ASC);

CREATE TABLE admin_dashboards (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  definition JSONB NOT NULL DEFAULT '{"panels":[]}'::jsonb,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_dashboards_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT admin_dashboards_name_valid CHECK (char_length(name) BETWEEN 1 AND 120 AND name !~ '[[:cntrl:]]'),
  CONSTRAINT admin_dashboards_description_valid CHECK (char_length(description) <= 2000)
);
CREATE INDEX admin_dashboards_updated_idx ON admin_dashboards (updated_at DESC, id DESC);

CREATE TABLE admin_status_page_config (
  id BOOLEAN PRIMARY KEY DEFAULT TRUE,
  name TEXT NOT NULL DEFAULT 'Stealth status',
  description TEXT NOT NULL DEFAULT '',
  is_public BOOLEAN NOT NULL DEFAULT FALSE,
  components JSONB NOT NULL DEFAULT '[]'::jsonb,
  published_incidents JSONB NOT NULL DEFAULT '[]'::jsonb,
  updated_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT admin_status_page_singleton CHECK (id),
  CONSTRAINT admin_status_page_name_valid CHECK (char_length(name) BETWEEN 1 AND 160),
  CONSTRAINT admin_status_page_description_valid CHECK (char_length(description) <= 4000)
);
INSERT INTO admin_status_page_config (id) VALUES (TRUE) ON CONFLICT (id) DO NOTHING;
