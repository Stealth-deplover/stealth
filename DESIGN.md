# Stealth Console design direction

This document records the visual direction already present in the Stealth
Console. It is a product constraint for future UI work, not a request for a
visual redesign.

## Product identity

Stealth is an operational developer control plane. The interface should feel
calm, precise, and trustworthy while helping an operator inspect and change
infrastructure state.

## Audience and tone

The primary audience is developers and platform operators. Use direct labels,
specific status language, and compact supporting text. Prefer useful context
over decorative copy.

## Visual language

- Use a dark neutral background with slightly lighter grouped surfaces.
- Use cyan as the primary action and focus accent.
- Use violet, green, amber, red, and blue only for distinct semantic states.
- Use resource and connection icons to explain operational meaning; avoid
  decorative glyphs that do not identify the state or action.
- Use system sans for interface text and monospace for IDs, URLs, and logs.
- Keep motion subtle and useful. Respect `prefers-reduced-motion`.

## Composition

Organize each view around one clear operational task. Use grouped panels for
related data, predictable spacing, and responsive layouts that keep actions
reachable on small screens. Empty, loading, and error states are part of the
composition, not afterthoughts.

## Interaction dials

- Energy: 1/5. The console should be calm rather than promotional.
- Rhythm: 2/5. Use compact data groupings with enough space to scan.
- Motion: 1/5. Use transitions for state changes only; no decorative motion.

## Deliberate constraints

- No global gradients or grid textures. They add visual noise without helping
  an operator understand system state.
- Cards provide grouping and boundaries by default. Elevation is reserved for
  overlays and transient layers that must sit above the page.
- Focus, hover, and disabled states must remain understandable without color
  alone.
- Interactive targets should be at least 44 pixels high or wide where the
  control is icon-only.
