-- Refuse to invalidate API keys issued with the new scopes. Operators can
-- revoke or replace those keys before applying this down migration.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM project_api_keys
    WHERE scopes && ARRAY['apps.read','apps.write']::text[]
  ) THEN
    RAISE EXCEPTION 'cannot roll back Apps API-key scopes while keys with Apps scopes remain';
  END IF;
END
$$;

DELETE FROM project_service_layouts WHERE resource_type='app';

ALTER TABLE project_service_layouts
  DROP CONSTRAINT project_service_layouts_type_check,
  ADD CONSTRAINT project_service_layouts_type_check
    CHECK (resource_type IN ('function','site','database','storage'));

DROP TABLE project_apps;
DROP TABLE platform_hostname_claims;

CREATE OR REPLACE FUNCTION stealth_api_key_scopes_canonical(input_values text[]) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  value text;
  previous text := '';
BEGIN
  IF input_values IS NULL OR cardinality(input_values) < 1 OR cardinality(input_values) > 15 OR array_position(input_values, NULL) IS NOT NULL THEN
    RETURN false;
  END IF;
  FOREACH value IN ARRAY input_values LOOP
    IF previous <> '' AND previous >= value THEN
      RETURN false;
    END IF;
    previous := value;
  END LOOP;
  RETURN true;
END;
$$;

ALTER TABLE project_api_keys
  DROP CONSTRAINT project_api_keys_scopes_supported,
  DROP CONSTRAINT project_api_keys_scopes_nonempty,
  DROP CONSTRAINT project_api_keys_scopes_canonical;

ALTER TABLE project_api_keys
  ADD CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY[
    'users.read','users.write',
    'databases.read','databases.write',
    'storage.read','storage.write',
    'functions.read','functions.write',
    'sites.read','sites.write',
    'webhooks.read','webhooks.write',
    'realtime.read',
    'messaging.read','messaging.write'
  ]::text[]),
  ADD CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 15),
  ADD CONSTRAINT project_api_keys_scopes_canonical CHECK (stealth_api_key_scopes_canonical(scopes));
