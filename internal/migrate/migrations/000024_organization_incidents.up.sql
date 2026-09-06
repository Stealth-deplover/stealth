-- Incidents are organization-scoped operational records. The incident row is
-- the current state; updates retain the operator timeline without storing
-- secrets or provider payloads.
CREATE TABLE organization_incidents (
  id UUID PRIMARY KEY,
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  created_by_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  title TEXT NOT NULL,
  severity TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'investigating',
  services TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT organization_incidents_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT organization_incidents_title_length CHECK (char_length(title) BETWEEN 3 AND 160),
  CONSTRAINT organization_incidents_severity_valid CHECK (severity IN ('critical', 'warning', 'info')),
  CONSTRAINT organization_incidents_status_valid CHECK (status IN ('investigating', 'identified', 'monitoring', 'resolved')),
  CONSTRAINT organization_incidents_services_count CHECK (cardinality(services) BETWEEN 1 AND 16),
  CONSTRAINT organization_incidents_started_valid CHECK (started_at <= now()),
  CONSTRAINT organization_incidents_resolved_valid CHECK (resolved_at IS NULL OR resolved_at >= started_at),
  CONSTRAINT organization_incidents_status_resolved_consistent CHECK ((status = 'resolved') = (resolved_at IS NOT NULL))
);

CREATE INDEX organization_incidents_organization_created_idx
  ON organization_incidents (organization_id, id DESC);
CREATE INDEX organization_incidents_organization_status_idx
  ON organization_incidents (organization_id, status, id DESC);

CREATE TABLE organization_incident_updates (
  id UUID PRIMARY KEY,
  incident_id UUID NOT NULL REFERENCES organization_incidents(id) ON DELETE CASCADE,
  author_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
  status TEXT NOT NULL,
  message TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT organization_incident_updates_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT organization_incident_updates_status_valid CHECK (status IN ('investigating', 'identified', 'monitoring', 'resolved')),
  CONSTRAINT organization_incident_updates_message_length CHECK (char_length(message) BETWEEN 1 AND 4000)
);

CREATE INDEX organization_incident_updates_incident_created_idx
  ON organization_incident_updates (incident_id, created_at ASC, id ASC);
