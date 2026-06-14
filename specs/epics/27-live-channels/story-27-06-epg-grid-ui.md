# Story 27.6 — EPG grid UI

## Description

The **Electronic Program Guide** as a UI: the classic cable-TV
**channel × time grid** in the Maktaba web app (and the shared component
contract the native apps reuse), driven by the guide API
([27.4](story-27-04-epg-generation.md)). Rows are channels, columns are
time, cells are programs sized to their duration. A **now-line** marks the
current moment; clicking a cell **tunes** to that channel's live stream
([27.3](story-27-03-live-stream-engine.md) / the player in
[27.7](story-27-07-live-channel-player.md)). The guide is **responsive**
(horizontal grid on desktop/TV, vertical "what's on now" list on mobile)
and **D-pad navigable** for the TV apps, reusing `web/src/lib/keyboard`.

This story owns the web UI (React, `web/`) and defines the shared client
contract; the native renderers are tracked under Epic 18's app surface
but consume the same guide payload and interaction model.

## Acceptance criteria

- **AC1** A **time-grid** renders channels as rows and a scrollable time
  axis as columns; each program is a cell whose **width is proportional
  to its duration**; the grid loads a window (e.g. now − 30 min …
  now + 3 h) and lazy-loads more as the user scrolls in time.
- **AC2** A **now-line** (vertical indicator at the current wall-clock
  time) is always visible and advances; the program currently airing on
  each channel is visually marked (and shows a progress fill).
- **AC3** **Click/select a program cell** for the *currently airing* block
  tunes that channel (navigates to the live player); selecting a
  *future* block opens its **details** (poster, description, duration,
  start time, series/episode) with a "set reminder"/"tune at start"
  affordance where applicable, not an immediate tune.
- **AC4** **Hover (desktop) / focus (TV) / long-press (mobile)** on a
  cell shows a details popover without leaving the grid.
- **AC5** A **category filter** narrows the visible channels
  (movies/kids/…); a **"What's On Now"** compact view lists each
  channel's current program as cards (this is the default on mobile).
- **AC6** **Responsive:** desktop/TV get the 2-D scrolling grid; mobile
  gets a vertical, channel-ordered "now & next" list with horizontal
  per-channel scroll for upcoming programs.
- **AC7** **D-pad navigation** (TV): arrow keys move focus cell-to-cell
  (left/right in time, up/down across channels), `OK` tunes/opens
  details, `Back` exits — implemented via the shared keyboard/focus
  layer; focus is always visible and never trapped.
- **AC8** The grid reflects **live truth**: the now-line, "is airing,"
  and progress are computed against the same `channel_programs` the
  stream uses; the view refreshes (poll/SSE) so a program rolling over is
  reflected within a few seconds without a full reload.
- **AC9** Channels/programs the user cannot access are simply absent
  (ACL enforced server-side); the grid handles an empty lineup with an
  empty state + "create a channel" CTA (linking to
  [27.8](story-27-08-channel-admin-ui.md) for editors).
- **AC10** **Performance:** the grid virtualises rows and time columns so
  a 50-channel × 24-h guide stays smooth; only on-screen cells render.
- **AC11** **i18n + RTL:** the guide renders correctly in Arabic (RTL
  time axis mirrored appropriately), reusing the existing i18n layer.

## Test cases

- **TC1** `test_grid_renders_proportional_cells` — a 30-min and a
  120-min program render at a 1:4 width ratio.
- **TC2** `test_now_line_position` — with a fixed clock, the now-line sits
  at the correct x for the current time; the airing cell is marked.
- **TC3** `test_click_airing_tunes` — clicking the airing cell navigates
  to the live player for that channel.
- **TC4** `test_click_future_opens_details` — clicking a future cell
  opens details, does **not** tune.
- **TC5** `test_category_filter` — selecting "kids" shows only kids
  channels.
- **TC6** `test_whats_on_now_view` — the compact view lists current
  programs as cards; default on a mobile viewport.
- **TC7** `test_dpad_navigation` — simulated arrow keys move focus across
  cells and channels; `OK` tunes; focus stays visible.
- **TC8** `test_live_refresh` — advance the clock past a boundary →
  now-line and airing cell update without a full reload.
- **TC9** `test_empty_lineup_state` — no channels → empty state + CTA;
  editor sees "create channel," viewer sees a neutral message.
- **TC10** `test_virtualised_large_guide` — 50×24 h guide renders only
  visible cells (assert off-screen cells absent from the DOM).
- **TC11** `test_rtl_layout` — Arabic locale → time axis and grid mirror
  correctly.

## Edge cases

- **EC1 Guide horizon end.** Scrolling past `horizon_until` shows a "guide
  ends here / generating more" boundary, not blank cells or an error.
- **EC2 Very short programs (filler).** Filler/bumper blocks are
  collapsed/merged visually (per [27.4](story-27-04-epg-generation.md)
  AC10) so the grid isn't shredded into slivers; an "Up Next" chip may
  represent them.
- **EC3 Degraded/empty channel.** Shows a single "No content" cell
  spanning the row rather than gaps.
- **EC4 Clock drift on the client.** The now-line uses server time
  (returned with the guide payload) reconciled to the client clock, so a
  wrong local clock doesn't misplace the now-line.
- **EC5 Mid-scroll program rollover.** A program ending while the user
  scrolls updates in place without jumping the scroll position.
- **EC6 Tune from a future cell.** A future cell's "tune now" still tunes
  the channel **live** (you can't watch the future); "watch from start"
  is only offered for the currently-airing program
  ([27.7](story-27-07-live-channel-player.md)).
- **EC7 Slow network.** Skeleton cells while loading; cached last guide
  shown stale-marked rather than a blank grid.
