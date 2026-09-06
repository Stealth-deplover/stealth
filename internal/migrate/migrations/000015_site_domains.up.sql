-- Custom hostnames are project-owned bindings to a published Site. Ownership
-- is proven with a DNS TXT challenge before a hostname can serve traffic.

CREATE TABLE site_domains (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL,
  site_id UUID NOT NULL,
  hostname TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  verification_token TEXT NOT NULL,
  verified_at TIMESTAMPTZ,
  tls_status TEXT NOT NULL DEFAULT 'external',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT site_domains_id_uuidv7 CHECK (substring(id::text FROM 15 FOR 1) = '7'),
  CONSTRAINT site_domains_site_project_fk FOREIGN KEY (site_id, project_id)
    REFERENCES project_sites(id, project_id) ON DELETE CASCADE,
  CONSTRAINT site_domains_project_site_id_unique UNIQUE (id, project_id, site_id),
  CONSTRAINT site_domains_hostname_unique UNIQUE (hostname),
  CONSTRAINT site_domains_hostname_valid CHECK (
    char_length(hostname) BETWEEN 4 AND 253 AND
    hostname = btrim(hostname) AND
    hostname = lower(hostname) AND
    hostname ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$'
  ),
  CONSTRAINT site_domains_status_valid CHECK (status IN ('pending','verified','disabled')),
  CONSTRAINT site_domains_verification_token_valid CHECK (verification_token ~ '^[0-9a-f]{64}$'),
  CONSTRAINT site_domains_verified_at_valid CHECK ((status = 'verified') = (verified_at IS NOT NULL)),
  CONSTRAINT site_domains_tls_status_valid CHECK (tls_status IN ('external','pending','active','failed'))
);

CREATE INDEX site_domains_project_site_id_idx ON site_domains (project_id, site_id, id);
CREATE INDEX site_domains_verified_hostname_idx ON site_domains (hostname) WHERE status = 'verified';
