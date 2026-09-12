# Stealth Context

## Console route context

The Console route context is the client-side representation of the current
Console pathname. It identifies the active organization and optional project,
and exposes canonical paths for Console navigation. Route parsing and dynamic
path construction belong to this context rather than to individual rendering
modules.

## Instance bootstrap capability

The Instance bootstrap capability owns the first-run setup state, GitHub Device
Flow onboarding, and legacy-installation Instance Owner adoption. The HTTP API
depends on its narrow `BootstrapStore` interface rather than the full
repository. The capability preserves the existing transaction and sealing
invariants: only the verified first-owner flow can seal bootstrap, and an
existing installation can be adopted only through the explicit legacy path.

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
