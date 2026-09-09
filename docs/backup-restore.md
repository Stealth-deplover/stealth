# Backup and restore runbook

This runbook documents operator responsibilities. Stealth does not provide an
automatic disaster-recovery service merely because the Compose stack has
persistent volumes.

## PostgreSQL

- Use provider-native snapshots or `pg_dump`/`pg_dumpall` on a schedule that
  matches the data-loss tolerance of the installation.
- Store backups outside the host running the Compose volumes and protect them
  with encryption and access controls.
- Record the schema/application release that produced each backup.
- Periodically restore a copy into an isolated PostgreSQL instance, run the
  migration check, start Stealth against it, and verify organizations,
  projects, resources, sessions, and queue rows.

For a logical backup, a representative command is:

```bash
pg_dump --format=custom --file=stealth-$(date -u +%Y%m%dT%H%M%SZ).dump "$DATABASE_URL"
```

Use the matching `pg_restore` procedure in an isolated database. Do not run a
restore over the live database without an explicit maintenance plan.

## Object storage

When `STORAGE_DRIVER=s3`, enable the provider's versioning, replication, or
backup policy and test restoring both object data and metadata. When
`STORAGE_DRIVER=local`, back up the `stealth_storage` volume together with the
database; a host-volume copy alone is not a high-availability design.

The function/site artifacts and database rows must be restored as a coherent
set. A backup that has never passed a restore verification is not a DR plan.
