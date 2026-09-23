# Browser setup

`stealth install` uses a short-lived browser installer for a fresh host. The
terminal performs the host checks, starts only the setup Compose project, and
stays attached to the encrypted setup state while the browser runs. The
browser then owns the reviewed configuration and the host CLI observes the
durable `install_requested` transition before calling the shared Go install
engine on the host.

## Start

Run the supported bootstrap or invoke the installed CLI:

```bash
curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/HEAD/scripts/bootstrap.sh | sh
stealth install
```

The CLI checks Docker and Docker Compose on the host, creates the private
installation directory, starts the
setup API, setup Console, setup proxy, PostgreSQL, and Redis, then prints a
temporary HTTPS setup URL and a one-time setup code. Open the URL in a browser
on the same operator workstation. If the temporary tunnel cannot be created,
the CLI leaves the setup service running on its loopback URL and can continue
observing the browser flow through an SSH-forwarded local URL. The normal
command remains alive until setup completes or is stopped. `--no-wait` is
rejected for this browser flow unless a future release adds an explicit
host-side supervisor.

The setup URL and code expire after 15 minutes. `stealth setup` creates a new
session when the first-owner flow is still open. `stealth install --repair`
keeps the existing configuration and is the recovery path for a partial local
installation.

## Wizard stages

The setup Console walks through these stages:

1. Welcome and host checks. Docker, Docker Compose, CPU, memory, and disk are
   host CLI results projected through encrypted setup state; service checks are
   performed by the temporary setup API.
2. Instance name and public URL.
3. GitHub App connection, using the GitHub Manifest flow by default or manual
   App credentials as a fallback, followed by browser authorization of the
   first owner.
4. Networking, using a scoped Cloudflare API token for a named production
   tunnel or one of the local/reverse-proxy modes.
5. Database and Redis selection. Bundled services are the default; external
   PostgreSQL and Redis URLs must pass a live connection test.
6. Storage selection. Local storage is the default; S3-compatible settings
   must pass a live storage test.
7. Review of the final settings.
8. Installation progress streamed over SSE.
9. Final health, readiness, public URL, and named Cloudflare Tunnel checks.

The install button is idempotent. A second click returns the existing run,
and a dropped browser connection can reconnect to the persisted setup state.
The request is durably marked as `install_requested` before the host CLI starts
production installation, so the browser cannot continue changing the reviewed
configuration after that point. The host CLI can restart and resume observing
this state without requiring the browser to remain open. Progress is written
to encrypted state and the setup API re-reads that state for SSE and polling;
there is no in-memory event dependency.
Failed runs remain resumable and preserve the generated configuration and
provider side effects. A successful setup removes the temporary setup
services, starts the production API, worker, Console, proxy, and optional
Cloudflare Tunnel, then redirects to the dashboard.

## GitHub and Cloudflare security

GitHub App Manifest registration returns App credentials to the server
callback. The setup API stores credentials in encrypted setup state and never
returns them to the Console. The Manifest state is random, hashed at rest,
single-use, and expires quickly. It includes the exact setup HTTPS callback for
the next step. After conversion, Stealth starts GitHub's browser Web
Application Flow with a second one-time state and PKCE challenge. The callback
exchanges the code server-side, looks up `/user`, establishes the first
Instance Owner, and discards the access token. No Device Flow setting, device
code, client secret, or access token enters the browser UI, URL storage, or
logs. If a user cancels or the exchange fails, the one-time state is consumed
and the wizard offers a fresh browser authorization attempt.

The browser submits the non-secret JSON manifest in a `POST` form field to
GitHub's documented Manifest endpoint. The per-installation manifest includes
the exact HTTPS callback URL and requests no webhook events because the setup
service does not consume GitHub App webhooks.

The default App name is `Stealth Setup <random-suffix>` to avoid collisions;
GitHub lets the registering user edit that name before creating the App. A
manually entered App must have the exact HTTPS callback
`/v1/setup/github/authorize/callback` registered for the setup host. The
legacy Device Flow API remains only for compatibility with existing
non-browser callers and is not used by this wizard.

Cloudflare setup is token-first. The Console shows an API Token field and a
`Verify Token` action. The setup API validates the token by discovering the
accessible Cloudflare accounts, then the browser selects an account and
domain. `Continue` performs the provider-side provisioning on the server: it
creates or resumes the named tunnel, configures the Console ingress to
`http://proxy:80` with a catch-all 404 rule, creates the proxied Console CNAME,
and writes the private cloudflared token file. The host CLI then starts the
production Compose profile, checks the named tunnel health, verifies the
production hostname, and removes the temporary Quick Tunnel only after those
production checks pass. Before worker startup, a network-isolated one-shot
initializer decrypts the onboarding snapshot and writes a versioned,
Cloudflare-only encrypted import artifact. It contains the existing Cloudflare
connection identity and API token. The import mount excludes the complete
setup snapshot and its unrelated GitHub, Cloudflared tunnel, setup database,
Redis, and S3 credentials, plus bootstrap and session state; the worker
receives its separate PostgreSQL and Redis runtime configuration as required
to operate. It imports the artifact
into its durable Cloudflare connection row only when that production
connection is absent.

After an Instance Owner configures a workload base domain, the worker
asynchronously adds one proxied wildcard CNAME and a wildcard rule on that
same named tunnel. For example, `*.apps.example.com` routes to
`http://traefik:8080`; the existing Console route remains on `http://proxy:80`
and Nginx. The workload domain in PostgreSQL is desired state. Admin status
reports whether provider reconciliation is pending, ready, or in error. See
[Cloudflare workload routing](cloudflare-workload-routing.md) for connection
and upgrade behavior.

Create a custom Cloudflare API token scoped to the account and zones used by
the installation. The minimum permissions are:

- Account: `Cloudflare Tunnel` `Edit`.
- Account: `Account Settings` `Read`, required for account discovery.
- Zone: `Zone` `Read`, required for Console and workload zone discovery.
- Zone: `DNS` `Edit`, required for the Console and workload CNAME records.
- Zone: `SSL and Certificates` `Read`, required to inspect workload edge TLS
  readiness.

Scope Zone Read and DNS Edit to both the Console and workload zones when they
are different. Scope SSL and Certificates Read only to the workload zone. If
both hostnames use the same zone, one zone scope is sufficient.

Stealth does not accept a Global API Key. The token is submitted to the setup
API, stored in encrypted setup state during onboarding and then encrypted at
rest in PostgreSQL, and never returned in public setup state, browser query
data, SSE events, or logs. Cloudflare OAuth support is
experimental and inactive in this release. It is not shown as a connection
option and the inactive endpoint never redirects to Cloudflare or exchanges a
callback code. A random `*.trycloudflare.com` setup hostname is never used as
an OAuth redirect URI.

## External infrastructure

Connection strings and storage credentials are accepted only by setup API
requests. They are kept server-side and are omitted from the public setup
state. Changing a saved external URL or storage credential clears its tested
status, so the replacement must be tested again before installation.

The host CLI starts bundled PostgreSQL and Redis only for bundled modes. For
external modes, the host installengine runs migration and production services
without starting the bundled dependency. This keeps the selected topology
consistent with the review screen.

## Completion and recovery

Creating the first Instance Owner permanently seals public bootstrap in the
database. The setup cookie and encrypted state claim continue authorizing that
same in-progress wizard until production handoff completes; a new anonymous
browser cannot reactivate first-run setup. The final session handoff is a
single-use encrypted ticket submitted in a POST body to the configured
production URL, not a query parameter.

If cleanup fails after production becomes healthy, the setup state reports a
cleanup failure and keeps the browser flow resumable. Retry from the setup
Console or local CLI after checking `stealth doctor`. Do not remove Docker
volumes as a repair action. The host preflight also reports CPU, memory, free
disk, Docker, Cloudflare API reachability, and Cloudflare Tunnel edge
connectivity. The final `cloudflared` startup performs its own DNS and
TCP/UDP port 7844 checks.
