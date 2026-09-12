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

The database loader owns the required `DATABASE_URL`, pool bounds, and
connection lifetime settings. Other configuration domains should follow the
same loader-and-apply boundary instead of adding parsing branches to
`config.Load`.

The auth loader owns session lifetimes, the canonical public app URL, Console
CORS origins, auth/project rate limits, cookie security, and SMTP delivery
settings. It must preserve the existing URL, origin, email, and numeric
validation before applying values to `Config`.
