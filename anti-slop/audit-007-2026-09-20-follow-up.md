# Anti-slop audit 007 follow-up

Date: 2026-09-20
Mode: AFTER approved remediation
Audit source: `69fe79dc51fe70bdc9f0c6f92506e10b78559dfd`
Scope: findings 1 through 5 from `anti-slop/audit-007-2026-09-20.md`

## Design read

Stealth Console remains a midnight precision instrument for operators, with
ENERGY 1 / RHYTHM 2 / MOTION 1. This remediation keeps the existing dark
surface, restrained acid-lime focus treatment, and compact operational layout.
It changes state semantics and native interaction behavior only.

## Resolution

### 1. Organization loading announcement

Status: FIXED

`OrganizationsIndexView` now uses the shared `LoadingState` with the
contextual `Loading organizations…` label while retaining the existing
three-card loading geometry. The loading region remains one polite, atomic
status with `aria-busy`, rather than announcing each skeleton independently.

`organizations-view.test.tsx` renders the real pending-query branch and
asserts the named busy status.

### 2. Secondary query error announcements

Status: FIXED

Added the shared `InlineError` presenter for secondary errors in Admin
Overview, Admin Status Page, and Agent detail. Each message is now an atomic
alert and names a useful recovery action without hiding the usable surrounding
page.

`inline-error.test.tsx` verifies the announced error contract.

### 3. Browser setup field error association

Status: FIXED

Browser setup form errors now have stable ids, are announced as alerts, and
mark the affected input or textarea with `aria-invalid`. Each invalid control
references its rendered field error through `aria-describedby`; controls with
a hint retain both the hint and error descriptions.

`browser-setup-view.test.tsx` verifies the label, invalid state, hint, and
error association together.

### 4. Incident detail action semantics

Status: FIXED

The incident list no longer makes a table row focusable and clickable. The
Incident cell now contains a named native button that opens the same detail
dialog, with the existing visible acid-lime focus ring and a 44 px minimum
height.

`admin-incidents-view.test.tsx` verifies the named button, absence of row
tabindex, and opening of the dialog.

### 5. Organization audit empty state

Status: FIXED

`OrganizationAuditView` now supplies the domain-specific empty message
`No audit events recorded for this organization.` to the shared table.

`data-table.test.tsx` verifies that a domain view can provide its own empty
message without changing generic table behavior elsewhere.

## Validation

- Focused remediation tests: passed, 5 files and 8 tests.
- Full Console tests: passed, 45 files and 171 tests.
- `npm run typecheck`: passed.
- `npm run format:check`: passed.
- `npm run lint`: passed.
- `npm run build`: passed. OpenAPI generation produced no tracked generated
  source change.
- `git diff --check`: passed.
- U+2014 scan of `console/src`: no matches.
- Contrast check: Coral Red `#eb5757` on Carbon `#0f1011` is 5.47:1, passing
  the 4.5:1 normal-text threshold.
- Local Playwright smoke: attempted but blocked before the test body because
  the Chromium executable is absent from the runner. No browser click-through
  result is claimed locally.

## Delivery gate

- R-02: PASS. No new em dash was introduced in Console source.
- R-03: PASS by retaining the existing responsive grid/table behavior and
  using a 44 px native incident action.
- R-25: PASS for the newly used Coral Red error text against Carbon.
- R-26 and R-32: PASS by replacing the custom focusable row with a native,
  labelled button that retains a visible focus ring.
- R-27 and C-4: PASS through the shared named loading state, contextual empty
  state, announced secondary errors, and connected field errors.
- R-35: PARTIAL locally. Build and automated tests pass; Playwright cannot
  start until the runner has the expected Chromium executable. Remote CI must
  provide the browser confirmation after the branch is pushed.
- R-01, R-04 through R-24, R-28 through R-31, and R-33 through R-38: no new
  violation was introduced by this focused remediation.

## Handoff

All five approved Audit 007 findings are fixed locally. No backend, API,
telemetry, data model, or unrelated visual redesign was included. No merge was
performed.
