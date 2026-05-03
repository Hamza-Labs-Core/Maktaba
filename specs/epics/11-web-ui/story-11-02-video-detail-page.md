# Story 11.2 — Video detail page (metadata, subtitle tracks, processing status)

A `/watch/{id}` page that shows the full video metadata, available
subtitle/audio tracks, processing job state, transcript sidebar, chapter
list, and the player itself. Renders with partial data: a video that's
`PROBED` but not yet `TRANSCRIBED` still shows player + metadata, marks the
transcript as "in progress", and live-updates as segments arrive over WS.

**Anchors:** [`architecture.md` §6.2](../../architecture.md), §3 (FSM), §7
(jobs). Depends on Epic 7 Stories 7.4 (video read), 7.5 (process control),
7.6 (transcript window), 7.7 (subtitles), 7.16 (WS fan-out).

## AC

- Header: title, poster, duration, language flags, content type, library
  name, file path (admin-only).
- Tabs: **Watch** (default), **Transcript**, **Chapters**, **Files**,
  **Processing**.
- The Processing tab shows every `processing_jobs` row for this video,
  current state, last segment offset, ETA, and per-stage controls
  (pause/resume/cancel/retry).
- Stage names match the canonical set settled in
  [REVIEW §1.3.c](../../REVIEW.md): `scan, probe, extract, transcribe,
  subtitle_gen, index, thumbnail` (note `thumbnail`, not `thumb`; and
  `subtitle_gen` is a real stage per REVIEW §1.3.b resolution).
- Subtitle track list shows source (auto / sidecar / embedded), language,
  format (`srt` / `vtt`), and "set as default" affordance. Default applies
  to all clients via `playback_state.preferred_subtitle_lang`.
- Audio track list shows codec, channels, language, and "play this track"
  affordance.
- Live updates over `/ws/jobs` and `/ws/library/{id}` re-render badges and
  progress bars without a full refetch.
- A reaped job (server-side reaper) surfaces with the human-readable reason
  from `job.reaped`.

## TC

- Open a video that's still transcribing at 23%: the transcript sidebar
  shows the segments persisted so far; new segments slide in as
  `job.progress` events arrive.
- Switch subtitle track from Arabic-auto to English-sidecar: the new VTT
  loads within 1 s; player position is preserved.
- Admin clicks "Reprocess from `transcribe`" on the Processing tab: a
  confirmation modal appears; on confirm, the segments are cleared and a
  new job is enqueued.
- Non-admin user tries to see file path: hidden behind a feature gate;
  no information leak.

## EC

- Job is in `failed` state with a long error message: the message is
  truncated with a "Show details" disclosure; copy-to-clipboard works.
- A subtitle file referenced in the DB has been deleted on disk: the row
  is greyed out with a "File missing — rescan needed" hint.
- Video state is `MISSING` or `CORRUPTED` (per
  [REVIEW §1.3.a](../../REVIEW.md)): the player surface shows
  "Cannot play — file missing or unreadable" instead of attempting a
  session, and the Processing tab surfaces the recovery path
  (rescan / restore from copy).
- Video state is `SUPERSEDED`: page redirects to the active twin video
  with a banner.
- Transcript exceeds 50,000 segments (very long lecture): virtualize the
  list (TanStack Virtual); first paint ≤ 500 ms.
