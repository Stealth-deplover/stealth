# Anti-slop audit 006 follow-up

Date: 2026-09-20
Mode: DURING approved remediation
Audit source: `56a11eaef4525c0f369e8871c86521fd6fa7dd7d`
Scope: findings 1 through 4 from
`anti-slop/audit-006-2026-09-20.md`

## Resolution

### 1. Route and shell loading announcements

Status: FIXED

`console/src/components/feedback/loading-state.tsx` now supports custom
labels, custom visual children, and a shared `role="status"`/polite live
region. The Console shell, all auth Suspense fallbacks, and the Services
canvas dynamic-import fallback now use that primitive while preserving their
existing skeleton dimensions and visual language.

The expired-session shell state announces the redirect message through the
same status region. The default `aria-label="Loading"` remains compatible
with existing consumers while the visible-for-assistive-technology text uses
the typographic ellipsis.

Regression coverage was added to
`console/src/components/feedback/loading-state.test.tsx` and the existing
overview loading test continues to pass.

### 2. Admin time-range focus contrast

Status: FIXED

The custom From/To datetime controls and Refresh select in
`console/src/features/admin/admin-time-range.tsx` no longer use the low
contrast `focus:border-smoke` treatment or unconditional `outline-none`.
They now use the established acid-lime focus border and a visible
focus-visible ring with a void offset. The full `#e4f222` accent measures
`16.15:1` against the void background and `15.44:1` against carbon, which
clears the 3:1 non-text indicator threshold.

`console/src/features/admin/admin-time-range.test.tsx` asserts the approved
focus classes for all three controls.

### 3. Authentication and confirmation error announcements

Status: FIXED

Async failures in Login, Register, password reset, password recovery, and
`ConfirmDialog` now render with `role="alert"`. Client-side authentication
field errors now set `aria-invalid` and point to their rendered messages with
`aria-describedby`.

The existing visible copy and authentication privacy behavior are unchanged.
`console/src/components/confirm-dialog.test.tsx` verifies that an async
confirmation rejection is exposed as an alert.

### 4. CreateDialog native form metadata

Status: FIXED

`console/src/components/create-dialog.tsx` now emits native `name`
attributes for text, textarea, select, and multiselect controls. It also
accepts optional field-level `autoComplete` metadata and passes it to the
native controls. Multiselect checkboxes share the field name so their native
grouping remains coherent while React state handling is unchanged.

`console/src/components/create-dialog.test.tsx` verifies names for input,
select, and checkbox controls plus an email autocomplete hint.

## Validation

- Focused remediation tests: PASS, 4 files and 16 tests.
- Full Console tests: PASS, 42 files and 166 tests.
- Console format check: PASS.
- Console typecheck: PASS.
- Console lint: PASS.
- Console production build: PASS. API client generation produced no
  unintended tracked source change.
- `git diff --check`: PASS.
- Static follow-up scan: all route/shell/dynamic skeleton fallbacks now sit
  inside `LoadingState`; no `focus:border-smoke` time-range treatment
  remains; the approved auth/confirmation mutation paths expose alerts.
- Local browser click-through: NOT RUN because the available Playwright
  browser cannot start without `libatk-1.0.so.0`.
- Production Compose Smoke: not rerun locally because this remediation only
  changes Console frontend code and the local environment has no Docker
  daemon. The previous remote PR checks were green before this change; new
  remote CI is required after push.

## Delivery gate

- R-27 and C-4: PASS for the approved loading and async-error states through
  shared status/alert semantics.
- R-32: PASS for the approved time-range controls through an explicit
  high-contrast focus border and focus-visible ring.
- R-25: PASS for the checked acid-lime focus border against void and carbon.
- R-03: no touch-target regression; existing 44 px controls remain intact.
- R-35: PARTIAL. Automated tests/build pass; local browser click-through is
  unavailable because of the missing system library.
- No backend, API, database, telemetry, or unrelated audit finding was
  changed.

## Handoff

All four findings approved from Audit 006 are implemented. The changes are
ready for commit/push to the existing PR; no merge was performed.
