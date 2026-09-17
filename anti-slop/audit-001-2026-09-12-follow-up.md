# Anti-slop audit 001 follow-up

Date: 2026-09-13
Mode: AFTER
Scope: Stealth Console frontend and shared UI primitives
Approved findings: 1, 2, 3, 4, 5, 6, 7, 8

## Resolution

### 1. Em dash in user-facing fallbacks

Status: FIXED

Replaced UI fallback em dashes with `Not available` in shared formatters and
the affected API key, database, function, messaging, organization, and
webhook views. The data-value regression test now asserts the new label.

### 2. Mobile control target sizes

Status: FIXED

Shared buttons now use a 44px minimum height and icon buttons use a 44px square
target. Inputs, selects, pagination controls, sidebar navigation, dialog close,
context selectors, sortable headers, and storage object actions were updated to
preserve the same minimum target where they bypass the shared Button component.

### 3. Secondary text contrast

Status: FIXED

The Console theme now promotes `slate-500`, `slate-600`, and `slate-700` to
`#a1adbc`, `#8d98a9`, and `#7d8ca0`. These colors provide at least 4.5:1
contrast for normal text on the page, panel, and subtle dark surfaces used by
the Console. The production CSS contains the same resolved theme variables.

### 4. Organization overview data states

Status: FIXED

The overview now shows loading while either query is pending, a retryable error
for organization or plan failures, and an explicit empty state when either
response is missing. The project list keeps its own independent rendering and
state. Three focused tests cover loading, error, and successful data states.

### 5. Dropdown keyboard focus

Status: FIXED

Radix dropdown items now expose a visible cyan highlighted background, text, and
inset ring through `data-[highlighted]`, so keyboard navigation has a reliable
focus indicator without depending on hover.

### 6. Design direction and dials

Status: FIXED

Added `DESIGN.md` documenting the existing dark operational-console direction,
audience, tone, palette, typography, composition, accessibility constraints,
and explicit dials: Energy 1/5, Rhythm 2/5, Motion 1/5.

### 7. Gradient and grid texture purpose

Status: FIXED

Removed the global radial gradient and authentication-layout grid texture. The
design direction records that these effects are intentionally excluded because
they do not improve operator comprehension.

### 8. Card elevation rationale

Status: FIXED

Removed the large shadow from the default Card primitive. Dialogs, menus, and
service overlays retain explicit elevation because they are transient layers
that must sit above page content. This boundary is recorded in `DESIGN.md`.

## Verification

- `npm run api:generate`: passed; generated schema unchanged.
- `npm run typecheck`: passed.
- `npm run lint`: passed.
- `npm test`: passed, 31 files and 131 tests.
- `npm run build`: passed.
- `git diff --check`: passed.
- `rg '—' console/src`: no matches.
- Full Playwright E2E remains environment-blocked: Chromium cannot launch
  because `libatk-1.0.so.0` is unavailable, and the runner has no passwordless
  `sudo` to install the missing OS dependency. No click-through pass is claimed.
