# Stealth Context

## Console route context

The Console route context is the client-side representation of the current
Console pathname. It identifies the active organization and optional project,
and exposes canonical paths for Console navigation. Route parsing and dynamic
path construction belong to this context rather than to individual rendering
modules.

## Backend composed configuration

`Config` is the validated application snapshot used by API and worker
composition roots. Domain-specific loaders own their environment parsing and
constraints, then apply a complete validated slice to that snapshot. The
execution loader owns function and agent runner settings; it must preserve the
existing defaults and production credential gates.
