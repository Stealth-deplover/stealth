-- Realtime subscriptions are read-only project data access. Existing API keys
-- remain valid; this forward-only migration adds the independent scope without
-- rewriting any stored secret material.

ALTER TABLE project_api_keys
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_supported,
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_nonempty,
  DROP CONSTRAINT IF EXISTS project_api_keys_scopes_canonical;

ALTER TABLE project_api_keys
  ADD CONSTRAINT project_api_keys_scopes_supported CHECK (scopes <@ ARRAY[
    'users.read','users.write',
    'databases.read','databases.write',
    'storage.read','storage.write',
    'functions.read','functions.write',
    'sites.read','sites.write',
    'webhooks.read','webhooks.write',
    'realtime.read'
  ]::text[]),
  ADD CONSTRAINT project_api_keys_scopes_nonempty CHECK (cardinality(scopes) BETWEEN 1 AND 13),
  ADD CONSTRAINT project_api_keys_scopes_canonical CHECK (
    (cardinality(scopes) < 2 OR scopes[1] < scopes[2]) AND
    (cardinality(scopes) < 3 OR scopes[2] < scopes[3]) AND
    (cardinality(scopes) < 4 OR scopes[3] < scopes[4]) AND
    (cardinality(scopes) < 5 OR scopes[4] < scopes[5]) AND
    (cardinality(scopes) < 6 OR scopes[5] < scopes[6]) AND
    (cardinality(scopes) < 7 OR scopes[6] < scopes[7]) AND
    (cardinality(scopes) < 8 OR scopes[7] < scopes[8]) AND
    (cardinality(scopes) < 9 OR scopes[8] < scopes[9]) AND
    (cardinality(scopes) < 10 OR scopes[9] < scopes[10]) AND
    (cardinality(scopes) < 11 OR scopes[10] < scopes[11]) AND
    (cardinality(scopes) < 12 OR scopes[11] < scopes[12]) AND
    (cardinality(scopes) < 13 OR scopes[12] < scopes[13])
  );
