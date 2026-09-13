# Browser setup

`stealth install` uses a short-lived browser installer for a fresh host. The
terminal prepares Docker and starts only the setup Compose project. The browser
then owns the reviewed configuration and the setup API calls the shared Go
install engine directly.

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
the CLI leaves the setup service running on its loopback URL.

The setup URL and code expire after 15 minutes. `stealth setup` creates a new
session when the first-owner flow is still open. `stealth install --repair`
keeps the existing configuration and is the recovery path for a partial local
installation.

## Wizard stages

The setup Console walks through these stages:

1. Welcome and host checks.
2. Instance name and public URL.
3. GitHub App connection, using the GitHub Manifest flow when available or
   manual App credentials as a fallback.
4. Networking, including Cloudflare OAuth or a narrowly scoped API token.
5. Database and Redis selection. Bundled services are the default; external
   PostgreSQL and Redis URLs must pass a live connection test.
6. Storage selection. Local storage is the default; S3-compatible settings
   must pass a live storage test.
7. Review of the final settings.
8. Installation progress streamed over SSE.
9. Final health, readiness, public URL, and named Cloudflare Tunnel checks.

The install button is idempotent. A second click returns the existing run,
and a dropped browser connection can reconnect to the persisted setup state.
Failed runs remain resumable and preserve the generated configuration and
provider side effects. A successful setup removes the temporary setup
services, starts the production API, worker, Console, proxy, and optional
Cloudflare Tunnel, then redirects to the dashboard.

## GitHub and Cloudflare security

GitHub App Manifest registration returns App credentials to the server
callback. The setup API stores credentials in encrypted setup state and never
returns them to the Console. The Manifest state is random, hashed at rest,
single-use, and expires quickly. The final owner identity still uses the
server-side GitHub Device Flow; its device code and access token do not enter
the URL, browser storage, or logs.

Cloudflare OAuth uses explicit account and zone scopes. The API token fallback
is accepted only after an account request succeeds. The API creates a named
tunnel, configures ingress to the Stealth proxy with a catch-all 404 rule,
creates a proxied CNAME, and stores the official cloudflared token file with
private permissions. The temporary Quick Tunnel is setup transport only and
is never promoted to production ingress.

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
volumes as a repair action.
