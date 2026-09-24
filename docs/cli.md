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
Console, and proxy after the one-time handoff. The production API, Console,
BuildKit, Traefik, and App containers do not receive the socket. The production
worker still mounts it for non-root Docker-backed build and persistent App
runtime work.

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
├── telemetry/             # versioned Collector configurations
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

The Compose, Collector, and proxy files are downloaded from the same versioned
Git tag as the CLI. The config pins API, setup, worker, ingress-control, migration, Console,
the capability-free Collector image, the dedicated Docker-log Collector image,
and the restricted telemetry Docker proxy image to the same GHCR release tag.

An existing `config.env` is operator state: supported update and repair paths
preserve its secrets and custom values while adding missing release keys. The
Compose, Collector, proxy, and telemetry files are release-managed artifacts;
supported update and repair replace them atomically with the target release
after validation. Direct edits to those managed files may therefore be
replaced. A bounded previous managed-asset set is kept under `state/` for
recovery/debugging, while persistent database, storage, and Collector state
volumes are preserved. Unknown local files are not recursively removed.

An ordinary fresh install still refuses to overwrite an existing installation.
After a partial preparation or Compose-validation failure, the active files,
`config.env`, and `VERSION` remain recoverable; use `stealth doctor` or
`stealth install --repair` for the next attempt. The recovery journal remains
active through managed-asset activation, `config.env`, `VERSION`, and Compose
validation; it stores no secret values. The coordinated `stealth update` path
uses the checksum-verified target binary to perform the target release's
migration before replacing the installed CLI binary.

## Operations

```bash
stealth version
stealth status
stealth doctor
stealth logs
stealth logs api
stealth logs worker --follow
stealth logs console
stealth ingress status
stealth ingress verify
stealth ingress cutover
stealth ingress rollback
```

`status` reads Compose service state and prints the configured Console URL.
`doctor` is read-only and checks Docker, Compose, private configuration,
service health, API health/readiness/version endpoints, Console/proxy HTTP
reachability, available disk space, and the App runtime network's local bridge
driver and Stealth ownership labels. It does not create the network or start an
App. In setup mode the runtime check is skipped. `logs` delegates to
`docker compose logs`; it does not build a log storage subsystem.

`stealth ingress status` reports the saved Cloudflare Console origin state.
`stealth ingress verify` is read-only and checks the provider's current Tunnel
configuration, local Traefik routes, public Console HTTPS behavior, browser
security headers, and HSTS. Console routes may use up to five safe HTTPS
redirects on the configured hostname and port; `/` normally redirects to
`/organizations`. Every redirect response must retain the required security
headers and HSTS. Add `--site-hostname portfolio.apps.example.com`
to verify a platform Site below the configured workload domain;
`--site-sha256` can require a deterministic body digest.

`stealth ingress cutover` explicitly changes the Console rule in the existing
Cloudflare Named Tunnel from `proxy`/Nginx to `traefik`. The host checks
production service health, local route behavior, and public HTTPS before the
provider change. If post-cutover verification fails, it requests Nginx again,
reconciles the existing tunnel, and verifies public recovery. If automatic
rollback cannot be verified, run `stealth ingress rollback` from the host.
Manual rollback uses the origin-only one-shot maintenance operation, preserves
the workload wildcard and catch-all, and does not call workload DNS or
certificate APIs. It preflights healthy local Nginx, running Cloudflared, and
healthy bundled PostgreSQL when applicable; it does not require public API,
Console, or Traefik health. Neither command removes Nginx or changes Cloudflare
HSTS settings. Existing installations default to Nginx and updates do not
change their desired origin.

To update the installed Stealth CLI to the latest stable GitHub Release:

```bash
stealth update
```

On a host without an installation, the command downloads only the Linux
amd64/arm64 CLI archive for the running platform, verifies its entry in
`checksums.txt`, validates the extracted binary, and replaces the installed
CLI with an atomic file swap. For an existing installation, the verified target
CLI runs the coordinated release migration described in
[`upgrade.md`](upgrade.md): release-managed Compose, Collector, proxy, and
telemetry assets are validated and updated, while `config.env`, secrets,
unknown files, and persistent state are preserved. A failed download,
checksum, asset preparation, Compose validation, or replacement leaves the
installation recoverable. If the platform migration succeeds but executable
replacement does not, the command reports the resulting CLI/platform skew and
the next `stealth update` reconciles it. Development builds can use `stealth
update --check` to inspect availability, but a release build is recommended
for self-update.

`v0.2.5` predates this handoff. Its first update to the bridge release is
CLI-only by design; run the documented second `stealth update` (or `stealth
install --repair`) with the bridge binary to migrate the existing stack. See
the exact bridge-release procedure in [`upgrade.md`](upgrade.md); do not infer
that a single v0.2.5 update has changed old production assets.

If the stable release has already advanced beyond the bridge, acquire the
bridge archive and `checksums.txt`, verify the selected archive entry, and
replace only the existing CLI executable with the extracted `stealth` file.
Do not run the normal fresh-install bootstrap for this step: an existing root
must remain untouched until the bridge CLI runs `stealth update`.

Use `stealth update --check` for a network-only check; it exits non-zero when
an update is available. The update source is the official stable release only:
drafts, prereleases, arbitrary URLs, and downgrades are rejected. If the
installation directory is not writable, rerun the command with the
appropriate system permissions; Stealth never invokes `sudo` or asks for its
password.

For an existing installation, `stealth update` also pulls/recreates the
versioned production stack, runs the normal migrations and health checks, and
updates the telemetry topology. It is the supported coordinated platform
upgrade path; it does not promise zero-downtime upgrades or automatic database
rollback. Traefik runtime-state ownership is prepared by the narrow,
one-shot `traefik-state-init` Compose service running inside Docker; a normal
user with a writable installation directory does not need `sudo` or a manual
`chown`.

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
--remove-orphans`; it never passes `--volumes`. It preserves persistent App
containers and their separately managed runtime network along with PostgreSQL
data, `stealth_storage`, function-runner staging, `config.env`, the Compose/proxy
assets, `VERSION`, and CLI recovery files. The middle interactive mode also
preserves App containers/network while removing the generated Compose,
proxy, `VERSION`, and `state/` runtime files, but deliberately keeps
`config.env`: it contains `FUNCTIONS_SECRET_KEY` and database credentials
needed to recover preserved encrypted data.

Purge first validates that the local Compose file declares exactly the
installation's configured named volumes and that any existing volumes carry
the matching Compose project labels. It also validates the runtime bridge's
Stealth labels and each persistent App container's schema, UUID ownership
labels, workload digest, and deterministic name. After stopping Compose
services, it stops and removes only those validated App container IDs and the
exact owned runtime bridge, then removes project-owned Compose volumes with
`docker compose down --volumes --remove-orphans`. It verifies the resources are
gone before removing local configuration and secrets. An unowned/conflicting
container or network stops purge before destructive cleanup. It never runs
`docker system prune` or `docker volume prune`.
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
