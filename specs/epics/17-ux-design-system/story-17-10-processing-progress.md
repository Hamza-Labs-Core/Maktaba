# Story 17.10 — Processing progress visualization

The Queue dashboard's per-job visualization is consistent across web,
desktop, mobile, and TV: a horizontal bar with audio-time annotation,
ETA, state, and stage.

**Anchors:** [`architecture.md` §7](../../architecture.md). Implements
the visual language consumed by
[Story 11.5](../11-web-ui/story-11-05-processing-queue-dashboard.md).

## AC

- Bar segments: `done` (filled) | `current` (animated stripe) |
  `pending` (empty); color-coded by state.
- Annotation: `01:23:17 / 04:12:04 (33%)` — audio-time, not
  wall-clock.
- ETA next to the bar; updated only after 3 segments have committed
  ([Story 11.5 EC](../11-web-ui/story-11-05-processing-queue-dashboard.md)).
- Stage indicator: small icon strip showing `scan → probe → extract →
  transcribe → subtitle_gen → index → thumbnail` with the current stage
  highlighted. Stage names match the canonical set settled in
  [REVIEW §1.3.b/c](../../REVIEW.md).
- Pause / resume / cancel inline buttons; Force-Pause appears after
  `pause_grace_sec`.
- Tooltip on hover: backend, model, attempts, last heartbeat (every
  5 s per [REVIEW §1.4.c](../../REVIEW.md)).

## TC

- A running job updates the bar 1 Hz; reduced motion clamps to 0.5 Hz.
- A paused job's bar shows the resume offset as a vertical line.
- A failed job's bar shows the failure point and an error icon.

## EC

- A job with `total_duration_seconds = NULL` (probe pending): bar shows
  indeterminate stripes.
- A job that resumes from a different model: stage strip shows a
  "model upgraded" hint.
- A job whose duration metadata changed (file replaced) mid-flight:
  surface a "duration changed during processing" warning; bar uses the
  new duration as the denominator for progress that's after the swap.
