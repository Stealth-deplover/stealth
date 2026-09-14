# Release engineering

For the end-to-end release and repository-settings checklist, see
[`RELEASING.md`](RELEASING.md).

Releases use stable tags matching `vMAJOR.MINOR.PATCH` (for example
`v1.2.3`) or explicitly numbered release-candidate tags matching
`vMAJOR.MINOR.PATCH-rc.N` (for example `v0.3.0-rc.1`). Pushing either tag
starts `.github/workflows/release.yml`. RC releases are marked as GitHub
pre-releases and do not replace the latest stable release. Automatic bootstrap
resolution and `stealth update` use stable releases only; an RC must be pinned
explicitly with `STEALTH_VERSION` or `--version`.

The workflow:

1. validates Go and Console source checks;
2. builds API, setup, worker, migration, and standalone Console images;
3. stamps OCI labels and Go build metadata with the tag, commit, and UTC build
   time;
4. builds Linux amd64 and arm64 `stealth` CLI archives and a `checksums.txt`
   file;
5. pushes version and 12-character `sha-<short sha>` tags to GHCR; and
6. creates a stable or pre-release GitHub Release with the CLI archives, checksums, migration,
   configuration, upgrade, and known limitation headings.

The workflow uses `GITHUB_TOKEN` with `contents: write` and `packages: write`
only for the publishing jobs. It does not put registry credentials or
application secrets in image layers. Production should pin one release across
API, worker, migrate, and Console. The setup image is used only by fresh
browser setup or setup repair. `latest` is not used by the deployment
documentation.

The CLI archives are published as `stealth_Linux_x86_64.tar.gz` and
`stealth_Linux_arm64.tar.gz`. The bootstrap verifies the corresponding
`checksums.txt` entry before executing the binary. Container publication and
the GitHub Release both depend on the production Compose HTTP smoke job.

The Console image is Next.js `output: "standalone"` and contains only the
standalone runtime plus static/public assets. It is not a Vercel deployment.
