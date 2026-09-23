# Stealth Context

## Console route context

The Console route context is the client-side representation of the current
Console pathname. It identifies the active organization and optional project,
and exposes canonical paths for Console navigation. Route parsing and dynamic
path construction belong to this context rather than to individual rendering
modules.

## Instance bootstrap capability

The Instance bootstrap capability owns the first-run setup state, GitHub
first-owner authorization, and legacy-installation Instance Owner adoption.
The HTTP API depends on its narrow `BootstrapStore` interface rather than the
full repository. The capability preserves the existing transaction and
sealing invariants: only the verified first-owner flow can seal bootstrap, and
an existing installation can be adopted only through the explicit legacy path.

The setup-state credential seam owns the database URL, Redis URL, and S3 key
pair needed by the setup flow. These values live only in encrypted
`State.Secrets`; `Draft` and `PublicState` carry the non-secret choices and
tested markers. Any state-format change must migrate older encrypted setup
state before rewriting it.

The setup configuration module is the deep seam between HTTP input and durable
setup state. It owns normalization, validation, credential fallback, and the
rules that invalidate a dependency's tested marker after a relevant change.
The setup install input module translates that durable state into the shared
`installengine.Plan` and private production environment, so the HTTP adapter
does not assemble release inputs itself.

The first-owner authorization module under `internal/bootstrap` owns provider
identity normalization, session and setup-handoff creation, and the repository
write. GitHub Web Application Flow and the retained legacy Device Flow are
provider adapters that supply its small authorization proof; neither flow
duplicates owner persistence rules in an HTTP handler.

The Cloudflare provisioning module owns provider side effects and durable
intent/resource-ID reconciliation for named tunnels and DNS. It validates the
selected zone, refuses conflicting provider records, and makes retries safe
after a state write fails. The HTTP adapter only decodes the request and maps
the module's typed errors to transport responses.

Cloudflare workload readiness also depends on active edge TLS coverage for the
desired wildcard hostname. The production certificate-pack inventory is read
from the discovered workload zone; a matching active certificate is required
before the provider status becomes ready. Total TLS state is informational and
does not prove coverage for Cloudflare Tunnel hostnames. Missing coverage keeps
the DNS and tunnel route in place while exposing an actionable TLS status.

The host preflight module owns CPU, memory, free-disk, Docker, Compose, and
Cloudflare outbound-connectivity checks. CLI and browser setup adapters supply
the local-substitutable probes and retain their own presentation, so readiness
definitions do not drift between installation surfaces.

The browser setup flow module owns form state, provider callbacks, install
progress effects, and handoff actions. Cloudflare setup is token-first: the
server verifies the scoped token, discovers accounts and zones, provisions the
named tunnel and DNS, and the shared installer starts and verifies the
production cloudflared service before Quick Tunnel cleanup. The browser setup
view owns stage rendering and delegates lifecycle transitions to that flow
module. Cloudflare OAuth remains an explicit inactive experimental seam and is
not a browser connection path.

## Console log stream

Console log viewers consume a typed `LogSource` identified by the resource
being inspected. The source owns the endpoint path, bounded cursor query, API
response mapping, and cancellable page loader. The stream hook owns polling,
cursor progression, deduplication, and resetting retained lines when the
resource identity changes; feature views provide only the resource context.

## Backend composed configuration

`Config` is the validated application snapshot used by API and worker
composition roots. Domain-specific loaders own their environment parsing and
constraints, then apply a complete validated slice to that snapshot. The
execution loader owns function and agent runner settings; it must preserve the
existing defaults and production credential gates.

The storage loader owns local/S3 paths, quotas, credentials, and staging
settings; it must preserve the existing defaults and storage validation
contract.

The site loader owns static publication limits and Git fetch concurrency. Its
defaults intentionally inherit storage limits for struct-literal test or
embedded configurations, while environment loading keeps the explicit site
values and original bounds.

The telemetry loader owns the optional OTLP endpoint, service name, and sample
ratio. An empty endpoint is a supported no-op configuration; non-empty values
must remain absolute HTTP(S) URLs without credentials, query, or fragment.

The agent settings loader owns only the validated public provider/model catalog
used by Console metadata and request validation. It clones catalog data when
applying it and never treats catalog entries as provider credentials or worker
capability.

The secret settings loader owns decoding of the function-encryption key and
dedicated bootstrap key, plus the GitHub App client ID. It keeps key material
isolated and clones it into the application snapshot; `ValidateFunctions` and
`ValidateBootstrap` remain the production fail-closed gates.

The transport settings loader owns the HTTP listener, Redis endpoint, metrics
token, and trusted proxy network list. It clones network values into the
application snapshot so request-IP trust remains an explicit, immutable
boundary for the API.

The TLS settings loader owns optional ACME listener, directory, email, and
certificate-cache configuration. It receives the resolved storage root and
HTTP listener so certificate cache placement and listener collision checks are
validated before the application snapshot is assembled.

The database loader owns the required `DATABASE_URL`, pool bounds, and
connection lifetime settings. Other configuration domains should follow the
same loader-and-apply boundary instead of adding parsing branches to
`config.Load`.

## Backend runtime composition

`internal/runtime` owns shared process resource composition. API, worker, and
migration entry points use its database pool policy and optional Redis and
migration lifecycle, while keeping process-specific registration and execution
in their own composition roots. Resource failures close any already-created
clients before returning, so a partially assembled process cannot leak a pool
or Redis client.

The auth loader owns session lifetimes, the canonical public app URL, Console
CORS origins, auth/project rate limits, cookie security, and SMTP delivery
settings. It must preserve the existing URL, origin, email, and numeric
validation before applying values to `Config`.

## Backend queue worker persistence

Queue workers depend on local persistence capabilities instead of the concrete
repository. Agent, messaging, webhook, realtime, and Site workers each expose
their own narrow `Persistence` seam for leasing, terminal transitions, and
worker-owned logs or retention. The repository remains the production
implementation supplied by the worker composition root; tests can provide a
small fake without constructing unrelated control-plane state.

## Backend Site lifecycle

Site persistence is organized into three modules. The control-plane module
owns Site metadata and authorization, the deployment module owns source
metadata, activation, build leases, quota transitions, and build logs, and the
artifact module owns immutable path cleanup and public artifact resolution.
All three remain methods on the repository so existing callers keep one
transactional persistence boundary; the file/module split keeps each lifecycle
invariant near the SQL that enforces it.

## Backend persistent App model

Apps are long-running project workloads with durable desired configuration.
Their versioned `WorkloadSpec` is normalized before persistence and identified
by the SHA-256 digest of its canonical JSON representation. Desired and
observed generations remain separate: writes advance desired state only, and
runtime status, errors, and observed generation belong to trusted future
workers. A newly created App is `not_deployed`.

Apps and Sites reserve labels in one database-enforced platform hostname
namespace. An App hostname is reserved metadata; Apps do not enter the static
Site route snapshot and are not executed yet. WorkloadSpec contains runtime
intent only: build and image identity are outside the contract. A future
immutable AppDeployment will own source identity, build definition, OCI digest,
and a WorkloadSpec snapshot/hash. Future workers may advance
`observed_generation` only after the actual workload reflects the desired
generation; PostgreSQL accepting a write is not observation. The intended
future pipeline is BuildKit → OCI → Moby → gVisor. Runtime implementations must
translate the Stealth-owned contract into constrained settings; WorkloadSpec
grants no host access, host mounts, host networking, host PID/IPC, Linux
capabilities, privileged mode, or Docker API access. Secret values have a
separate future encrypted lifecycle and never belong in WorkloadSpec.

## Backend authentication email delivery

The mailer transport keeps a generic `Message`/`Sender` seam for explicit
user-authored project messaging, while authentication flows use the private
payload of `AuthMessage` through `AuthSender`. `NewAuthMessage` is the only
constructor for security email content: it selects fixed Stealth-owned copy
and accepts only a validated server-generated `AuthLink`. The HTTP API stores
only the typed auth sender, so request handlers cannot pass an arbitrary body
to authentication email delivery.

## Console typed form adapters

`CreateDialog` owns field rendering, transient string state, and interaction
feedback. Feature modules own resource-specific form value types and adapters
that validate those values and map them to generated API request payloads.
Resource views should pass the typed adapter result to the mutation instead of
parsing a generic `Record<string, string>` inline.

## Console transport

The Console API transport is the typed seam between TanStack Query and the
generated OpenAPI client. Query modules pass the Query-owned AbortSignal into
the request options and use the shared transport normalizer for API errors and
response data. Cursor traversal accepts the same signal so cancellation stops
both the active request and any subsequent page request.

## Console cache coherence

`src/api/cache-coherence.ts` owns the mapping from semantic project resource
changes to query keys and deduplicates invalidation work. Mutation adapters and
the project realtime adapter both feed this policy; they do not treat realtime
payloads as authoritative state. Cache removal and restore predicates remain
local where they express lifecycle-specific behavior rather than ordinary
resource staleness.

## Console function variables

`FunctionVariablesPanel` owns the complete function-variable workflow: cursor
pagination, metadata-only query state, typed form adaptation, mutation
feedback, and table actions. `FunctionDetailView` composes that panel with
function deployment and execution views instead of owning each variable
concern inline.

## CLI lifecycle workflows

The setup and uninstall workflows keep operational decisions and effects in
their workflow modules, while Bubble Tea models and rendering live in sibling
`*_tui.go` modules. Both TTY and non-TTY paths share the same bootstrap,
uninstall-plan, safety, and cleanup operations; presentation does not decide
which resources are safe to change.

## Console database table detail

`DatabaseRowsView` composes the table metadata shell, `TableRowsPanel` owns
server-filtered row browsing and row creation, `TableSchemaPanel` owns column
and index rendering plus column creation, and `TableRowDetail` owns the
selected-row query and row mutations. URL query state remains the navigation
seam shared by the shell and row browser, so pagination, filtering, and row
inspection retain the existing route behavior.

## Console storage bucket detail

`BucketDetailView` composes storage metadata and tab state. `BucketObjectBrowser`
owns paginated object listing, deletion, upload completion, and file selection;
`BucketUploadDialog` owns filename/quota validation and multipart upload;
`BucketObjectDetail` owns metadata and rename actions; and
`BucketSettingsPanel` owns bucket-limit updates. The shell keeps only the
permission projection and route-level selection so object and settings
changes remain local to their modules.

## Instance domain capability

`PUBLIC_APP_URL` remains the configured Console/public application URL. The
canonical hostname derived from that URL is the instance hostname and is not a
second PostgreSQL setting.

`workload_base_domain` is a separate optional singleton PostgreSQL setting
owned by the Instance Owner. It is normalized through the shared
`internal/domainname` capability, which applies IDNA ASCII canonicalization and
public-suffix-aware registrable-domain validation. Instance Admin and
organization roles do not grant mutation access.

The domain settings API exposes the derived instance hostname and stored
workload base domain at `/v1/admin/domain-settings`. `GET` follows existing
instance-admin visibility, while `PATCH` is Instance Owner-only. Sending JSON
`null` explicitly clears the workload setting; an omitted field is rejected.

Sites now receive a stable globally unique `platform_label` in PostgreSQL.
When `workload_base_domain` is configured, the Site API derives a canonical
`platform_hostname` such as `portfolio.apps.example.com`; when it is unset the
field is `null`. A Site rename never changes the persisted label or public
hostname. The small reserved namespace (`api`, `admin`, `console`, `status`,
and `www`) is allocated a deterministic UUID-suffixed label instead.

The existing worker owns a PostgreSQL-backed platform-route reconciler. It
renders the complete desired platform Site snapshot to
`traefik/dynamic/generated/platform-sites.yaml` and updates the top-level
`traefik/dynamic/.reload.yaml` sentinel with crash-safe
temporary-file/fsync/atomic-rename publication. PostgreSQL is authoritative;
generated YAML is derived state and is rebuilt on worker startup, with stale
routes removed from the next successful snapshot. A PostgreSQL advisory lock
provides single-writer coordination across workers.

The host installer creates and validates the Traefik paths but does not require
host `chown` or root execution. A one-shot production Compose service named
`traefik-state-init` runs as `0:0` with `network_mode: none` and only the
dynamic directory mounted at `/state`. Docker preserves the invoking host user
as owner, assigns worker group `10001`, and prepares `dynamic/` and
`dynamic/generated/` as `0775` plus `.reload.yaml` as `0664`. The fixed worker
identity is `10001:10001`. The worker receives the dynamic directory read-write
because replacing the top-level reload sentinel requires write access to its
parent. The release-managed `core.yaml` is overlaid read-only at the same
worker path; `traefik.yaml` and the installation root are not mounted into the
worker. The runtime smoke proves that the worker can update generated state and
the reload sentinel but cannot write or replace `core.yaml` or access
`traefik.yaml`. Traefik receives the whole dynamic tree read-only. Upgrade and
repair run the initializer before dependent services, re-validate the narrow
path boundary, reject symlinks, and preserve generated content. Operators
should not manually chown the installation.

Platform hostnames target a separate private API listener containing only the
current, enabled Site static-serving surface. It independently resolves the
Host against PostgreSQL, so a stale Traefik file cannot serve a deleted or
disabled Site and cannot expose Console API, health, metrics, or version
routes. The upgrade-safe Console origin is Nginx; workload wildcard routing
uses the same named tunnel to reach Traefik. An Instance Owner/operator can
explicitly cut the Console origin over with `stealth ingress cutover`. The
Instance Owner's PostgreSQL
workload domain is desired state for one wildcard
record and one wildcard tunnel ingress rule. The worker discovers the matching
Cloudflare zone and reconciles it. Console origin desired state is stored
durably and defaults to `proxy` for existing installations; install/update
does not change it. Cutover reuses the same Named Tunnel, verifies public
HTTPS security headers and HSTS, and automatically requests a verified Nginx
rollback after failure. Nginx stays installed and running. Wildcard routing
covers platform Site hostnames only; custom-domain DNS remains user-managed.

The instance-global Cloudflare API token is encrypted with the shared
`functionsecret` cipher in PostgreSQL. Admin status responses expose only
connection and reconciliation state. On upgrade, the isolated one-shot
Cloudflare state initializer decrypts legacy setup state and atomically emits
a narrow encrypted artifact containing only the Cloudflare connection
identity and API token. The worker mounts only that derived artifact; the
Cloudflared tunnel token, GitHub credentials, and database, Redis, and S3
setup credentials are not included. The worker imports the artifact once when
the production connection is absent. Provider reconciliation is asynchronous,
single-writer under a PostgreSQL advisory lock, and retries from PostgreSQL
desired state after restart.
