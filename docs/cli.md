# Stealth CLI and installer

The official `stealth` CLI is a small Go binary that orchestrates the
versioned production Compose stack. Docker Compose remains the deployment
primitive; the CLI does not replace the Go API, worker, migration command, or
reverse proxy.

## Quick install

The bootstrap entrypoint is hosted on GitHub Raw through the repository's
`HEAD` reference, so it follows the current default branch:

```bash
curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/HEAD/scripts/bootstrap.sh | sh
```

The bootstrap detects Linux amd64/arm64, resolves the latest stable SemVer
release (or `STEALTH_VERSION`), downloads the matching release archive and
`checksums.txt`, verifies SHA-256, and then starts `stealth install`. It does
not install Docker or run a large deployment script.

For an inspect-first install:

```bash
curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/HEAD/scripts/bootstrap.sh -o bootstrap.sh
less bootstrap.sh
sh bootstrap.sh
```

To pin a release:

```bash
STEALTH_VERSION=v0.2.2 \
  sh bootstrap.sh
```

To test a release candidate, pin it explicitly; the unpinned bootstrap path
and `stealth update` never select prereleases automatically:

```bash
STEALTH_VERSION=v0.3.0-rc.1 \
  sh bootstrap.sh
```

This is a repository distribution URL, not a separate product endpoint. It
will continue to follow a future default-branch rename without a branch name
in documentation. The script will not complete until a matching GitHub
Release contains the versioned CLI archive and `checksums.txt`.

## Installer flow

`stealth install` performs the local system checks, generates private setup
configuration, downloads the versioned Compose assets, starts the setup
Compose project, and stays alive while the browser wizard runs. The setup
project contains the setup API, setup Console, setup proxy, PostgreSQL, and
Redis. It does not collect provider credentials in the terminal. The browser
wizard owns the reviewed configuration, while the host CLI remains the only
production installation executor and calls the reusable Go install engine.

The CLI prints the setup URL and code, then waits for the browser to submit the
reviewed configuration. The host process must stay attached for fresh browser
setup: `stealth install --wait` and the default invocation both observe
`install_requested` and execute production installation. `--no-wait` is
rejected because this release has no separate host supervisor. The
`STEALTH_INSTALL_WAIT` environment setting cannot disable this safety rule.
After the browser commits installation, the request is stored in encrypted
resumable state; closing the browser does not cancel the host operation.

The temporary setup image has no Docker socket, Docker CLI, or Compose plugin.
The host CLI writes the finalized production configuration, runs Docker Compose,
performs health checks, owns Quick Tunnel cleanup, and removes the setup API,
Console, and proxy after the one-time handoff. The production API and Console
images do not receive the socket. The production worker still mounts it for the
existing non-root Docker-backed function and site runner.

See the [browser setup guide](web-setup.md) for the complete wizard, provider
connection, external infrastructure, and recovery behavior. The owner is an
instance-level role, separate from organization membership and organization
ownership; it does not implicitly grant access to every organization or
project.

The default installation directory is `~/.stealth`. Set
`STEALTH_INSTALL_DIR` to an absolute writable directory when a different
location is required. The CLI writes:

```text
~/.stealth/
├── config.env                 # generated secrets, mode 0600
├── compose.production.yaml
├── compose.setup.yaml
├── console/deploy/nginx.conf
├── VERSION
└── state/
```

`config.env` contains the local setup proof and the release settings:

- `BOOTSTRAP_CLI_KEY`, a private 32-byte key used only to authenticate the
  local CLI and encrypt short-lived GitHub browser authorization state;
- `GITHUB_APP_CLIENT_ID`, which is empty during browser setup and is filled
  from the server-owned GitHub App connection before production handoff.

Keep both values with the rest of `config.env`. The bootstrap key is never
printed or sent in the setup URL. `FUNCTIONS_SECRET_KEY` is a separate security
domain and is never used as a bootstrap-key fallback.

The Compose and proxy files are downloaded from the same versioned Git tag as
the CLI. The config pins API, setup, worker, migration, Console, and the
restricted telemetry Docker proxy images to the same GHCR release tag.

An existing `config.env` or `VERSION` is never replaced by a normal reinstall.
The command refuses to proceed and leaves volumes untouched. After a partial
failure, use `stealth doctor`; an explicit `stealth install --repair` reuses
the existing private configuration and does not regenerate secrets.

## Operations

```bash
stealth version
stealth status
stealth doctor
stealth logs
stealth logs api
stealth logs worker --follow
stealth logs console
```

`status` reads Compose service state and prints the configured Console URL.
`doctor` is read-only and checks Docker, Compose, private configuration,
service health, API health/readiness/version endpoints, Console/proxy HTTP
reachability, and available disk space. `logs` delegates to
`docker compose logs`; it does not build a log storage subsystem.

To update the installed Stealth CLI to the latest stable GitHub Release:

```bash
stealth update
```

The command downloads only the Linux amd64/arm64 CLI archive for the running
platform, verifies its entry in `checksums.txt`, validates the extracted
binary, and replaces the installed CLI with an atomic file swap. A failed
download, checksum, extraction, or replacement leaves the current CLI
untouched. Development builds can use `stealth update --check` to inspect
availability, but a release build is recommended for self-update.

Use `stealth update --check` for a network-only check; it exits non-zero when
an update is available. The update source is the official stable release only:
drafts, prereleases, arbitrary URLs, and downgrades are rejected. If the
installation directory is not writable, rerun the command with the
appropriate system permissions; Stealth never invokes `sudo` or asks for its
password.

`stealth update` updates the CLI binary only. It does not pull or restart API,
worker, Console, PostgreSQL, Redis, or proxy containers, apply migrations, or
upgrade the running Stealth server stack. Use the production upgrade runbook
for coordinated platform changes.

## First-run Instance Owner setup

On a new installation, `stealth install` starts the separate setup Compose
project and displays a one-time code plus an immutable-digest-pinned
`cloudflare/cloudflared:2026.9.0` Quick Tunnel when the Docker network is
available:

```text
https://random-name.trycloudflare.com/setup
STEALTH-XXXX-XXXX-XXXX
```

The code has 60 bits of cryptographic entropy, expires after 15 minutes, is
rate-limited, is stored by the API only as a SHA-256 hash, and is invalidated
after the first successful owner creation. Open the URL and complete the
[browser setup wizard](web-setup.md). The wizard connects GitHub through the
Manifest plus browser Web Application Flow, selects and tests infrastructure,
creates the named production Cloudflare Tunnel, and streams the shared install
engine's progress. The random TryCloudflare hostname is used only as the
short-lived setup callback configured in the per-installation Manifest; it is
never used as a redirect for a shared OAuth client. Neither the setup code,
GitHub authorization code, PKCE verifier, nor access token is placed in
browser storage or logs. If a Quick Tunnel cannot be started, the CLI leaves
the installation intact and shows the local setup URL instead.

The browser Install action first persists an `install_requested` phase. This
phase closes browser-owned configuration updates before the host CLI claims the
run as `installing`. A second click returns the existing run. If
Ctrl+C is pressed before the request, the waiting CLI removes the temporary
setup services and tunnel after rechecking state. After the request, it keeps
the installation state resumable and prints `stealth install --repair --wait`
as the recovery command.

The cloudflared image pin is defined in `internal/cli/setup.go` so it can be
reviewed and updated as one change. It currently pins the multi-architecture
`2026.9.0` manifest to
`sha256:ff69a2225ad7c6f85ed84fbd5f3087df46202426b2388ec60214098e0adf05e9`.
Maintainers should update the version and digest together after verifying the
official image manifest. The Quick Tunnel command uses the existing Compose
network and targets only the bundled `proxy` service.

To resume onboarding after cancellation or expiration, use:

```bash
stealth setup
```

The command creates a fresh setup session only while no Instance Owner exists.
After the owner is created, the backend permanently seals bootstrap, including
across API restarts. The temporary tunnel is closed and removed immediately
after completion. A Quick Tunnel is an onboarding transport only: it is
temporary, has no production SLA, and must not be treated as permanent
ingress. Configure a reverse proxy or production Cloudflare Tunnel separately.

Existing installations are not reopened during upgrade and no account is
automatically promoted. Migration seals public bootstrap as a
`legacy_installation`. A local operator can explicitly select an existing
account with a strong confirmation:

```bash
stealth setup --adopt-owner
```

This path uses only the dedicated local CLI proof, records an audit event, and
does not change organization membership. It is not available from `/setup`.

For instance removal, use the documented backup and upgrade runbooks. Do not
delete Docker volumes as a repair action.

## Uninstall

`stealth uninstall` is one guided command with three removal levels. In a
terminal it opens the same Bubble Tea/Lip Gloss style used by the installer and
shows a removal plan before making changes.

```bash
# Interactive guided flow
stealth uninstall

# Stop and remove services while preserving data and configuration
stealth uninstall --keep-data --yes

# Preview the destructive scope without changing anything
stealth uninstall --purge --dry-run

# Permanently remove the instance-owned data after explicit automation consent
stealth uninstall --purge --yes
```

The safest mode runs the equivalent of `docker compose down
--remove-orphans`; it never passes `--volumes`. PostgreSQL data,
`stealth_storage`, function-runner staging, `config.env`, the Compose/proxy
assets, `VERSION`, and CLI recovery files remain available for reinstall or
recovery. The middle interactive mode also removes the generated Compose,
proxy, `VERSION`, and `state/` runtime files, but deliberately keeps
`config.env`: it contains `FUNCTIONS_SECRET_KEY` and database credentials
needed to recover preserved encrypted data.

Purge first validates that the local Compose file declares exactly the
installation's configured named volumes and that any existing volumes carry
the matching Compose project labels. It then removes the services and those
project-owned volumes with `docker compose down --volumes --remove-orphans`,
verifies the resources are gone, and only then removes the local configuration
and secrets. It never runs `docker system prune` or `docker volume prune`.
External S3 object storage is not deleted because the CLI cannot safely prove
ownership of a bucket or prefix; remove it separately with provider tooling
after verifying the scope. Redis has no persistent volume in the bundled
Compose baseline. Short-lived function/site build and execution volumes are
normally removed by the worker; uninstall does not sweep ambiguous leftover
volumes that lack this instance's Compose ownership label.

Interactive purge requires typing the exact word `stealth`. In non-TTY mode,
an explicit mode and `--yes` are required; `--yes` by itself always selects
the non-destructive service-removal mode and never implies purge. A missing or
partial installation is reported clearly, and purge stops without deleting
data until the complete layout can be validated. The CLI binary is never
removed automatically.

## Secret and terminal safety

Secrets are generated with `crypto/rand`, written only to `config.env` with
mode `0600`, and are not printed or passed as command-line arguments. The
bootstrap verifies release archives before executing them. It does not require
a controlling `/dev/tty` because fresh setup choices are completed in the
browser; `NO_COLOR` and `TERM=dumb` produce readable plain output.

The CLI release artifacts are:

```text
stealth_Linux_x86_64.tar.gz
stealth_Linux_arm64.tar.gz
checksums.txt
```

The release workflow builds both CLI archives with the same version, commit,
and UTC build metadata used by the container images, and publishes them only
after the existing production Compose HTTP smoke passes.
