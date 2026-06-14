# Plan 27.7 — Live channel player — implementation

> Implementation plan for [story-27-07-live-channel-player.md](story-27-07-live-channel-player.md).
> Self-contained. Cross-links: tunes via `POST /api/channels/{id}/tune`
> → live HLS ([Plan 27.3](plan-27-03-live-stream-engine.md)); mini-guide
> reuses the `/now` payload ([Plan 27.4](plan-27-04-epg-generation.md));
> builds on the existing `web/src/pages/VideoPlayer.tsx` + `lib/keyboard`.
> "Watch from beginning" opens an ordinary on-demand session (Epic 8).
> **Adds no migration, no new API** beyond `tune` (owned by 27.3).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Live player is a distinct mode of the existing player**, not a fork — same HLS engine, different chrome (LIVE indicator, no live scrub bar, surf/banner). | Reuse the proven player; live just changes affordances. |
| D2 | **Channel surfing leans on the warm-window instant re-tune** (27.3 D5) and debounces rapid surfing so only the landed channel actually tunes. | Story AC2/EC2 — surfing must be fast and must not spawn an encoder per skipped channel. |
| D3 | **Tune banner + mini-guide read `/now`**, not the heavy guide grid. | Cheap, current-state overlays; the grid is the full EPG ([27.6](plan-27-06-epg-grid-ui.md)). |
| D4 | **"Watch from beginning" = on-demand session for the program's `video_id` at offset 0**, with a "return to live" re-tune. | Story AC5 / 27.3 EC8 — never perturb the shared live timeline; reuse VOD. |
| D5 | **PiP is feature-detected per platform**; unsupported → control hidden. | Story AC6 — no broken affordances. |

---

## 1. Files (web)

```
web/src/pages/LivePlayer.tsx          # route /live/{number}: live mode of the player (D1)
web/src/components/live/
├── LiveControls.tsx                  # LIVE badge, surf buttons, return-to-live, PiP (D1/D5)
├── TuneBanner.tsx                    # channel + current/next + progress; auto-hide ~5s (AC3)
├── MiniGuide.tsx                     # overlay /now list over playback (AC4/D3)
├── ChannelSurf.ts                    # up/down + number-entry state machine (D2)
├── useChannelTune.ts                 # tune() → manifest; warm re-tune aware (D2)
└── __tests__/
```

## 2. Surfing state machine (`ChannelSurf.ts`, D2)

```ts
// channel-up/down move an index over the lineup (wrap per config);
// number entry accumulates digits with a ~1.5s commit timeout → tune.
// rapid surfing: only tune the channel the user rests on for > DEBOUNCE ms;
// intermediate channels show guide-preview (from /now), not a tune (EC2).
```

`useChannelTune` calls `POST /api/channels/{id}/tune`, swaps the HLS
source, and (thanks to 27.3's warm window) a re-tune within the grace
window is instant.

## 3. Overlays (`TuneBanner.tsx` / `MiniGuide.tsx`, D3)

- **Banner:** on every tune, fetch the channel's `/now` slice (current +
  next + progress), render number/logo/name/current/next/progress, start
  a ~5 s auto-hide timer; `Info`/`OK` re-summons; suppressed while the
  mini-guide is open (Story EC3).
- **Mini-guide:** overlay the `/now` list across all accessible channels
  on top of still-playing video; selecting a row tunes; dismiss returns.

## 4. Watch-from-beginning + PiP (D4/D5)

- "Watch from beginning" resolves the current block's `video_id` (from
  `/now`) and opens a normal VOD session at offset 0; a "return to live"
  button re-tunes the channel at the live edge.
- PiP via the Web Picture-in-Picture API where present; on native, the
  platform PiP; feature-detected, hidden otherwise.

## 5. D-pad / remote (AC8)

Reuse `lib/keyboard`: up/down surf, guide button → mini-guide (long-press
→ full EPG), `OK` toggles banner, left/right scrub only in
watch-from-beginning mode. Mappings shared with the native apps.

## 6. Files to create / modify

**Create:** everything under `web/src/components/live/`, `LivePlayer.tsx`,
tests.

**Modify:**
- `web/src/` router — `/live/{number}` route + deep-link resolution
  (unknown/disabled/forbidden → appropriate state, Story EC6).
- `VideoPlayer.tsx` — extract the shared HLS playback core if needed so
  live mode reuses it without duplication.
- Native apps — implement live player against the shared surf/tune/banner
  contract (Epic 18 surface).

## 7. Dependencies

- **27.3** (`tune` + live HLS + warm re-tune), **27.4** (`/now` for
  banner + mini-guide), Epic 8 (VOD player core for watch-from-beginning),
  `lib/keyboard`. No migration.

## 8. Test strategy

Live-mode chrome (LIVE badge, no live scrub), surf up/down + wrap, number
entry + commit timeout, banner contents + auto-hide + re-summon,
mini-guide overlays without stopping playback, watch-from-beginning opens
VOD + return-to-live, PiP feature-detect, degraded-channel slate,
auto-rejoin on stall, D-pad mappings, and the surf-debounce assertion
(rapid surf doesn't tune intermediate channels).
