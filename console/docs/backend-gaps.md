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
