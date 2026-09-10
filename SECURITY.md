# Security Policy

Stealth handles sessions, API keys, provider credentials, tenant data, and—when enabled—the Docker-backed Function/Site runner. Please report security issues responsibly.

## Reporting a vulnerability

Do not open a public issue with exploit details, credentials, or sensitive logs.

If GitHub private vulnerability reporting is enabled for this repository, use **Security → Advisories → Report a vulnerability**. A private security-advisory channel is the preferred route. No private security email address is currently published. If private reporting is unavailable, contact the repository maintainers privately through GitHub and request a secure channel; use a public issue only to ask for contact without disclosing details.

Please include the affected commit or release, deployment mode, a minimal reproduction, impact, and any mitigations. Redact secrets and personal data. Give maintainers reasonable time to investigate before public disclosure.

## Deployment reminders

Never commit `.env` files, session secrets, API keys, provider credentials, or private values in `NEXT_PUBLIC_*` variables. Review the [production deployment guide](docs/production-deployment.md), especially the Docker socket and `TRUSTED_PROXY_CIDRS` trust boundaries.

Maintainers should add a dedicated private security contact here when one is established.
