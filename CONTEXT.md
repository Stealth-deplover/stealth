# Stealth Context

## Console route context

The Console route context is the client-side representation of the current
Console pathname. It identifies the active organization and optional project,
and exposes canonical paths for Console navigation. Route parsing and dynamic
path construction belong to this context rather than to individual rendering
modules.

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
