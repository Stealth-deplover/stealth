# Anti-slop audit 004 follow-up

Date: 2026-09-20
Mode: DURING approved remediation
Audit source: `634fda65359e8db459cdaf0459facafd97a88d3b`, incorporated into
main as `d7fa45a2e3dc8efc49a4ac966046346858d6bc14`

## Resolution

### 1. Telemetry time-range state

Status: FIXED

`useAdminTimeRange` now uses a stable storage snapshot for normal persisted
preferences and retains a session-only override only when storage writes are
rejected. Preset range, refresh interval, and validated custom timestamps are
all represented in the live hook state. Storage remains optional persistence,
not the source of truth for an in-progress operator choice.

Every Admin view now receives the active custom range through
`AdminTimeRange`, so the request window remains correct after a storage
failure. The regression test makes both `localStorage.getItem` and
`localStorage.setItem` throw and verifies the selected preset, refresh value,
and custom query range remain active.

### 2. Touch targets

Status: FIXED

The shared Admin time-range presets now use `min-h-11` and `min-w-11`.
Datetime inputs, Apply, Refresh, the Admin log-history select, the error
status select, the Infrastructure scope choices, and the project log filter
now use a 44 px minimum height. The preset group may wrap rather than force a
desktop-width control row on a narrow display.

The time-range test asserts the standard touch-target classes for each
interactive control.

### 3. Infrastructure scope semantics

Status: FIXED

Infrastructure scope is now a labelled native-button filter group. The
incorrect tab and tab-list roles were removed, and the active scope is exposed
with `aria-pressed`. This matches the actual query-filter behavior without
claiming tab-panel keyboard conventions that the component does not provide.

The new component test verifies the group role, pressed state, state change,
and minimum touch target.

### 4. Virtualized Admin log semantics

Status: FIXED

The virtualized log result now uses a conformant table structure: a header
row, a row group, row indices, and explicit cells. Detail opening is an
announced native `Open` button in its own column instead of a button whose
role was overwritten to `row`.

The related implementation review found that the detail root omitted the
shared `DialogContent` wrapper. It is now wrapped in `DialogContent`, giving
the explicit action a real Radix dialog with focus handling and a labelled
close control. This was required to make the approved log-detail fix usable,
not a separate product feature.

The new regression test checks table and header semantics, opens a detail
dialog through the named action, and verifies that the close control is
present.

## Validation

- `npm run typecheck`: passed.
- `npm run format:check`: passed.
- Focused Vitest coverage: 3 files, 9 tests passed.
- `npm run test -- --maxWorkers=1`: passed, 37 files and 157 tests.
- `npm run build`: passed and generated `.next/BUILD_ID`; generated OpenAPI
  output was unchanged.
- `git diff --check`: passed.

## Delivery gate

- R-03: PASS by standard 44 px targets and responsive wrapping in the
  remediated controls. Browser-size verification remains a CI follow-up.
- R-26 and C-4: PASS by session fallback when browser storage is unavailable.
- R-32: PASS by native filter semantics and the restored log-table/dialog
  interaction path.
- R-35: PARTIAL locally. The production build succeeded, but Playwright was
  not run in this environment. CI browser coverage remains the delivery
  confirmation.
