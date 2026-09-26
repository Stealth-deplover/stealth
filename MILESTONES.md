# Stealth v1.0 GA Milestones

> Persistent tracker for humans, Codex, and future reviewers.
>
> Target: **Stealth v1.0 GA — production-grade self-hosted developer cloud**.

## Rules

1. Read this tracker before starting milestone work.
2. Fetch the latest `main` and inspect source before editing.
3. A top-level milestone remains unchecked while its PR is open.
4. Mark a top-level milestone complete only after an independent audit passes,
   final-head required CI is green, the PR is merged, and post-merge `main`
   required CI is green.
5. A Codex self-report is not completion evidence. Source, migrations, tests,
   CI, runtime behavior, and operator documentation are authoritative.
6. Check sub-items only when their implementation or test evidence exists.
7. Keep exactly one `▶ CURRENT` marker, and move it only after the current
   top-level milestone is complete.
8. Preserve historical evidence. Reopen a completed milestone if a regression
   invalidates it.

## Phase A — Apps Production Runtime

### [x] A1. Immutable App build and deployment foundation

Evidence: PR #96.

- Immutable App deployments and verified OCI artifacts.
- Source and artifact lifecycle with desired deployment selection.

### [x] A2. Persistent Moby App runtime

Evidence: PR #97.

- Trusted worker owns runtime side effects and persistent App containers.
- Runtime security baseline; API has no Docker socket.

### [x] A3. App health convergence + public routing

Evidence: PR #100.

- Health is separate from process liveness and fenced to generation,
  deployment, container, and routing incarnation.
- Incarnation-specific Traefik targets fail closed on restart or replacement.
- Private runtime-address validation is consistent between API and ingress.

### [x] A4. App Runtime Logs

Evidence: PR #101, merged as `b5d0631cf85cb5be7a8f649dd33350265f3d2a9f`;
post-merge required `main` checks were green.

- Existing isolated Docker file-log Collector feeds redacted logs to ClickHouse.
- PostgreSQL stores trusted App-to-container metadata, not log bodies.
- Project-scoped API and Console viewer use opaque timestamp/event-ID cursors.
- Retained history can span verified runtime container replacements while
  telemetry retains the records.

### [ ] ▶ CURRENT A5. App Environment Variables + Encrypted Secrets

#### Cryptography and operator key

- [x] Dedicated `APPS_SECRET_KEY`, separate from Function encryption key.
- [x] Fresh install and upgrade generate the key only when it is missing.
- [x] Existing valid key is preserved; invalid existing key fails closed.
- [x] Runtime and upgrade accept standard Base64 and raw URL-safe Base64.
- [x] AES-256-GCM with a fresh nonce, versioned ciphertext, and authenticated
  project/App/variable context.
- [x] No external cryptography dependency.
- [x] Function secret ciphertext format and key behavior remain unchanged.

#### PostgreSQL desired state

- [x] Additive App environment metadata and ciphertext migration.
- [x] Unique variable keys, bounded count/value/description, and deletion cascade.
- [x] Plaintext is not stored in PostgreSQL; value length metadata is bounded
  and kept transactionally with ciphertext.
- [x] Existing installations migrate without fabricated environment records.
- [x] Migration rollback and ciphertext-size backfill have PostgreSQL tests.
- [x] Aggregate configured plaintext value limit is 512 KiB per App.

#### API and authorization

- [x] Read metadata and create, replace, edit metadata, clear, and delete values.
- [x] `apps.read` reads metadata only; `apps.write` is required for mutations.
- [x] Cross-project access fails closed.
- [x] Plaintext is write-only; ciphertext, nonce, and key material are not
  serialized.
- [x] Audit metadata omits plaintext values.
- [x] Aggregate-limit rejection does not commit a row or advance desired state.

#### Runtime convergence

- [x] Runtime-affecting value changes advance `desired_generation`; metadata-only
  changes do not restart an App.
- [x] Trusted worker decrypts values immediately before container creation.
- [x] Secret values are excluded from Docker CLI arguments and BuildKit.
- [x] Temporary environment file is mode `0600`, memory-backed, bounded by the
  aggregate product limit, wiped, and removed.
- [x] Worker rechecks aggregate plaintext size after decryption and refuses to
  create a container for oversized state.
- [x] Generation, lease, health, and route fencing remain fail-closed.

#### Console and API contract

- [x] App detail has an Environment Variables panel; values are not revealed
  after submission.
- [x] OpenAPI documents write-only values, byte limits, aggregate limit, and
  unsupported multiline/NUL input.
- [x] Generated Console client is maintained by the repository generator.
- [x] Read-only users can inspect safe variable metadata.

#### Operations and security

- [x] BuildKit does not receive App runtime variables or secrets.
- [x] Existing common secret redaction remains enabled; it is not a guarantee
  of detecting every possible secret format.
- [x] Installer and upgrade preserve the operator key and do not rotate it.
- [x] Restore documentation requires the matching operator key to decrypt DB
  ciphertext.
- [x] Docker retains the effective environment in container configuration
  while the container exists; host Docker access remains privileged.
- [x] Per-App network isolation and gVisor are not claimed.

#### Tests and production acceptance

- [x] Cipher round-trip, fresh nonce, AAD, and tamper tests.
- [x] API/repository authorization, generation, and lease tests.
- [x] Worker tests prove over-limit decrypted state cannot reach container
  creation.
- [x] PostgreSQL tests cover total-limit create/replace/clear/delete behavior.
- [x] Production Compose smoke exercises real variable and secret injection,
  replacement, and runtime recovery.
- [ ] Final-head required CI is green.
- [ ] Independent audit passes.
- [ ] PR merged.
- [ ] Post-merge `main` required CI is green.

### [ ] A6. App Resource + Operational Hardening

- [ ] Disk/image pressure management and safe garbage collection.
- [ ] Orphan cleanup convergence and resource enforcement under pressure.
- [ ] Network isolation review and bounded retry/backoff tuning.
- [ ] Operator-visible runtime diagnostics.

### [ ] A7. Deployment History + Rollback + Diagnostics

- [ ] Deployment/runtime history and desired-state rollback.
- [ ] Current/previous deployment visibility.
- [ ] Failure reasons with recovery guidance and safe diagnostics.

### [ ] A8. Apps Production Acceptance

- [ ] Clean install and real App deploy/update.
- [ ] Variable/secret update, crash, same-container restart, and recreation.
- [ ] Worker, Collector, and host reboot recovery.
- [ ] Routing recovery, retained logs, and rollback.
- [ ] Disk pressure and long-running soak.

Completion of A8 means the Apps Production Runtime phase is complete.

## Phase B — Production Operations

- [ ] B1. Installer production completion.
- [ ] B2. Platform upgrades.
- [ ] B3. Platform rollback.
- [ ] B4. Backup, restore, and disaster recovery.
- [ ] B5. Host reboot, storage pressure, and cleanup.

## Phase C — Security Hardening

- [ ] C1. Tenant isolation review.
- [ ] C2. Quotas and abuse limits.
- [ ] C3. Security acceptance suite.

Evaluate optional sandboxing such as gVisor only if risk analysis justifies it.

## Phase D — Observability Completion

- [ ] D1. Platform observability.
- [ ] D2. Search, filtering, and retention.
- [ ] D3. Operator signals.

## Phase E — Developer Experience

- [ ] E1. Console/API parity for Apps, Functions, Sites, Databases, Storage,
  Messaging, Webhooks, and Agents.
- [ ] E2. Deployment UX.
- [ ] E3. Documentation.

## Phase F — Agents

Provider execution stays deferred until the core developer cloud is
production-grade.

- [ ] ⏸ F1. Provider adapter.
- [ ] ⏸ F2. Agent execution.
- [ ] ⏸ F3. Tools and secrets.
- [ ] ⏸ F4. Logs, traces, retries, and cancellation.
- [ ] ⏸ F5. Usage limits and security.

## Phase G — Multi-worker / HA

Begin only after single-host production quality.

- [ ] G1. Multiple API replicas.
- [ ] G2. Distributed worker leases and scheduling.
- [ ] G3. Workload placement and node-failure recovery.
- [ ] G4. PostgreSQL HA strategy.
- [ ] G5. Distributed object storage production profile.
- [ ] G6. Rolling platform upgrades.

## Phase H — v1.0 GA Gate

- [ ] Clean-host installation and NAT/Cloudflare deployment.
- [ ] Real App deploy and Function execution.
- [ ] Database, storage, and messaging core flows.
- [ ] Encrypted App secret rotation and failure/restart recovery.
- [ ] Host reboot, backup/restore, upgrade, and rollback.
- [ ] Disk pressure handling, security suite, and long-running soak.
- [ ] Release artifacts, operator docs, and developer docs.
- [ ] No known release-blocking security or reliability defects.

Final gate:

```text
[ ] Stealth v1.0 GA
```

Only check the final gate after a dedicated release audit.
