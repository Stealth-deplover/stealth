# Browser setup

`stealth install` uses a short-lived browser installer for a fresh host. The
terminal performs the host checks, starts only the setup Compose project, and
stays attached to the encrypted setup state while the browser runs. The
browser then owns the reviewed configuration and the setup API calls the
shared Go install engine directly.

## Start

Run the supported bootstrap or invoke the installed CLI:

```bash
curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/HEAD/scripts/bootstrap.sh | sh
stealth install
```

The CLI checks Docker, creates the private installation directory, starts the
setup API, setup Console, setup proxy, PostgreSQL, and Redis, then prints a
temporary HTTPS setup URL and a one-time setup code. Open the URL in a browser
on the same operator workstation. If the temporary tunnel cannot be created,
the CLI leaves the setup service running on its loopback URL and can continue
observing the browser flow through an SSH-forwarded local URL. Use
`stealth install --no-wait` only when a separate supervisor will observe the
state; the normal command remains alive until setup completes or is stopped.

The setup URL and code expire after 15 minutes. `stealth setup` creates a new
session when the first-owner flow is still open. `stealth install --repair`
keeps the existing configuration and is the recovery path for a partial local
installation.

## Wizard stages

The setup Console walks through these stages:

1. Welcome and host checks.
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
The request is durably marked as `install_requested` before the setup worker
starts, so the browser cannot continue changing the reviewed configuration
after that point. The host CLI can restart and resume observing this state
without requiring the browser to remain open.
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
domain. `Continue` performs the remaining provisioning on the server: it
creates or resumes the named tunnel, configures ingress to the Stealth proxy
with a catch-all 404 rule, creates the proxied CNAME, writes the private
cloudflared token file, starts the production Compose profile, checks the
named tunnel health, and verifies the production hostname. The temporary
Quick Tunnel is removed only after those production checks pass.

Create a custom Cloudflare API token scoped to the account and domain used by
the installation. The minimum permissions are:

- Account: `Cloudflare Tunnel` `Edit`.
- Account: `Account Settings` `Read`, required for account discovery.
- Zone: `Zone` `Read`, required for domain discovery.
- Zone: `DNS` `Edit`, required for the proxied CNAME.

Stealth does not accept a Global API Key. The token is submitted to the setup
API, stored only in encrypted setup state, and never returned in public setup
state, browser query data, SSE events, or logs. Cloudflare OAuth support is
experimental and inactive in this release. It is not shown as a connection
option and the inactive endpoint never redirects to Cloudflare or exchanges a
callback code. A random `*.trycloudflare.com` setup hostname is never used as
an OAuth redirect URI.

## External infrastructure

Connection strings and storage credentials are accepted only by setup API
requests. They are kept server-side and are omitted from the public setup
state. Changing a saved external URL or storage credential clears its tested
status, so the replacement must be tested again before installation.

The setup API starts bundled PostgreSQL and Redis only for bundled modes. For
external modes, migration and production services run without starting the
bundled dependency. This keeps the selected topology consistent with the
review screen.

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
