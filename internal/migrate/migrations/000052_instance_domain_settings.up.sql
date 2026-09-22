-- The instance workload domain is optional until a later routing capability
-- is configured. The boolean key and check constraint make this a singleton
-- instance-level setting rather than an organization or project property.
CREATE TABLE instance_domain_settings (
  id BOOLEAN PRIMARY KEY DEFAULT TRUE,
  workload_base_domain TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT instance_domain_settings_singleton CHECK (id),
  CONSTRAINT instance_domain_settings_domain_valid CHECK (
    workload_base_domain IS NULL OR (
      char_length(workload_base_domain) BETWEEN 1 AND 253 AND
      workload_base_domain = btrim(workload_base_domain) AND
      workload_base_domain = lower(workload_base_domain) AND
      workload_base_domain ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$'
    )
  )
);

INSERT INTO instance_domain_settings (id)
VALUES (TRUE)
ON CONFLICT (id) DO NOTHING;
