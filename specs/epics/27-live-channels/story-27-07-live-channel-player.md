# Story 27.7 — Live channel player

## Description

A dedicated **live player mode** for channels — the "watching TV"
experience, distinct from the on-demand player. It plays a channel's live
HLS ([27.3](story-27-03-live-stream-engine.md)) and adds the affordances
people expect from a TV: **channel surfing** (up/down and number entry),
a **tune banner** overlay that shows what you just tuned to and
auto-hides, a **mini-guide** overlay to peek at other channels without
leaving the current one, **"watch from beginning"** for the current
program, and **PiP** where the platform supports it.

This story owns the web player (React, building on the existing
`web/src/pages/VideoPlayer.tsx`) and the shared interaction contract the
native TV/mobile/desktop apps reuse.

## Acceptance criteria

- **AC1** The live player plays a channel's live HLS at the **live edge**
  (joined at the wall-clock offset, AC of
  [27.3](story-27-03-live-stream-engine.md)); it is a distinct mode from
  the VOD player (no scrub bar for the live timeline; a "LIVE" indicator).
- **AC2** **Channel surfing:** channel-up / channel-down (keys, on-screen
  buttons, D-pad) switch to the next/previous channel by lineup order;
  **number entry** (type `1` `2` → channel 12, with a short commit
  timeout) tunes directly. Surfing is fast — it leans on the warm-window
  instant re-tune ([27.3](story-27-03-live-stream-engine.md) AC6).
- **AC3** A **tune banner** appears on every channel change showing:
  channel number + logo + name, the **current program** (title, poster),
  **next up**, and a **progress bar** for the current program; it
  **auto-hides after ~5 s** and can be dismissed or re-summoned (`Info`/
  click).
- **AC4** A **mini-guide overlay** (press the guide button) shows a
  compact "what's on now/next across all channels" list **over** the
  still-playing current channel; selecting a row tunes to it; dismiss
  returns to full-screen playback. The current channel keeps playing
  behind it.
- **AC5** **"Watch from beginning"** on the current program starts an
  **on-demand** playback of that program's `video_id` from offset 0
  (per [27.3](story-27-03-live-stream-engine.md) EC8 — this does **not**
  perturb the shared live timeline); a "return to live" control jumps
  back to the live edge.
- **AC6** **PiP** is offered where supported (web Picture-in-Picture API,
  mobile/desktop native PiP); unsupported platforms hide the control
  rather than erroring.
- **AC7** The player surfaces stream state cleanly: a brief "tuning…"
  state on switch, a graceful "channel unavailable / no content" slate
  for degraded channels ([27.1](story-27-01-channel-definition.md) EC5),
  and automatic re-join if the stream hiccups (resume at live edge).
- **AC8** **D-pad / remote** ergonomics on TV: up/down surf channels,
  left/right summon banner / scrub the *current program* only in
  "watch from beginning" mode, `OK` toggles banner, a dedicated guide
  button opens the mini-guide / full EPG; mappings reuse the shared
  keyboard layer.
- **AC9** Channel changes update **watch state** appropriately: live
  viewing records "watched channel X" lightly; "watch from beginning"
  records normal per-video progress in `play_state`.
- **AC10** The player respects ACL: surfing only cycles channels the user
  can access; a deep link to a forbidden channel shows a not-authorised
  state.

## Test cases

- **TC1** `test_live_mode_no_vod_scrubbar` — live player shows LIVE
  indicator, no seekable live timeline; VOD player unchanged.
- **TC2** `test_channel_up_down` — channel-up/down moves through lineup
  order, wrapping at the ends.
- **TC3** `test_number_entry_tunes` — typing `1`,`2` within the timeout
  tunes channel 12; an invalid number shows a brief "no channel" toast.
- **TC4** `test_tune_banner_contents_and_autohide` — on tune, banner
  shows number/logo/name/current/next/progress; hides after ~5 s; `Info`
  re-summons it.
- **TC5** `test_mini_guide_overlays_without_stopping` — open mini-guide →
  current channel still playing behind; selecting a row tunes.
- **TC6** `test_watch_from_beginning_is_vod` — invoking it opens an
  on-demand session for the current program at offset 0; "return to live"
  re-tunes the channel.
- **TC7** `test_pip_supported_and_hidden` — PiP control present where
  supported, absent where not; never errors.
- **TC8** `test_degraded_channel_slate` — tuning an empty channel shows a
  "no content" slate, not a spinner forever.
- **TC9** `test_auto_rejoin_on_hiccup` — simulated stream stall → player
  re-joins at the live edge.
- **TC10** `test_dpad_mappings` — simulated remote: up/down surf, guide
  button opens mini-guide, OK toggles banner.
- **TC11** `test_surf_uses_warm_retune` — rapid up/down does not spawn a
  permanent encoder per channel (leans on warm window; assert via the
  streaming runtime registry in an integration test).

## Edge cases

- **EC1 Surfing past the ends.** Channel-up from the last channel wraps to
  the first (configurable); number entry to a non-existent number is
  rejected with feedback, not a crash.
- **EC2 Rapid surfing.** Debounce/commit so holding channel-up doesn't
  open N sessions; only the channel the user lands on for >X ms is
  actually tuned (the rest are guide-preview only).
- **EC3 Banner during mini-guide.** Opening the mini-guide suppresses the
  auto-banner to avoid stacked overlays.
- **EC4 "Watch from beginning" near program end.** If the current program
  is about to end, the VOD session still plays it from the start; "return
  to live" lands on whatever is now airing (which may be the next
  program).
- **EC5 PiP + channel surf.** Surfing while in PiP retunes the PiP
  stream; the banner is suppressed in PiP (no room) or shown in the main
  window if restored.
- **EC6 Deep link to a channel.** A URL like `/live/{number}` tunes
  directly; an unknown/disabled/forbidden number resolves to an
  appropriate empty/forbidden state.
- **EC7 Audio focus / background.** On mobile, backgrounding the app
  either continues audio (if PiP/background play enabled) or pauses and
  re-joins live on return — never silently keeps a transcode alive with
  no consumer (the engine's idle reaper handles the abandoned case).
