# Plan 27.6 — EPG grid UI — implementation

> Implementation plan for [story-27-06-epg-grid-ui.md](story-27-06-epg-grid-ui.md).
> Self-contained. Cross-links: consumes the guide API
> ([Plan 27.4](plan-27-04-epg-generation.md)); tunes via the live player
> ([Plan 27.7](plan-27-07-live-channel-player.md)); reuses the design
> system + `web/src/lib/keyboard` D-pad layer (Epic 17/18). **Adds no
> migration, no API** (read-only over 27.4).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **One shared guide data hook (`useGuide`) + a presentational grid**; native apps reuse the hook contract, not the React DOM. | Story spans web + native; the data/interaction contract is shared, the renderer per-platform. |
| D2 | **Virtualised 2-D grid** (windowed rows × time columns); only on-screen cells mount. | Story AC10 — 50×24 h must stay smooth. |
| D3 | **Now-line uses server time** from the payload, reconciled to the client clock. | Story EC4 — a wrong client clock must not misplace "now." |
| D4 | **Cell click is context-sensitive: airing → tune; future → details.** | Story AC3 — you can't watch the future; live is the only tune target. |
| D5 | **Mobile renders a "now & next" vertical list, not the grid.** | Story AC6 — a 2-D grid is unusable on a phone. |
| D6 | **D-pad focus via the shared keyboard layer**; focus always visible, never trapped. | Story AC7 — TV is a first-class target. |

---

## 1. Files (web)

```
web/src/pages/Guide.tsx              # route: the EPG page (grid / now-list switch by viewport)
web/src/components/guide/
├── GuideGrid.tsx                    # virtualised channel×time grid (D2)
├── GuideRow.tsx                     # one channel row of program cells
├── ProgramCell.tsx                  # one block; width ∝ duration; airing/progress states
├── NowLine.tsx                      # server-time now indicator (D3)
├── WhatsOnNowList.tsx               # mobile vertical view (D5)
├── ProgramDetailsPopover.tsx        # hover/focus/long-press details (AC4)
├── CategoryFilter.tsx               # AC5
└── useGuide.ts                      # shared data hook: fetch + live refresh (D1)
web/src/components/guide/__tests__/  # RTL + interaction tests
```

## 2. Data hook (`useGuide.ts`, D1/D3)

```ts
function useGuide(range: TimeRange, opts: { category?: string }) {
  // GET /api/channels/guide?start&end&category — channels × blocks
  // returns { channels, blocksByChannel, serverNow, horizonUntil }
  // live refresh: poll/SSE every few seconds; reconcile serverNow → client offset (D3)
}
```

The same response shape feeds the native renderers (the contract, not the
component, is the deliverable for apps).

## 3. Grid mechanics (`GuideGrid.tsx`, D2/D4)

- Time → x via a px-per-minute scale; a `ProgramCell`'s width =
  `duration_min × scale`. Horizontal scroll = time travel; lazy-load more
  blocks past the loaded window up to `horizonUntil` (then a boundary
  marker, Story EC1).
- Virtualisation: render only channel rows in the vertical viewport and
  only cells in the horizontal time window.
- `ProgramCell` click: if `is_live` → navigate to `/live/{number}`
  ([27.7](plan-27-07-live-channel-player.md)); else open details (D4).
- Filler/bumper blocks arrive pre-collapsed from the API
  ([27.4](plan-27-04-epg-generation.md) AC10); the cell renders an "Up
  Next" chip rather than slivers (Story EC2).

## 4. Responsive + D-pad (D5/D6)

- A viewport hook switches `GuideGrid` ↔ `WhatsOnNowList` (mobile default
  = list).
- D-pad: register a focus grid with `web/src/lib/keyboard`; arrows move
  cell↔cell and row↔row, `OK` = cell action, `Back` exits. Focus ring
  always visible; auto-scroll to keep focus on-screen.
- i18n/RTL: time axis direction mirrors in RTL locales (Story AC11/TC11).

## 5. Files to create / modify

**Create:** everything under `web/src/components/guide/`, `Guide.tsx`,
tests.

**Modify:**
- `web/src/` router — add the `/guide` route.
- Home/nav — entry point to the guide.
- Native apps (`apps/{tv,mobile,desktop}`) — implement the renderer
  against the shared `useGuide` contract (tracked under Epic 18 surface;
  this plan defines the contract + web reference renderer).

## 6. Dependencies

- **27.4** (guide API), **27.7** (tune target), design system +
  `lib/keyboard` (Epic 17/18). No migration, no new API.

## 7. Test strategy

Component tests: proportional cell widths, now-line position with a fixed
clock, airing-cell tune vs. future-cell details, category filter,
mobile list view, D-pad navigation, virtualisation (off-screen cells
absent from DOM), RTL, empty/horizon-end states.
