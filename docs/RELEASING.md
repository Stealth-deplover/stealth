# Release checklist

Use this checklist for each public release. The release workflow is
`.github/workflows/release.yml`; it accepts stable tags matching
`vMAJOR.MINOR.PATCH` and rejects prerelease tags.

## Repository and CI

- [ ] Confirm the reviewed source is on the intended default branch and the
  worktree is clean.
- [ ] Confirm the Apache-2.0 `LICENSE` and its copyright attribution remain an
  intentional project-owner decision.
- [ ] Confirm CI is green, including Go checks, OpenAPI generation, Console
  checks, Docker builds, and E2E tests.
- [ ] Run the release preflight from the repository root:

  ```bash
  git status --short
  go mod verify
  go vet ./...
  go test ./...
  go build -o /tmp/stealth-api ./cmd/api
  go build -o /tmp/stealth-worker ./cmd/worker
  go build -o /tmp/stealth ./cmd/stealth
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/stealth-linux-amd64 ./cmd/stealth
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/stealth-linux-arm64 ./cmd/stealth
  bash -n scripts/bootstrap.sh scripts/bootstrap_test.sh scripts/production-smoke.sh scripts/compose-production-smoke.sh
  ./scripts/bootstrap_test.sh
  docker compose --env-file .env.production.example -f compose.production.yaml config --quiet
  ```

## Tag and release artifacts

- [ ] Confirm the release version is a stable `vMAJOR.MINOR.PATCH` tag.
- [ ] Create and push the tag only from the reviewed release commit:

  ```bash
  version=v1.2.3
  git tag "$version"
  git push origin "$version"
  ```

- [ ] Confirm the release workflow completes successfully.
- [ ] Verify the GitHub Release contains `stealth_Linux_x86_64.tar.gz`,
  `stealth_Linux_arm64.tar.gz`, and `checksums.txt`.
- [ ] Verify `checksums.txt` contains a valid SHA-256 entry for both CLI
  archives and that the archive contents contain an executable `stealth` file.
- [ ] Verify GHCR contains versioned API, worker, migration, and Console
  images for the release.

## Clean-host validation

The release workflow's `Release installer smoke` job runs on a fresh
`ubuntu-latest` runner after publication. It validates latest-release
resolution, archive download, mandatory checksum verification, CLI execution,
and the release-tagged Compose configuration. It intentionally stops at the
non-interactive TTY handoff and does not run a full production stack.

- [ ] On a clean Linux amd64 host, run the published bootstrap entrypoint:

  ```bash
  curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/HEAD/scripts/bootstrap.sh | sh
  ```

  On an interactive terminal, the bootstrap hands off to `stealth install`.
  When the CLI was downloaded or installed without that handoff, run:

  ```bash
  stealth install
  stealth status
  stealth doctor
  ```

- [ ] Confirm `stealth install` pulls the matching versioned images, applies
  migrations, starts all services, and retains private configuration and
  persistent volumes on failure.
- [ ] Verify API `/healthz` and `/readyz`, Console availability, proxy routing,
  and the reported version metadata.
- [ ] Restart the API, worker, Console, and proxy containers, then rerun
  `stealth status` and `stealth doctor`.
- [ ] Exercise `stealth install --repair` on a test installation and confirm
  existing configuration, secrets, and volumes are preserved.
- [ ] Run the documented upgrade path in [`upgrade.md`](upgrade.md), including
  backups, migrations, coordinated restart, and post-upgrade health checks.
- [ ] Verify rollback expectations: only roll application images back when the
  database schema remains compatible; otherwise restore verified PostgreSQL
  and object-storage backups before restarting the older release.
- [ ] Repeat the clean-host flow on Linux arm64 when suitable infrastructure is
  available, and record when arm64 was not tested.

## Known limitations

- [ ] Record that CLI upgrade and uninstall commands are not shipped.
- [ ] Record that the bundled single-host deployment does not claim HA or
  exactly-once external side effects.
- [ ] Record that Agent provider execution remains queue-only by default.
- [ ] Record that full clean-host install validation is separate from unit,
  integration, and release-artifact checks.

## GitHub security settings

Verify these repository settings manually; `.github/dependabot.yml` configures
version-update behavior but cannot enable account or repository security
features by itself:

- [ ] Dependency graph is enabled.
- [ ] Dependabot alerts are enabled.
- [ ] Dependabot security updates are enabled.
- [ ] Dependabot version updates are enabled.
- [ ] Code scanning is enabled and CodeQL results are being uploaded.

## Repository settings

- [ ] About description: **Open-source developer cloud for apps, functions, databases, storage, messaging, and AI agents.**
- [ ] Topics: `self-hosted`, `developer-platform`, `paas`, `cloud`, `go`, `nextjs`, `postgresql`, `redis`, `docker`, `openapi`.
- [ ] Branch protection and required checks cover `main` and the release workflow.
- [ ] Add a 1280×640 social preview with minimal branding, readable small text,
  and no sensitive screenshots.
- [ ] Add a real website/docs URL only when one exists. Enable Discussions only
  if maintainers intend to support community Q&A.

## Screenshots and social preview

No screenshots are committed yet. Future redacted product captures should live
under `docs/assets/`, use consistent dimensions such as 1600×900, and follow
names such as `dashboard.png`, `functions-deployment.png`,
`database-storage.png`, and `observability.png`. Add an `agents-runtime.png`
capture only when provider execution is representative. Do not include secrets,
private tenant data, or unfinished placeholder blocks in the README.

## License and attribution

The root `LICENSE` is Apache-2.0. Maintainers should retain a clear project-owner
approval and confirm the copyright holder before distributing future artifacts.
No `NOTICE` file or vendored third-party source is currently present; review
dependency and asset attribution requirements before distributing bundled
artifacts.
