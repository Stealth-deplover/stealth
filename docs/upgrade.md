# Upgrade and rollback

Stealth releases are coordinated application releases. Use the same version
for `stealth-api`, `stealth-worker`, `stealth-migrate`, and `stealth-console`.
The current deployment does not promise rolling upgrades between incompatible
API/worker/schema versions.

## Go module identity

The canonical Go module path is now `github.com/Stealth-deplover/stealth`.
Consumers of the previous `github.com/nazxf/stealth-api` module must update
their imports and `go.mod` requirements; Go does not automatically treat the
two paths as the same module. No compatibility `replace` directive or
forwarding packages are provided.

## Upgrade procedure

1. Read the GitHub Release notes for the target version, especially migration
   and configuration changes.
2. Verify PostgreSQL and object-storage backups and record where the restore
   artifacts are stored. See [`backup-restore.md`](backup-restore.md).
3. Update all four image variables in `.env.production` to the same immutable
   release tag or digest.
4. Pull the images and validate the rendered Compose file.
5. Stop or coordinate workers if the release notes require a quiet queue.
6. Start PostgreSQL/Redis if needed, then run the one-shot migration command.
7. Recreate API, worker, Console, and proxy from the same release.
8. Verify `/healthz`, `/readyz`, `/version`, worker health, and the HTTP smoke
   script. Check logs for migration and worker claim errors.

```bash
docker compose --env-file .env.production -f compose.production.yaml pull
docker compose --env-file .env.production -f compose.production.yaml up -d postgres redis
docker compose --env-file .env.production -f compose.production.yaml up migrate
docker compose --env-file .env.production -f compose.production.yaml up -d --force-recreate api worker console proxy
./scripts/production-smoke.sh
```

The migration runner is deterministic and protected by a PostgreSQL advisory
lock. It fails loudly; it does not perform destructive automatic rollback.

## Rollback boundary

Changing an image back is safe only when the database schema remains backward
compatible with that application version. After an incompatible or
irreversible migration, application rollback may require restoring PostgreSQL
and object storage from verified backups before starting the older release.
Do not claim a database rollback merely because an older image is available.

If the migration has not changed schema compatibility and the issue is limited
to application code, pin all four images back to the previous release, run the
smoke checks, and inspect worker leases before resuming traffic.
