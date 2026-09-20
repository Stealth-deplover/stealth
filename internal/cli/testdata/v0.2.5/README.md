Source fixture for the real Stealth v0.2.5 installation shape.

- Source tag: `v0.2.5`
- Annotated tag object: `66daabcf6f34c937d2bfedc5921c9e0658edee4a`
- Tagged commit: `6a09e45ccac7ababcbba5e1a42d0e74d487f9f07`
- Exact copied release-managed files:
  - `compose.production.yaml`
  - `compose.setup.yaml`
  - `console/deploy/nginx.conf`
- `config.env` is a deterministic test rendering of the v0.2.5 generated
  configuration shape. Secret values and host-specific values are fake or
  normalized for tests; no credentials were copied from a real installation.
- `VERSION` models the file written by the v0.2.5 installer.
- The v0.2.5 tag has no `telemetry/` directory. Its absence is intentional and
  is part of the upgrade regression contract.

The integration test may replace image references and local probe ports in a
temporary copy, but it never mutates this checked-in fixture.
