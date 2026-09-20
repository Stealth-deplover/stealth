# Upgrade and rollback

Stealth releases are coordinated application releases. Use the same version
for `stealth-api`, `stealth-worker`, `stealth-migrate`, `stealth-console`,
`stealth-otel-collector`, `stealth-otel-docker-logs`, and
`stealth-telemetry-docker-proxy`. The host-metrics and Docker-metrics
collectors use the capability-free `stealth-otel-collector` image; the
Docker-log collector uses the dedicated `stealth-otel-docker-logs` image.
The `stealth-setup` image is only needed for a fresh browser installation or
setup repair.
The current deployment does not promise rolling upgrades between incompatible
API/worker/schema versions.

## Go module identity

The canonical Go module path is now `github.com/Stealth-deplover/stealth`.
Consumers of the previous `github.com/nazxf/stealth-api` module must update
their imports and `go.mod` requirements; Go does not automatically treat the
two paths as the same module. No compatibility `replace` directive or
forwarding packages are provided.

## Upgrade procedure

Use the installed host CLI for a coordinated update:

```bash
stealth update
```

When an installation is present, `stealth update` validates the target release
assets, migrates the installation, pulls the target images, runs the existing
database/telemetry state initialization and migrations, recreates the
production services, and performs the normal health checks before replacing
the CLI binary. On a host without an installation it remains a CLI-only
self-update.

`stealth install --repair` performs the same managed-asset preparation for the
currently installed release and is the supported recovery path for a missing
or damaged runtime asset.

The host CLI treats these repository-controlled files as release-managed:

- `compose.production.yaml` and `compose.setup.yaml` when setup assets are present;
- `telemetry/otel-collector.yaml`, `telemetry/host-metrics.yaml`,
  `telemetry/docker-logs.yaml`, and `telemetry/docker-stats.yaml`;
- `console/deploy/nginx.conf`.

The complete target set is downloaded and validated before activation. Existing
managed files are replaced atomically, with one bounded previous-release set
under `state/managed-assets.previous` for recovery/debugging. Unknown files in
the installation root and telemetry directory are preserved. Direct edits to
release-managed files are not an override mechanism and may be replaced by a
supported update or repair.

`config.env` is operator state. Its existing values, including generated
secrets and custom image references, are preserved; only missing release keys
are added, and canonical Stealth image references advance with the installed
release. Persistent database, object-storage, and Collector state volumes are
not deleted or recreated by this migration. External PostgreSQL/Redis settings
remain external and are not replaced with bundled services.

Compose configuration is validated before image pulls or service recreation.
If target assets cannot be downloaded/validated, or Compose rejects them, the
active files, `config.env`, and `VERSION` remain at the previous release. A
later service or migration failure leaves a coherent prepared asset set and can
be retried with `stealth install --repair`; this process does not promise
zero-downtime upgrades or automatic database rollback.

For an operator-managed checkout, the manual platform procedure below remains
available. Installed deployments using the host CLI should use the coordinated
command so managed assets and the runtime are migrated as one lifecycle.

## Manual platform upgrade

1. Read the GitHub Release notes for the target version, especially migration
   and configuration changes.
2. Verify PostgreSQL and object-storage backups and record where the restore
   artifacts are stored. See [`backup-restore.md`](backup-restore.md).
3. Update all production image variables in `.env.production` to the same
   immutable release tag or digest. Keep the setup image on the same release
   when fresh browser setup or repair may be used.
4. Pull the images and validate the rendered Compose file.
5. Stop or coordinate workers if the release notes require a quiet queue.
6. Start PostgreSQL/Redis if needed, then run the one-shot migration command.
7. Recreate API, worker, Console, proxy, the main Collector, host-metrics
   Collector, Docker-log Collector, Docker-metrics Collector, and proxy from
   the same release configuration.
8. Verify `/healthz`, `/readyz`, `/version`, worker health, and the HTTP smoke
   script. Check logs for migration and worker claim errors.

```bash
docker compose --env-file .env.production -f compose.production.yaml pull
docker compose --env-file .env.production -f compose.production.yaml up -d postgres redis clickhouse
docker compose --env-file .env.production -f compose.production.yaml up migrate
docker compose --env-file .env.production -f compose.production.yaml up -d --force-recreate api worker console proxy otel-collector telemetry-host telemetry-docker-logs telemetry-docker-proxy telemetry-docker
./scripts/production-smoke.sh
```

The migration runner is deterministic and protected by a PostgreSQL advisory
lock. It fails loudly; it does not perform destructive automatic rollback.

When upgrading an installation that already has accounts, migration seals
public first-owner bootstrap as `legacy_installation` without assigning an
arbitrary account the privileged `instance_owner` role. This prevents an
existing deployment from unexpectedly exposing `/setup`. After the API is
healthy, a local operator may review and explicitly adopt an account:

```bash
stealth setup --adopt-owner
```

The command requires the dedicated `BOOTSTRAP_CLI_KEY`, lists existing account
identities locally, and requires the exact `ADOPT <account-id>` confirmation.
The assignment is audited and does not grant organization membership. Add the
dedicated `BOOTSTRAP_CLI_KEY` and `GITHUB_APP_CLIENT_ID` to `config.env` before
starting an upgraded API; neither falls back to `FUNCTIONS_SECRET_KEY`.
Stop or coordinate the old application processes before applying migrations.

## Rollback boundary

Changing an image back is safe only when the database schema remains backward
compatible with that application version. After an incompatible or
irreversible migration, application rollback may require restoring PostgreSQL
and object storage from verified backups before starting the older release.
Do not claim a database rollback merely because an older image is available.

If the migration has not changed schema compatibility and the issue is limited
to application code, pin the production images back to the previous release,
run the smoke checks, and inspect worker leases before resuming traffic.
