# Anti-slop audit 003 follow-up

Date: 2026-09-13
Mode: AFTER
Scope: Stealth Console frontend, navigation, operational copy, backend
surfaces, and product-facing documentation
Approved findings: 1, 2, 3, 4, 5
Fix commit: `690a2d0`

## Resolution

### 1. Resource detail loading states

Status: FIXED

Added explicit `LoadingState` branches to the site, site deployment,
function, and function deployment detail views. The primary query now shows
loading progress before the completed-response not-found branch. Existing
retryable error states and completed-response empty states remain intact.

### 2. Navigation loading and error states

Status: FIXED

Organization and project switchers now distinguish loading, failed, and
settled-empty responses. Their error states include a working Retry action.
The command palette now reports dynamic workspace data while it loads and
when it fails, while retaining static navigation commands and a Retry action.
Command-palette keyboard handling is limited to the search input so the Retry
button keeps its own Enter and click behavior.

### 3. Log text containment on narrow screens

Status: FIXED

The log viewport now hides horizontal overflow at its boundary, and the log
message column is shrinkable, flexible, and allowed to break long unbroken
values. Timestamps and levels retain their fixed readable columns.

### 4. Mobile navigation keyboard behavior

Status: FIXED

Replaced the hand-rolled mobile overlay with the existing Radix-backed Dialog
primitive. The dialog now provides semantic title and description, Escape
closing, focus containment, visible close control, and explicit focus return to
the mobile menu trigger. Navigation destinations and the desktop sidebar are
unchanged.

### 5. Product-facing copy punctuation

Status: FIXED

Replaced U+2014 punctuation in the CLI usage and purge label, `SECURITY.md`,
and the documentation index with plain punctuation. A repository scan now
finds the character only in `console/AGENTS.md`, where it is part of generated
tooling instructions and outside the product-facing copy scope.

## Verification

- `go mod verify`: passed.
- `go vet ./...`: passed.
- `go test ./...`: passed.
- `go test -race ./...`: passed.
- Go builds for API, worker, and CLI: passed using temporary output paths
  under `/tmp`; no generated binaries were added to the repository.
- `npm run api:generate`: passed and generated API output is unchanged.
- `npm run typecheck`: passed.
- `npm run lint`: passed.
- `npm run test -- --maxWorkers=1`: passed, 31 files and 131 tests.
- `npm run build`: passed.
- Prettier checks for every changed Console source file: passed.
- `git diff --check`: passed.
- No release tag was created or modified.

## Browser verification

The local Playwright smoke run was attempted against the production build but
could not launch Chromium because the runner lacks `libatk-1.0.so.0`. All four
smoke tests stopped at browser startup, so local click-through evidence is
not available. The production build and source-level keyboard review passed;
remote CI must provide the browser click-through evidence for this commit.

## Re-audit

- R-02: product-facing copy no longer contains U+2014; the remaining match is
  the documented tooling instruction in `console/AGENTS.md`.
- R-27: the four audited resource detail views and navigation overlays now
  expose explicit loading, empty, and error paths.
- R-03: log messages have a bounded viewport and long-token wrapping policy.
- R-32: mobile navigation uses the shared dialog behavior and restores focus;
  command-palette Enter handling no longer intercepts the Retry button.
- R-01, R-04, R-07, R-10, R-12, R-13, R-17, R-18, R-22, R-24, R-25, R-26,
  R-29, R-30, R-34, R-36, R-37, and R-38: no regression was found in the
  previously passing checks.

## Delivery gate

- R-02: PASS by source scan of product-facing copy.
- R-03: PASS by bounded log layout and production build; browser confirmation
  is pending because Chromium cannot start in this runner.
- R-27: PASS by explicit loading, empty, and error branches in the audited
  views.
- R-32: PASS by shared Dialog semantics, Escape handling, focus containment,
  and focus return in source; browser confirmation is environment-limited.
- R-35: PARTIAL. The app was built and unit-tested, but local click-through
  could not run because of the missing browser library.
- C-1 through C-5 and the remaining quality locks: PASS based on the previous
  audit evidence and this focused re-audit.

The product findings are fixed. Full browser delivery evidence remains an
environment limitation, not an unaddressed product finding.
