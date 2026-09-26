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

### [x] A5. App Environment Variables + Encrypted Secrets

Evidence: PR #102, merge commit `8c09819d84d53b4e65ded23a6744d7c23e84802b`;
post-merge `main` SHA `8c09819d84d53b4e65ded23a6744d7c23e84802b`; audit verdict:
passed.

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
- [x] Final-head required CI is green.
- [x] Independent audit passes.
- [x] PR merged.
- [x] Post-merge `main` required CI is green.

### [x] A5.5. Codebase Quality & Refactor Baseline

PR: #103
merge commit: `a4d1c34c324440d1f4f3c73d04970e542723d7bc`
post-merge main SHA: `a4d1c34c324440d1f4f3c73d04970e542723d7bc`
audit verdict: READY TO MERGE / passed
post-merge required checks: Backend, Console, Installer/release, CodeQL Go,
and CodeQL JavaScript/TypeScript passed on the verified main SHA.

#### Formatting / static quality

- [x] Explicit Console Prettier config and ignore policy.
- [x] Generated API client excluded from Prettier ownership.
- [x] `npm run format:check` required in Console CI.
- [x] Human-written Console source formatted.
- [x] Go remains gofmt-enforced.

#### Backend structure

- [x] `internal/appruntime/docker.go` decomposed by responsibility.
- [x] `internal/repository/apps_runtime.go` decomposed by responsibility.
- [x] `internal/appruntime/worker.go` decomposed by responsibility.
- [x] Existing package and layer boundaries preserved.
- [x] No behavior, schema, or API change intended.

#### Frontend structure

- [x] App detail is primarily composition and orchestration.
- [x] Feature panels own relevant UI logic.
- [x] No new frontend state framework or dependency.
- [x] Generated OpenAPI types remain authoritative.

#### Regression proof

- [x] Backend unit and integration tests green.
- [x] Required PostgreSQL Apps suites ran and passed.
- [x] Console lint, typecheck, tests, build, and E2E green.
- [x] Production Compose Smoke green, including the real v0.2.5 upgrade smoke.
- [x] No dependency additions.
- [x] Independent audit passes.
- [x] PR merged.
- [x] Post-merge `main` CI green.

### [ ] ▶ CURRENT A6. App Resource + Operational Hardening

#### Runtime image cache

- [x] Stealth-owned runtime image inventory is strict, byte-bounded, and processed in batches without a low lifetime count ceiling.
- [x] Desired/current deployment images are protected.
- [x] GC removes only safe Stealth runtime tags; global Docker prune is forbidden.
- [x] Runtime cache has validated max/target/interval configuration.
- [x] GC ordering and per-sweep work are deterministic and bounded; pressure with successful progress schedules another sweep after one minute.
- [x] Persisted OCI artifacts and artifact quota survive cache eviction.
- [x] An evicted deployment can be re-imported and converge.

#### Cleanup and recovery

- [x] Orphan container discovery remains ownership-fenced and idempotent.
- [x] Expired cleanup leases recover and stale workers are fenced.
- [x] Already-absent proven targets converge successfully.
- [x] Runtime, health, cleanup, and network retries remain bounded.
- [x] Completed and terminal cleanup history has a bounded retention policy.

#### Resource enforcement

- [x] Real runtime CPU limit is verified.
- [x] Real runtime memory and swap limits are verified.
- [x] Real runtime PID limit is verified.
- [x] Docker security profile and namespace constraints are verified.
- [x] Runtime log rotation settings are verified.

#### Operational visibility

- [x] Runtime image cache size, limit, pressure, GC result, and reclaim estimates are exposed.
- [x] OOM and process exit metrics use fixed low-cardinality reasons.
- [x] Safe structured diagnostics contain no secrets or unbounded metric labels.

#### Network review

- [x] Shared App bridge peers, reachable ports, and routing are documented.
- [x] Direct control-plane database/build networks remain unattached to the App bridge.
- [x] App containers are rejected if they gain unexpected network attachments.
- [x] Shared-bridge east-west traffic is documented and deferred to Phase C1.

#### Regression proof

- [x] Backend unit/integration tests green.
- [x] Required PostgreSQL Apps tests actually run and pass, including high-cardinality protection batching.
- [x] Production Compose Smoke verifies GC, image recovery, and actual HostConfig.
- [x] Existing Apps routing, health, logs, encrypted environment, and recovery smoke stays green.
- [x] Backend, Console, Installer/release, CodeQL Go, CodeQL JavaScript/TypeScript, and Production Compose Smoke CI green.
- [x] No dependency expansion.
- [ ] Independent audit passes.
- [ ] PR merged.
- [ ] Post-merge `main` CI green.

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

- [ ] C1. Tenant isolation review, including per-App east-west network isolation.
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
