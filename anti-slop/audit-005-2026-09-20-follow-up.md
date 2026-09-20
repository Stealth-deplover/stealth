# Anti-slop audit 005 follow-up

Date: 2026-09-20
Mode: DURING approved remediation
Audit source: `82020b2f39a317b68cb6029a22fbbac506262588`
Scope: approved findings 1 through 5 from
`anti-slop/audit-005-2026-09-20.md`

## Resolution

### 1. Shared loading announcements

Status: FIXED

`console/src/components/feedback/loading-state.tsx` now exposes the shared
skeleton state as a polite, atomic `status` live region with `aria-busy` and
an accessible `Loading…` message. The existing `aria-label="Loading"` is
retained for compatibility with current consumers and tests.

`console/src/components/data-table.tsx` now exposes a single `Loading table…`
status while preserving the table headers and visual skeleton rows. Individual
skeleton cells are not announced as status messages.

Regression coverage was added in:

- `console/src/components/feedback/loading-state.test.tsx`
- `console/src/components/data-table.test.tsx`

### 2. Telemetry chart alternatives

Status: FIXED

`console/src/features/admin/admin-log-volume-chart.tsx` and
`console/src/features/admin/admin-metric-chart.tsx` now give the canvas chart
an explicit image role, a meaningful label, and a concise accessible summary.
Each chart also has a collapsed native disclosure containing a semantic data
table with the same timestamps and values. The metric description states when
the visual chart is limited to eight series while the table retains the full
returned data set.

Regression coverage was added in:

- `console/src/features/admin/admin-telemetry-charts.test.tsx`

The disclosure uses native `details`/`summary` semantics and a 44 px minimum
summary height, so it remains keyboard and touch operable without changing the
existing chart visual language.

### 3. Admin touch targets

Status: FIXED

The Admin Audit `Load older activity` action now has an inline-flex 44 px
minimum hit area. The Admin Traces trace-ID action now has a 44 px minimum
height and width with centered content. The compact text and badge visuals are
preserved while the touch target follows the shared `Button` contract.

Changed files:

- `console/src/features/admin/admin-audit-view.tsx`
- `console/src/features/admin/admin-traces-view.tsx`

### 4. Live-tail status announcement

Status: FIXED

`console/src/features/admin/admin-logs-view.tsx` now uses the exported
`AdminLogTailStatus` presenter. Connection state and stream errors are exposed
through one atomic polite status region, preventing a screen-reader user from
missing the transition from connecting to streaming or disconnected/retrying.

Regression coverage was added to:

- `console/src/features/admin/admin-logs-view.test.tsx`

### 5. Dashboard telemetry recovery

Status: FIXED

`console/src/features/admin/admin-dashboards-view.tsx` now uses
`TelemetryPanelError` for metric, log, and monitor-status panels. The panel
error is announced with `role="alert"` and includes a panel-local `Retry`
button, including when refresh is disabled. Existing polling behavior is
unchanged.

Regression coverage was added in:

- `console/src/features/admin/admin-dashboards-view.test.tsx`

## Validation

- Focused remediation tests: PASS, 5 files and 7 tests.
- Full Console tests: PASS, 41 files and 163 tests.
- Console typecheck: PASS.
- Console lint: PASS.
- Console format check: PASS.
- Production build: PASS. API client generation produced no unintended
  source change.
- `git diff --check`: PASS for tracked changes.
- No backend, API, database, telemetry architecture, or unrelated audit
  finding was changed.

## Delivery gate

- R-03: PASS by adding 44 px hit areas to the two approved Admin actions and
  the chart data disclosures.
- R-27 and C-4: PASS for the approved loading, chart, live-tail, and dashboard
  states through semantic announcements, explicit data alternatives, and
  retry actions.
- R-32: PASS by using native table/disclosure semantics, a status live region,
  and existing visible focus styles.
- R-35: PARTIAL. The application built and all automated tests passed, but a
  local browser click-through was not run because the available Playwright
  browser cannot start without `libatk-1.0.so.0`. No manual browser result is
  claimed.
- R-01, R-04, R-06, R-07, R-08, R-09, R-10, R-11, R-12, R-13, R-14, R-17,
  R-18, R-19, R-22, R-23, R-24, R-25, R-26, R-28, R-29, R-30, R-31, R-33,
  R-34, R-36, R-37, and R-38: no new violation was introduced by the approved
  remediation.

## Handoff

The approved findings 1 through 5 are implemented locally. The current
worktree has not been merged automatically. CI after these changes and any
remote PR update remains the final external validation step.
