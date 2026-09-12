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
