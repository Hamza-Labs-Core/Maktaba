# Story 34.6 — Channel switching (controls and speed)

## Description

*As a viewer, I want to flip channels like on a TV — up/down, a number, last channel, a tap in the
guide or a swipe on my phone — and see the new channel almost at once, never a black screen. The same
player and switching should work in the Maktaba app (Epic 27.7) and for remote viewing (Epic 32.9).*

Design: [design.md](design.md) §8; README R6.

## Acceptance criteria

- **AC1 Controls.** On-screen up/down buttons; vertical swipe on the video (mobile); keys ↑/↓ and
  PageUp/PageDown; digits with a number overlay committed by Enter or 1.5 s idle; `L`/Backspace last
  channel; `G` guide; `I` or tap → mini-guide overlay with now/next for every channel (tap to tune);
  tap a tile or a guide cell; TV remote CH+/CH−, D-pad and number keys through the same key map. In RTL,
  "up = next channel" is kept.
- **AC2 Deep links.** Every switch updates the URL to `/<lang>/watch/<slug>` (history `replace` for
  rapid surfing, `push` when settled ≥ 5 s) so back/forward and sharing work; a cold load of that URL
  plays the channel.
- **AC3 Tuning card.** Within one frame (< 100 ms) of the input the target's card (logo, number, now,
  next) appears over the old picture; it stays until the new channel's first frame.
- **AC4 Make before break.** The old channel keeps playing until the new one has buffered its first
  segment and fired `canplay`; then the player swaps and stops the old one (its session is ended).
- **AC5 Neighbour manifests.** After a channel settles, the player has tokens for previous, next and
  last channel (from the `play`/`beat` response) and prefetches their **playlists only**, refreshed
  every 10 s; it never requests their segments (segments are billed; playlists are not).
- **AC6 Debounce.** Holding or repeating up/down moves the card immediately and commits the tune
  300 ms after the last press; at most one tune request is in flight.
- **AC7 Targets.** Warm channel (default: every public channel is always-on): first frame < 2 s after
  the input (p75 broadband, measured by the player). Cold channel (`tiering = top_n`): card immediately,
  "Starting channel…" and video < 20 s (spike value recorded in 34.3).
- **AC8 Shared module.** `@maktaba/tvplayer` is used by the public site, the Maktaba web live player
  (27.7 surfing ACs are met by it) and Epic 32.9; each host app supplies `resolve(channel)`.
- **AC9 Telemetry.** Each switch emits `switch{ms_to_card, ms_to_first_frame, path, prefetched}`; the
  admin usage panel shows p50/p75/p95.

## Test cases

- **TC1** `keymap.test.ts` (all keys, RTL, TV key codes), `swipe.test.ts`.
- **TC2** `switcher.test.ts` with a fake hls.js: make-before-break order, debounce, one request in flight.
- **TC3** `prefetch.test.ts`: playlists fetched, no segment URL ever requested for neighbours.
- **TC4** Playwright `switching.spec.ts` against a local HLS fixture: warm switch < 2 s, no black frame
  (canvas sample between frames).
- **TC5** Maktaba web: the 27.7 `test_channel_up_down` / digits tests pass with the shared module.

## Edge cases

- **EC1** Switch target is off air: card shows the reason; after 3 s the player returns to the previous
  channel unless the user moves on.
- **EC2** Number not in the lineup: "No channel 47" for 1.5 s, nothing tuned.
- **EC3** Token expired on a prefetched neighbour: `resolve` fetches a fresh one before loading.
- **EC4** Background tab: neighbour playlist refresh pauses; it resumes on focus.

## Dependencies

34.4 (play/beat with neighbours), 34.5; Epic 27.7; Epic 32.9.
