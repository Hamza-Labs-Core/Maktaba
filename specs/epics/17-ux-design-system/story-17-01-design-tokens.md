# Story 17.1 — Design tokens (colors, typography, spacing)

A token registry exported as CSS custom properties (web), JSON (Capacitor
plugin), Swift `struct` (tvOS), and Kotlin `object` (Android TV).

**Anchors:** [`architecture.md` §11](../../architecture.md).

## AC

- Token domains: color, typography, spacing, radius, elevation, motion,
  z-index, breakpoints.
- Color: brand palette + semantic tokens (`--color-bg`, `--color-fg`,
  `--color-accent`, `--color-success`, `--color-warn`, `--color-error`).
  Light + dark variants.
- Typography: 4 type roles (display, body, mono, transcript). Arabic
  font (`IBM Plex Sans Arabic`), Latin font (`Inter`); fallback stack
  documented.
- Spacing: 4 px base unit; 4, 8, 12, 16, 24, 32, 48, 64, 96 px scale.
- Single source of truth: `design/tokens/tokens.json`; build pipeline
  generates the four target outputs.
- Versioned: bumping a token bumps the design-system semver; clients
  pin a major version.

## TC

- Change a brand color in `tokens.json`: web, iOS, Android, tvOS,
  AndroidTV all rebuild with the new color.
- Switch theme dark → light: every token resolves correctly per token
  set.
- Generate the Swift output: compiles with the tvOS target.

## EC

- A native target requests a token that doesn't exist: the build fails
  loud, never falls back to a hard-coded default.
- A user's high-contrast OS mode: token set is overridden by a separate
  `tokens.high-contrast.json`.
- Token rename mid-version: shipped as a deprecated alias for one major.
