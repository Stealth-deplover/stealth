-- Durable organization plan state. Plan limits remain server-owned catalog
-- data so an operator can change commercial definitions without rewriting
-- tenant rows; this table stores only the subscribed plan lifecycle.
CREATE TABLE organization_plans (
  organization_id UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
  plan_key TEXT NOT NULL DEFAULT 'free',
  status TEXT NOT NULL DEFAULT 'active',
  current_period_start DATE NOT NULL DEFAULT date_trunc('month', now())::date,
  current_period_end DATE NOT NULL DEFAULT (date_trunc('month', now()) + interval '1 month - 1 day')::date,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT organization_plans_key_valid CHECK (plan_key IN ('free', 'pro', 'enterprise')),
  CONSTRAINT organization_plans_status_valid CHECK (status IN ('active', 'past_due', 'canceled')),
  CONSTRAINT organization_plans_period_valid CHECK (current_period_end >= current_period_start)
);

CREATE INDEX organization_plans_status_idx ON organization_plans (status, current_period_end);
