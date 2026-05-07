# Story 14.3 — 10-foot UI design (large text, D-pad navigation)

A TV-specific layout with large type, focus rings, and predictable
D-pad geometry. Shared design tokens
([Story 17.1](../17-ux-design-system/story-17-01-design-tokens.md)) but
TV-specific spacing scale.

**Anchors:** [`architecture.md` §6.5](../../architecture.md).

## AC

- Minimum body type: 28 pt at 1080p, 36 pt at 4K.
- Focus ring: 4 px outline + soft glow at the brand color; never relies
  on color alone.
- D-pad geometry: every focusable element sits on a predictable grid;
  diagonal moves not required for any flow.
- Rows use horizontal-snap focus; columns use vertical-snap.
- "Back" returns to the previous focus, not the top of the row.
- Safe-area: 5% inset on all four sides; never paint within it.
- All controls reachable with the remote alone; no swipe-only flows.

## TC

- Use only the Apple TV remote / Android TV remote: every flow
  completable.
- Inspect focus traversal on a row of mixed-width cards: focus moves
  predictably.
- Read body text from 3 m at 1080p: legible.

## EC

- A row with one item: focus left/right wraps within the row.
- Back from the player: returns to the detail page, not Home.
- A focus trap (e.g., a modal): the modal's first focusable receives
  focus; back exits the modal.
