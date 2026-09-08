# Backend gaps tracked by the console

The console only renders capabilities represented by the Go OpenAPI contract. These gaps are explicit boundaries; the frontend does not emulate them with fake data or a second backend. When a capability is added, regenerate the client with `npm run api:generate` before changing feature code.

## Service dependency graph

Frontend need:

Resource relationships for the Services canvas, including which sites/functions call databases, storage, webhooks, or other services.

Existing backend:

`GET/PUT /v1/projects/{projectID}/service-layout` stores resource positions only.

Missing:

An explicit relationship schema and read endpoint. Position data does not establish a dependency.

Recommendation:

Add a versioned project service-relationships endpoint with stable resource identifiers and relationship type. The current canvas renders real nodes, persists `x/y`, and keeps `edges` empty.

## Source and code editing

Frontend need:

Browse or edit function/site source in a developer console.

Existing backend:

Function and site deployment endpoints accept source archives; the contract does not return source files or an editor session.

Missing:

Source/artifact listing, file content, save/version, and authorization semantics.

Recommendation:

Define a source/artifact contract before adding Monaco or an in-console editor. The console currently explains the archive deployment boundary.

## Unified deployment feed

Frontend need:

A single project deployment timeline across Functions and Sites.

Existing backend:

Deployment history is scoped to each Function or Site.

Missing:

A project-level deployment index with resource type, resource ID, status, environment, and timestamps.

Recommendation:

Add a project deployment feed endpoint. The current Deployments page links to the real resource-scoped views.

## Aggregated project health and dashboard metrics

Frontend need:

One backend-owned project health signal, recent failure aggregate, and deployment summary for the overview.

Existing backend:

Project usage exposes measured counts and usage values; project audit exposes activity events.

Missing:

A unified health/status schema and aggregated recent deployment/failure view. A green “healthy” status cannot be inferred safely from independent list calls.

Recommendation:

Add a project overview or health endpoint with freshness and partial-data semantics. The current overview labels the API connection and displays only measured usage/audit data.

## Realtime logs and durable log cursors

Frontend need:

Low-latency build, runtime, and worker log streaming.

Existing backend:

Resource log endpoints expose bounded records with `after`/sequence-style incremental reads.

Missing:

A realtime transport and an explicit retention/stream lifecycle contract.

Recommendation:

Add SSE/WebSocket semantics or document polling limits and cursor retention. The reusable LogViewer polls only incremental pages and never refetches the full history.

## Global search

Frontend need:

Search resources across an organization/project from the command palette.

Existing backend:

Resource list endpoints are scoped and do not expose a global search operation.

Missing:

Authorized, paginated search with resource type and project scope.

Recommendation:

Add a global search endpoint. The command palette currently searches navigation and real organization/project records only.

## Permissions and roles granularity

Frontend need:

Explainable role/permission management for organization and project members.

Existing backend:

Membership and authorization responses are available for the current API surface, but no complete permission matrix is exposed to the console contract.

Missing:

Stable role definitions, permission scopes, invitation lifecycle details, and mutation semantics for granular access control.

Recommendation:

Add role/permission schemas and capability checks before building an authorization editor. The console never treats client state as authorization.

## Activity stream coverage

Frontend need:

A complete cross-resource activity timeline for the overview and organization audit experience.

Existing backend:

Organization/project audit event lists are available.

Missing:

An explicit activity stream contract covering all worker/deployment/resource events, ordering guarantees, and pagination semantics.

Recommendation:

Define the event taxonomy and cursor contract. The console currently displays only events returned by the audit endpoints.

## Trace filters and span hierarchy

Frontend need:

Filter traces by status/service and inspect distributed span trees.

Existing backend:

Trace list endpoints expose cursor/limit and root HTTP traces.

Missing:

Status/service query parameters and span relationships/detail payloads.

Recommendation:

Extend the trace schema with filter parameters and span relationships. The console shows root traces only and does not invent hierarchy.

## Messaging delivery worker and policy

Frontend need:

Show whether provider/topic/message operations are actually deliverable and guide operators through the supported lifecycle.

Existing backend:

The OpenAPI contract includes provider, topic, subscriber, message, cancellation, and delivery mutations. Provider credentials are accepted by the API and described as encrypted, while message delivery is queued for a trusted worker adapter.

Missing:

The contract does not expose worker readiness, provider delivery health, or a complete operator-facing policy for secret rotation and message cancellation semantics.

Recommendation:

Add explicit worker/provider health and delivery capability fields before the console presents destructive or credential-writing workflows. The current console intentionally exposes metadata only and does not pretend that queue acceptance means provider delivery.

## Runtime and capability catalog

Frontend need:

Keep Function runtimes, Site build runtimes/frameworks, regions, and future
provider capabilities consistent between forms and backend validation.

Existing backend:

Function runtime values are present as an OpenAPI enum, and the Agent Catalog
exposes provider/model values. There is no general capabilities endpoint.

Missing:

An authorized, versioned capability catalog with availability and deprecation
metadata for all resource types.

Recommendation:

Add `GET /v1/capabilities` (or a scoped equivalent) and make it the source for
runtime/framework/region choices. Until then, the console keeps the current
Function runtime fallback in one module and validates the form against that
same list.

Current frontend behavior:

Function runtimes are centralized from the generated OpenAPI enum. No Next.js
endpoint is used to emulate a capability service.

## Server-side filtering and sorting

Frontend need:

Search, filter, and sort controls that apply to the complete resource dataset.

Existing backend:

Most list endpoints expose only `limit` and cursor. Database rows additionally
support declared indexed filter, search, and ordering parameters.

Missing:

Consistent server-side filter/sort parameters for Functions, Sites, Users,
Webhooks, Agents, traces, and the other resource indexes.

Recommendation:

Add explicit query parameters and document their index and authorization
behavior.

Current frontend behavior:

Cursor navigation is server-driven wherever the contract is paginated. The
Function and Site list search is explicitly labeled current-page search rather
than pretending to search the entire dataset.

## Resource lookup by ID

Frontend need:

Load an organization directly when a deep link opens outside the first list
page.

Existing backend:

The organization path currently exposes update but not a GET-by-ID operation.

Missing:

`GET /v1/organizations/{organizationID}`.

Recommendation:

Add an authorized GET operation with the same organization visibility rules as
the list endpoint.

Current frontend behavior:

The detail hook follows organization list cursors until it finds the requested
ID. This is correct but less efficient than a resource lookup endpoint.

## CSRF model

Frontend need:

Safe mutation semantics when the Console session is represented by an
HttpOnly cookie.

Existing backend:

The API owns the `stealth_session` cookie and authorization. The OpenAPI
contract does not describe a CSRF token or origin-check requirement.

Missing:

An explicit CSRF defense contract for state-changing cookie-authenticated
requests.

Recommendation:

Document and enforce SameSite/origin checks or a CSRF token strategy in the Go
API. Do not add a Next.js proxy or a second session layer.

Current frontend behavior:

The browser sends the Go-owned cookie with `credentials: include`; no token is
persisted in browser storage. Production same-origin routing remains the
recommended deployment shape.

## Deployment retry and redeploy

Frontend need:

Retry a failed build or redeploy the same source without making the developer
upload the archive again.

Existing backend:

Function and Site uploads create a new immutable deployment. Ready deployments
can be activated. There is no retry or redeploy operation for an existing
deployment, and source archives are not returned to the Console.

Missing:

An explicit retry/redeploy endpoint that safely creates a new deployment from
the original source, with clear idempotency and authorization semantics.

Recommendation:

Add a backend-owned retry/redeploy operation when the artifact lifecycle and
source retention policy support it. The operation should return the new
deployment and preserve the existing immutable deployment history.

Current frontend behavior:

Failed deployment detail exposes build logs and links back to the Function or
Site to upload another archive. The Console does not show a fake Retry button.

## Database overview metadata and aggregates

Frontend need:

Database engine/status, total table and column counts, and the latest backup without scanning every resource page.

Existing backend:

ProjectDatabase exposes ID, project ID, name, and creation/update timestamps only. Tables and columns are separate cursor-paginated resources with no aggregate counts. Backups are ordered by ascending ID and have no latest-first option.

Missing:

Engine/status fields, aggregate counts, and an efficient latest-backup endpoint or descending backup ordering.

Current frontend behavior:

No engine, health, size, or fabricated count is displayed. Overview shows table count and latest backup only when the initial response contains the complete collection. Otherwise users browse the paginated lists. Table lists omit column counts rather than fetching schema separately for every table.

## Database schema and backup lifecycle

Frontend need:

Edit column definitions and show asynchronous backup status/progress or retained failure details.

Existing backend:

Columns can be listed, created, and deleted; there is no update-column endpoint. Backups are synchronous complete logical snapshots limited to 10,000 rows and 50 MB. Oversized snapshots fail rather than silently omitting rows. Backup metadata contains ID, size, checksum, and created_at, but no status enum or error field. Restore atomically replaces schema, rows, indexes, and relationships.

Missing:

Column update semantics and an asynchronous backup job/lifecycle contract.

Current frontend behavior:

Schema supports typed column creation and read-only existing definitions. Backup requests show a pending button and contextual request errors; persisted backups do not receive invented Pending/Ready/Failed statuses. Restore uses explicit destructive confirmation and invalidates table, column, index, and row caches.

## Storage object capabilities

Frontend need:

Object key/path, public URL, copy, global object search, or folder operations.

Existing backend:

Storage uses flat file IDs and display names; separators are rejected. File metadata includes content type, size, SHA-256, permissions, and timestamps. Authenticated download, display-name rename, delete, bucket settings, and cursor pagination exist. Upload uses the multipart filename; a separate name field cannot accompany that filename. File security is not a public/private visibility flag.

Missing:

Path/folder, public URL, copy, object count, and server-side file filter/sort contracts.

Current frontend behavior:

The browser sends the File as multipart data, with local selection and pending state only. The Console shows a flat object list, actual metadata, Go download links, rename, and contextual delete confirmation. It does not expose folders, public URLs, global file filtering, or invented visibility/counts.

## Database and Storage capability audit

The existing contract also supports database/table deletion, table permission replacement, index and relationship management, row import/export, and row transactions. These are backend capabilities, not gaps. This focused UX change keeps existing routes and does not add a SQL editor or a general administration framework. Row filtering uses equality JSON with indexed columns; sorting on user columns requires required indexed columns, and full-text search requires a declared full-text index. The schema browser follows all metadata cursors to validate row fields; row/object datasets remain server-paginated.

## Users and organization invitations

Frontend need:

Show project-user activity, sessions, editable profiles, and resendable organization invitations.

Existing backend:

Project users expose identity, verification, status, timestamps, create, block/unblock, and delete operations. Organization membership exposes role updates/removal, and invitations expose create, pending/expired listing, revoke, and one-time acceptance. Invitation creation reports whether the configured mailer accepted delivery.

Missing:

Project-user activity/session/profile endpoints, an invitation resend endpoint, and an invitation preview endpoint that returns organization metadata before acceptance. The invitation list also intentionally returns pending/expired records only.

Current frontend behavior:

The Console shows only returned user/member/invitation fields, gates mutations with `can_manage`, calls the real accept endpoint from the email token flow, and does not show session, activity, profile-edit, resend, or fake organization-preview controls.

## Webhook delivery inspection and retry

Frontend need:

Inspect an individual delivery's request/response payload, headers, latency, trace correlation, and retry or redelivery it after a failure.

Existing backend:

`GET /v1/projects/{projectID}/webhooks/{webhookID}/deliveries` returns cursor-paginated delivery metadata: event, lifecycle status, attempt count, last HTTP status, last error, and timestamps. Response bodies and secrets are not exposed, and there is no single-delivery or retry endpoint.

Missing:

An authorized delivery detail schema with backend-redacted request/response data, latency/trace metadata, and an explicit retry or redelivery operation with idempotency semantics.

Current frontend behavior:

The Console keeps lifecycle status separate from the original HTTP status, displays available failure context, polls only pending/running deliveries, and does not show request/response inspectors or a fake Retry action.

## Webhook event catalog

Frontend need:

Offer an authoritative event picker instead of asking developers to enter event names manually.

Existing backend:

Webhook events are validated as wildcard or lowercase event-name strings. The OpenAPI contract does not expose a closed enum or project capability catalog.

Missing:

An authorized, versioned event catalog with availability and deprecation metadata.

Current frontend behavior:

The Console accepts event names or `*` and relies on Go validation. It does not invent a different event list.

## API key rotation and usage metadata

Frontend need:

Atomically rotate a project API key and display richer audit metadata such as creator or last-used IP.

Existing backend:

The API exposes create, safe metadata lookup/list, and revoke. It returns `last_used_at` and expiry/revocation timestamps, but no atomic rotate endpoint, `created_by`, or `last_used_ip` fields.

Missing:

An explicit rotation contract with overlap/revocation semantics and any additional audit fields needed by the Console.

Current frontend behavior:

The Console shows only safe prefix/metadata, reveals the create secret once, and offers revoke. It does not show a fake Rotate action or infer usage history from missing fields.
