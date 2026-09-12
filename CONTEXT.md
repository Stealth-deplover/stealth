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
