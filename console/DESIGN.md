# Stealth Console visual direction

## Read

Stealth Console is a technical control plane for operators managing real services,
deployments, data, and signals. Its visual language is a midnight precision
instrument: quiet, dense enough for operations, and precise enough to support
fast scanning. The dials are ENERGY 1, RHYTHM 2, and MOTION 1.

## Decisions

- The fixed dark theme uses Void, Carbon, and Obsidian because operators read
  this product as an always-on control surface, not a marketing page.
- Acid Lime is reserved for primary actions and active navigation because a
  single high-contrast accent makes the next important action unambiguous.
- Inter Variable is the primary typeface because its compact forms remain
  readable in dense tables, forms, and navigation at ordinary UI sizes.
- Berkeley Mono or a system monospace fallback is limited to IDs, shortcuts,
  timestamps, and other technical values because those values benefit from
  stable character widths.
- Graphite and Smoke hairlines separate surfaces because geometry communicates
  hierarchy more calmly than stacked shadows in a monitoring console.
- Six-pixel controls and twelve-pixel cards provide a small, deliberate radius
  vocabulary that distinguishes interaction targets from content surfaces.
- Four-pixel spacing increments and a 1200px content measure keep resource
  pages aligned while allowing tables and operational panels to remain usable.
- Forty-four-pixel minimum interactive targets keep dense controls usable by
  touch and keyboard without changing the compact visual rhythm.
- Motion is limited to hover, focus, state, and loading feedback so transitions
  explain interaction without competing with live operational data.
- Existing Stealth routes, copy, data states, and icon meanings remain the
  product identity. The visual reference informs restraint and precision, not
  branding, content, or page cloning.
- The services canvas keeps its bounded grid because the grid is a navigation
  aid for positioning real resources, not a page-wide decorative background.

## State and accessibility commitments

- Loading, empty, error, disabled, and success states remain visible and
  actionable through existing shared components.
- Keyboard focus uses a visible Acid Lime ring, and the auth and console
  layouts expose skip links before their main content.
- Tables retain horizontal scrolling on narrow screens instead of clipping
  data. Controls remain reachable and usable at mobile widths.
