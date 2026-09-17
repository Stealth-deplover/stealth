# Host-side setup architecture

The browser wizard remains the configuration experience for fresh Stealth
installations. The temporary setup service is a coordinator and projection
API; the host-side `stealth install` process is the production deployment
executor.

## Ownership

Before the host-side migration, the flow was:

```text
Browser → setup API container → installengine → docker compose → host Docker
                                      ↘ /var/run/docker.sock
```

The setup API could therefore execute production installation from a
web-facing temporary container.

The current flow is:

```text
Browser → setup API → encrypted install_requested state
                         ↓
              host Stealth CLI → installengine → host docker compose
                         ↓
                 health checks → handoff → setup cleanup
```

`POST /v1/setup/install` validates the finalized configuration, persists a
fresh `InstallRunID`, changes the phase to `install_requested`, and returns
`202 Accepted`. It does not invoke Docker, Compose, or the production
installengine. The host CLI reloads and validates that state again, takes the
existing installation lock, verifies the same run ID, and runs the existing
`internal/installengine` implementation through the host command runner.

The setup API owns browser authentication, configuration collection, provider
callbacks, service checks, and public state projection. Once installation is
requested, the host CLI owns lifecycle, progress, failure, completion, handoff
status, Quick Tunnel cleanup, and removal of the setup API/Console/proxy.

## State and recovery

Setup state is encrypted and atomically replaced under a cross-process advisory
lock. Host progress updates carry the `InstallRunID`; stale writers cannot
change a newer run or move a run through an invalid phase. The durable phase
and run ID survive setup API restarts, browser disconnects, host CLI restarts,
and host reboots. `stealth install --repair --wait` restores the temporary
setup services when needed and resumes an incomplete run idempotently.

The setup service receives only the narrow host `state/` coordination bind
needed to share this encrypted state. It does not receive the production
installation root, Compose files, any other host filesystem path, or the
Docker API. The setup container runs as root only so the shared state remains
usable when the host CLI runs as a different supported UID. The host CLI
creates the state directory as setgid `0770` and writes the encrypted payload
and lock as group-private `0660`; the host operator must own or belong to the
directory's group. Existing installations created with the earlier `0600`
state permissions must be migrated by the installation operator (or repaired
as root) before a different non-root host UID can resume them.

`--no-wait` is rejected for browser setup in this release. There is no separate
host supervisor that could observe `install_requested`, so allowing the CLI to
exit would leave a request without an executor.

Ctrl+C before `install_requested` may remove the temporary setup services and
the exact recorded Quick Tunnel. After a request, Ctrl+C leaves the durable
run and production resources intact; repair/resume is the recovery path.

## Preflight and progress

Docker, Docker Compose, CPU, memory, disk, and host port checks run in the host
CLI. Safe names, results, and bounded detail are projected into encrypted setup
state for the browser System Check. The setup API contributes only checks it
legitimately owns, such as PostgreSQL, Redis, storage, and Cloudflare network
connectivity. It never runs `docker --version`, `docker compose version`, or
resource probes in the temporary container.

The installengine progress callback writes bounded, secret-free steps to
encrypted state. The setup API's SSE endpoint polls that durable state and
also serves the same state through the normal status polling path. A browser
can close and reconnect without depending on an in-memory event stream.

After production health checks pass, the host CLI persists `handoff` before
cleanup. The browser consumes the one-time production handoff, and the host
waits for that consumption (or the handoff expiry) before removing temporary
setup services and marking the run `complete`.

## Security boundary

The setup service has no `/var/run/docker.sock`, no Docker CLI, and no Compose
plugin. A regression check fails if the setup service regains a Docker socket
or `privileged: true`, and a built setup image is checked for Docker binaries.
This reduces the temporary web-facing service's authority over the host Docker
daemon; it does not eliminate other setup, provider, network, or host security
risks.
