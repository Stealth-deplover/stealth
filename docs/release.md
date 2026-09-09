# Release engineering

Releases use tags matching `vMAJOR.MINOR.PATCH`, for example `v1.2.3`.
Pushing such a tag starts `.github/workflows/release.yml`, which:

1. validates Go and Console source checks;
2. builds API, worker, migration, and standalone Console images;
3. stamps OCI labels and Go build metadata with the tag, commit, and UTC build
   time;
4. pushes version and full-commit `sha-<commit sha>` tags to GHCR; and
5. creates a GitHub Release with migration, configuration, upgrade, and known
   limitation headings.

The workflow uses `GITHUB_TOKEN` with `contents: write` and `packages: write`
only for the release job. It does not put registry credentials or application
secrets in image layers. Production should pin one release across API, worker,
migrate, and Console; `latest` is not used by the deployment documentation.

The Console image is Next.js `output: "standalone"` and contains only the
standalone runtime plus static/public assets. It is not a Vercel deployment.
