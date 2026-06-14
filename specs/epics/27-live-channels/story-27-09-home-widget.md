# Story 27.9 — "What's On Now" home widget

## Description

Surface live channels on the **main home screen** so they're discoverable
without opening the full guide: a horizontal **"What's On Now"** rail
showing each channel's currently-airing program with poster + progress,
the **next** program coming up, and a **Tune In** action that jumps
straight to the live player ([27.7](story-27-07-live-channel-player.md)).
It is driven entirely by the cheap `GET /api/channels/now` read path
([27.4](story-27-04-epg-generation.md)) — no transcode is started just to
render the home screen.

This story spans the API (the `now` payload shape, owned with
[27.4](story-27-04-epg-generation.md)) and the client rail on **all
platforms** (web + native TV/mobile/desktop), reusing the existing
home-screen rail pattern (Epic 14 discovery rails).

## Acceptance criteria

- **AC1** A **"What's On Now"** rail appears on the home screen (above or
  among the existing discovery rails) showing one card per enabled,
  accessible channel: channel number + logo + name, current program
  poster + title, a **progress bar** for the current program, and a
  compact **"Next: …"** line.
- **AC2** Each card has a **Tune In** action that navigates to the live
  player for that channel and joins live; the whole card is the
  primary tap/click/`OK` target.
- **AC3** The rail is powered by `GET /api/channels/now` (current + next
  per channel, with progress), which is a **read path over the schedule**
  — rendering the home screen starts **no** transcode.
- **AC4** The rail **updates live**: progress advances and a program
  rolling over swaps the card content within a few seconds (poll/SSE),
  without a full home reload.
- **AC5** The rail is **hidden entirely** when the user has no accessible
  channels (no empty rail clutter); for editors, an optional "Create a
  channel" entry may appear instead (configurable).
- **AC6** Available on **all client platforms** using the shared rail
  contract: web, tvOS, Android TV, mobile, desktop. On TV it is D-pad
  focusable; on mobile it scrolls horizontally.
- **AC7** Ordering follows the lineup (`sort_order`/`number`); a
  configurable cap limits how many channels show in the rail with a
  "See all / Open guide" affordance linking to the EPG
  ([27.6](story-27-06-epg-grid-ui.md)).
- **AC8** **i18n + RTL** correct; progress and "Next" localise.

## Test cases

- **TC1** `test_rail_renders_current_and_next` — rail shows each
  channel's current program (poster, title, progress) + a "Next" line.
- **TC2** `test_tune_in_navigates_live` — activating a card opens the
  live player for that channel.
- **TC3** `test_no_transcode_on_render` — rendering the rail issues only
  the `/now` read; no `OpenChannel`/FFmpeg is triggered (integration
  assertion against the streaming runtime registry).
- **TC4** `test_live_update_rollover` — advance the clock past a boundary
  → card swaps to the new current program within a few seconds.
- **TC5** `test_progress_advances` — progress bar reflects elapsed
  fraction of the current program.
- **TC6** `test_hidden_when_no_channels` — no accessible channels → rail
  absent (or editor CTA per config).
- **TC7** `test_cap_and_see_all` — more channels than the cap → capped
  rail + "Open guide" link.
- **TC8** `test_acl_scopes_rail` — rail shows only channels the user can
  access.
- **TC9** `test_rtl_rail` — Arabic locale → rail and progress mirror
  correctly.

## Edge cases

- **EC1 Degraded/empty channel in the rail.** Shows a "No content right
  now" card state rather than a broken poster; Tune In still works
  (lands on the slate).
- **EC2 Stale `now` between rollovers.** The card uses server time from
  the payload to compute progress so a wrong client clock doesn't show a
  >100 % or negative progress bar.
- **EC3 Many channels, slow device.** The rail virtualises / lazy-loads
  cards on lower-end TV/mobile so the home screen stays responsive.
- **EC4 Channel disabled while shown.** A disabled channel drops out of
  the rail on the next refresh; an in-progress Tune In to it resolves to
  a "channel unavailable" state.
- **EC5 Home screen with no live feature enabled.** If channels are not
  in use at all, the rail is simply absent — it never shows a placeholder
  for a feature the operator hasn't set up.
