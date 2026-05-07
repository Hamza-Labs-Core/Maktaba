# Story 11.8 — Dark / light theme

Theme is `light`, `dark`, or `system` (default). Switching is instant, no
flash of incorrect theme on cold load.

**Anchors:** [`architecture.md` §6.2](../../architecture.md). Depends on
Story 17.1 (design tokens).

## AC

- Theme tokens are defined as CSS custom properties on `:root[data-theme]`.
- `system` honors `prefers-color-scheme` and updates live when the OS
  toggles.
- Theme persists in `localStorage` and is applied before first paint
  (inline blocking script in `index.html`) — no FOUC.
- Posters and thumbnails render correctly on both themes; subtitles
  contrast against the player background regardless of theme.
- All components meet WCAG 2.1 AA contrast in both themes
  (see [Story 11.11](story-11-11-accessibility.md)).
- Switching theme animates background and surface colors over 150 ms; no
  layout shift.

## TC

- Toggle from light → dark → system on macOS and switch macOS theme:
  Maktaba's UI follows.
- Boot the page with `localStorage` empty and `prefers-color-scheme:
  dark`: app boots dark with no flash.
- Print stylesheet: prints in light theme regardless of UI selection.

## EC

- A user-supplied custom CSS overrides a token: warn in DevTools console
  but do not crash.
- Theme key in `localStorage` is corrupted (`"darkk"`): fall back to
  `system`.
- High-contrast OS mode (Windows): forced colors are honored where
  applicable; we do not override `forced-colors: active`.
