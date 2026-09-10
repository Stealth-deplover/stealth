# Stealth Documentation

This index links the repository’s existing guides. The root [README](../README.md) provides the product overview, current status, architecture, and development quick start.

## Getting Started

- [CLI and installer](cli.md) — installation flow, operator commands, and release artifacts.
- [Production deployment](production-deployment.md) — the supported Docker Compose baseline.

## Architecture and API

- [Architecture overview](../README.md#architecture) — Console, API, storage, queues, and workers.
- [OpenAPI contract](../openapi/openapi.yaml) — source of truth for Console requests.
- [Backend production-readiness contract](backend-production-readiness.md) — durable work, operational controls, and known guarantees.
- [Realtime event infrastructure](realtime.md) — outbox, SSE, Redis, and delivery behavior.

## Configuration and Self Hosting

- [Production configuration and proxy](production-deployment.md#configuration)
- [Proxy and cookies](production-deployment.md#proxy-and-cookies)
- [CLI installer flow](cli.md#installer-flow)
- [Console development](../console/README.md#development)

## Product Areas

The API contract is the detailed reference for each product surface. Operational behavior is documented in the backend readiness guide.

- [Functions](backend-production-readiness.md#durable-work-mapping)
- [Sites](backend-production-readiness.md#durable-work-mapping)
- [Database](../openapi/openapi.yaml) and [database backup/restore](backup-restore.md#postgresql)
- [Storage](../openapi/openapi.yaml) and [object storage backup/restore](backup-restore.md#object-storage)
- [Messaging and Webhooks](backend-production-readiness.md#durable-work-mapping)
- [Agents](../console/docs/backend-gaps.md#agent-task-execution)

## Security

- [Security policy](../SECURITY.md)
- [Backend security assumptions](backend-production-readiness.md#security-assumptions)
- [Console/backend capability boundaries](../console/docs/backend-gaps.md)

## Production Operations

- [Release engineering](release.md)
- [Upgrade and rollback](upgrade.md)
- [Backup and restore](backup-restore.md)
- [Production smoke checks](production-deployment.md#smoke-and-troubleshooting)

## Project Direction

- [Roadmap and implementation status](../README.md#roadmap)
- [Contributing](../CONTRIBUTING.md)
