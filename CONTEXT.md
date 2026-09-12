# Stealth Context

## Console route context

The Console route context is the client-side representation of the current
Console pathname. It identifies the active organization and optional project,
and exposes canonical paths for Console navigation. Route parsing and dynamic
path construction belong to this context rather than to individual rendering
modules.

## Console log stream

Console log viewers consume a typed `LogSource` identified by the resource
being inspected. The source owns the endpoint path, bounded cursor query, API
response mapping, and cancellable page loader. The stream hook owns polling,
cursor progression, deduplication, and resetting retained lines when the
resource identity changes; feature views provide only the resource context.
