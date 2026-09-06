-- Git-backed Site deployments retain the canonical public repository and ref
-- alongside the immutable downloaded source archive. Credentials are not
-- accepted in this milestone; only public GitHub/GitLab repositories are
-- allowed by the API parser.

ALTER TABLE site_deployments
  ADD COLUMN git_repository TEXT,
  ADD COLUMN git_ref TEXT;

ALTER TABLE site_deployments
  ADD CONSTRAINT site_deployments_git_metadata_valid CHECK (
    (
      source IN ('github','gitlab') AND
      git_repository IS NOT NULL AND
      git_repository = btrim(git_repository) AND
      char_length(git_repository) BETWEEN 1 AND 512 AND
      git_repository ~ '^https://(github\.com|gitlab\.com)/[^?#[:space:]]+$' AND
      git_ref IS NOT NULL AND
      git_ref = btrim(git_ref) AND
      char_length(git_ref) BETWEEN 1 AND 256 AND
      git_ref !~ '[[:space:]]' AND
      git_ref !~ '\.\.' AND
      git_ref !~ '//'
    ) OR (
      source NOT IN ('github','gitlab') AND
      git_repository IS NULL AND
      git_ref IS NULL
    )
  );

CREATE INDEX site_deployments_git_repository_idx
  ON site_deployments (git_repository, git_ref)
  WHERE git_repository IS NOT NULL;
