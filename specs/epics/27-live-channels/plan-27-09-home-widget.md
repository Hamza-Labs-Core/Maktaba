# Plan 27.9 — "What's On Now" home widget — implementation

> Implementation plan for [story-27-09-home-widget.md](story-27-09-home-widget.md).
> Self-contained. Cross-links: powered by `GET /api/channels/now`
> ([Plan 27.4](plan-27-04-epg-generation.md)); Tune In jumps to the live
> player ([Plan 27.7](plan-27-07-live-channel-player.md)); reuses the
> existing home-screen rail pattern (Epic 14 discovery rails). **Adds no
> migration; no new API** (consumes the `/now` payload, shape owned with
> 27.4).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Rail consumes the cheap `/now` read path; rendering the home screen starts no transcode.** | Story AC3 — the home screen must never spin up FFmpeg just to show cards. |
| D2 | **Reuse the existing discovery-rail component**; the live rail is a new data source, not a new layout. | Epic 14 rails already handle horizontal scroll, virtualisation, focus; the live rail is just another rail. |
| D3 | **Progress computed from server time** in the payload. | Story EC2 — a wrong client clock must not produce >100 %/negative progress. |
| D4 | **Rail hidden entirely when there are no accessible channels.** | Story AC5/EC5 — no placeholder for an unconfigured feature. |
| D5 | **Shared rail contract across platforms**; web reference + native renderers. | Story AC6 — all platforms, one data contract. |

---

## 1. Files (web)

```
web/src/components/home/
├── WhatsOnNowRail.tsx     # the rail: cards from /now (D1/D2)
├── ChannelNowCard.tsx     # logo + current poster/title + progress + "Next:" + Tune In (AC1/AC2)
├── useWhatsOnNow.ts       # GET /api/channels/now + live refresh (D1/D3)
└── __tests__/
```

## 2. Data hook (`useWhatsOnNow.ts`, D1/D3)

```ts
function useWhatsOnNow() {
  // GET /api/channels/now → [{ channel, current, next, progress, serverNow }]
  // live refresh every few seconds; recompute progress from serverNow (D3);
  // returns [] → caller hides the rail (D4)
}
```

## 3. Card + Tune In (`ChannelNowCard.tsx`, AC1/AC2)

Renders number + logo + current program poster/title, a progress bar, and
a "Next: …" line; the whole card is the Tune In target → navigate to
`/live/{number}` ([27.7](plan-27-07-live-channel-player.md)). A degraded
channel shows a "No content right now" state but still tunes (EC1).

## 4. Placement, cap, refresh (AC4/AC7)

- Mount the rail among the home discovery rails (position configurable).
- Cap the number of cards; a "Open guide" affordance links to the EPG
  ([27.6](plan-27-06-epg-grid-ui.md)) when capped.
- Live refresh swaps card content on program rollover within a few
  seconds (no full home reload).

## 5. Files to create / modify

**Create:** everything under `web/src/components/home/`, tests.

**Modify:**
- The home page — include `WhatsOnNowRail` (rendered only when non-empty,
  D4).
- Native apps — implement the rail against the shared `useWhatsOnNow`
  contract (Epic 18 surface).

## 6. Dependencies

- **27.4** (`/now`), **27.7** (live player tune target), Epic 14 rail
  component. No migration, no new API.

## 7. Test strategy

Rail renders current+next+progress; Tune In navigates live; **no
transcode on render** (integration assertion against the streaming
runtime registry — rendering issues only the `/now` read); live rollover
swap; progress from server time; hidden when empty; cap + "Open guide";
ACL scoping; RTL.
