# Stealth CLI and installer

The official `stealth` CLI is a small Go binary that orchestrates the
versioned production Compose stack. Docker Compose remains the deployment
primitive; the CLI does not replace the Go API, worker, migration command, or
reverse proxy.

## Quick install

The current bootstrap entrypoint is hosted on GitHub Raw while the installer
stabilizes:

```bash
curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/init/backend-import/scripts/bootstrap.sh | sh
```

The bootstrap detects Linux amd64/arm64, resolves the latest stable SemVer
release (or `STEALTH_VERSION`), downloads the matching release archive and
`checksums.txt`, verifies SHA-256, and then starts `stealth install`. It does
not install Docker or run a large deployment script.

For an inspect-first install:

```bash
curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/init/backend-import/scripts/bootstrap.sh -o bootstrap.sh
less bootstrap.sh
sh bootstrap.sh
```

To pin a release:

```bash
STEALTH_VERSION=v0.1.0 \
  sh bootstrap.sh
```

GitHub Raw is a temporary distribution URL. A future `get.stealth.dev` entry
point may serve the same verified bootstrap without changing the CLI release
format.

## Installer flow

`stealth install` is an interactive Bubble Tea/Lip Gloss terminal flow:

1. system checks;
2. instance URL;
3. bundled PostgreSQL, Redis, and local object storage review;
4. local/existing reverse proxy review;
5. configuration review;
6. secret generation, image pull, migration, startup, and bounded HTTP health
   verification.

The installer requires Docker and Docker Compose. It does not silently run a
third-party Docker installation script. The first release uses bundled
infrastructure and the existing local storage volume. External S3-compatible
storage remains an operator configuration documented by the production
deployment guide.

The instance owner email/bootstrap claim is not a platform-admin feature yet;
the installer does not pretend that an email address grants global access.

The default installation directory is `~/.stealth`. Set
`STEALTH_INSTALL_DIR` to an absolute writable directory when a different
location is required. The CLI writes:

```text
~/.stealth/
├── config.env                 # generated secrets, mode 0600
├── compose.production.yaml
├── console/deploy/nginx.conf
├── VERSION
└── state/
```

The Compose and proxy files are downloaded from the same versioned Git tag as
the CLI. The config pins API, worker, migration, and Console images to the
same GHCR release tag.

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

Upgrade and uninstall commands are intentionally not shipped in this
milestone. Do not delete Docker volumes as a repair action. Use the documented
backup and upgrade runbooks for production changes.

## Secret and terminal safety

Secrets are generated with `crypto/rand`, written only to `config.env` with
mode `0600`, and are not printed or passed as command-line arguments. The
bootstrap verifies release archives before executing them. If a piped shell
does not have a controlling `/dev/tty`, it stops with a clear error instead of
hanging for input. `NO_COLOR` and `TERM=dumb` produce readable plain output.

The CLI release artifacts are:

```text
stealth_Linux_x86_64.tar.gz
stealth_Linux_arm64.tar.gz
checksums.txt
```

The release workflow builds both CLI archives with the same version, commit,
and UTC build metadata used by the container images, and publishes them only
after the existing production Compose HTTP smoke passes.
