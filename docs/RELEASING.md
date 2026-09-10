# First-release checklist

Use this checklist for the first public release. The release workflow is
`.github/workflows/release.yml`; it accepts stable tags matching
`vMAJOR.MINOR.PATCH` and currently rejects prerelease tags.

## Repository and CI

- [ ] Merge the release source into the intended default branch before renaming it to `main`.
- [ ] Confirm the Apache-2.0 `LICENSE` and its copyright attribution are an intentional project-owner decision.
- [ ] Confirm CI is green, including Go checks, OpenAPI generation, Console checks, Docker builds, and E2E tests.
- [ ] Run the release preflight from the repository root:

  ```bash
  git status --short
  go test ./...
  go build ./cmd/stealth
  bash -n scripts/bootstrap.sh scripts/bootstrap_test.sh scripts/production-smoke.sh scripts/compose-production-smoke.sh
  ./scripts/bootstrap_test.sh
  docker compose --env-file .env.production.example -f compose.production.yaml config --quiet
  ```

## Tag and verify

- [ ] Confirm the release version is `v0.1.0` (or update the workflow and CLI validators before using a prerelease).
- [ ] Create and push the tag only from the reviewed release commit:

  ```bash
  git tag v0.1.0
  git push origin v0.1.0
  ```

- [ ] Verify the GitHub Release contains `stealth_Linux_x86_64.tar.gz`, `stealth_Linux_arm64.tar.gz`, and `checksums.txt`.
- [ ] Verify GHCR contains `ghcr.io/stealth-deplover/stealth-api:v0.1.0`, `ghcr.io/stealth-deplover/stealth-worker:v0.1.0`, `ghcr.io/stealth-deplover/stealth-migrate:v0.1.0`, and `ghcr.io/stealth-deplover/stealth-console:v0.1.0`.

## Clean-host smoke test

- [ ] On clean Linux amd64, and arm64 when available, run:

  ```bash
  curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/HEAD/scripts/bootstrap.sh | sh
  stealth install
  stealth status
  stealth doctor
  ```

  The bootstrap normally hands off to `stealth install`; run it explicitly
  when using the inspect-first download flow. Verify the Console URL, API
  health/readiness, migrations, and proxy routing.

- [ ] Exercise the repair path with `stealth install --repair` on a test installation.
- [ ] Follow [`upgrade.md`](upgrade.md) for upgrade, migration, backup, and rollback verification.
- [ ] Record known limitations: no CLI upgrade/uninstall commands, no HA or exactly-once guarantee, and Agent provider execution remains queue-only by default.

## GitHub settings

- [ ] About description: **Open-source developer cloud for apps, functions, databases, storage, messaging, and AI agents.**
- [ ] Topics: `self-hosted`, `developer-platform`, `paas`, `cloud`, `go`, `nextjs`, `postgresql`, `redis`, `docker`, `openapi`.
- [ ] Rename the default branch to `main` only after the source branch is merged; update branch protection and any external integrations.
- [ ] Add a 1280×640 social preview with minimal branding, readable small text, and no sensitive screenshots.
- [ ] Add a real website/docs URL only when one exists. Enable Discussions only if maintainers intend to support community Q&A.

## Screenshots and social preview

No screenshots are committed yet. Future redacted product captures should live
under `docs/assets/`, use consistent dimensions such as 1600×900, and follow
names such as `dashboard.png`, `functions-deployment.png`,
`database-storage.png`, and `observability.png`. Add an `agents-runtime.png`
capture only when provider execution is representative. Do not include secrets,
private tenant data, or unfinished placeholder blocks in the README.

## License and attribution

The root `LICENSE` is Apache-2.0. The current history shows it was introduced
with the repository-polish work rather than an earlier recorded license
decision, so maintainers should retain a clear project-owner approval and
confirm the copyright holder before publishing the first release. No `NOTICE`
file or vendored third-party source is currently present; review dependency and
asset attribution requirements before distributing bundled artifacts.
