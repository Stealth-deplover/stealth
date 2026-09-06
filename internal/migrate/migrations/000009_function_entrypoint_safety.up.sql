-- Entrypoints are resolved inside a read-only runtime workspace. Keep them
-- relative and reject dot segments before a worker ever constructs a path.
ALTER TABLE project_functions
  DROP CONSTRAINT project_functions_entrypoint_valid,
  ADD CONSTRAINT project_functions_entrypoint_valid CHECK (
    char_length(entrypoint) BETWEEN 1 AND 255 AND
    entrypoint = btrim(entrypoint) AND
    entrypoint <> '.' AND entrypoint <> '..' AND
    left(entrypoint, 1) <> '/' AND
    position(chr(92) IN entrypoint) = 0 AND
    position(chr(13) IN entrypoint) = 0 AND position(chr(10) IN entrypoint) = 0 AND
    entrypoint !~ '(^|/)\.\.?(/|$)' AND
    entrypoint !~ '(^|/)/'
  );
