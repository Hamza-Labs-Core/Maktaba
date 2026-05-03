# Story 11.7 — Responsive design (desktop, tablet, mobile)

The same React app must render correctly from 360 px (phone portrait) to
2560 px (large desktop), using a mobile-first Tailwind breakpoint scale
and never horizontally scrolling at supported sizes.

**Anchors:** [`architecture.md` §6.2](../../architecture.md). Depends on
Stories 17.1, 17.2 (design tokens, components).

## AC

- Breakpoints: `sm 640`, `md 768`, `lg 1024`, `xl 1280`, `2xl 1536`
  (Tailwind defaults).
- At ≤ 640 px the navigation collapses to a bottom tab bar (Library,
  Search, Queue, Settings); the header search becomes a full-screen
  overlay.
- At 641–1023 px the sidebar collapses to icons; tap to expand.
- At ≥ 1024 px the full sidebar is permanent.
- Video player layout: full-width 16:9 below 768 px; 16:9 with a
  side-by-side transcript at ≥ 1024 px.
- All text scales correctly at 200% browser zoom; no horizontal scroll
  appears at any breakpoint.
- Touch targets ≥ 44 × 44 CSS px on touch devices.

## TC

- Viewport-test matrix: iPhone SE 375 × 667, Pixel 7 412 × 915, iPad 1024
  × 768, MBP 14 1512 × 982, 4K 2560 × 1440. Visual regression suite
  (Playwright + image diff) gates merges.
- Rotate iPad while playing video: the player layout reflows without
  pausing.
- Browser zoom 200% on desktop: layout still readable, no overflow.

## EC

- Foldable Android (split mode 280 px wide): graceful, never broken; show
  a "Maktaba is best at ≥ 320 px wide" hint.
- Browser without `container queries` support: fall back to media queries
  (Tailwind already does so).
- Extreme aspect ratios (ultra-wide 21:9): video letterboxed, transcript
  sidebar wider — never stretches the player.
