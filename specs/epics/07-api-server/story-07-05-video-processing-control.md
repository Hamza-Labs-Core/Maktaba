# Story 7.5 — Video processing control

Endpoints from §9.2: `POST /api/videos/{id}/process`,
`POST /api/videos/{id}/reprocess`.

**AC-1 — Process now (priority bump).**
- **Given** a video in any state,
- **When** `POST /api/videos/{id}/process` is sent with `{stage:
  "transcribe"?, priority: 50?}`,
- **Then** an existing pending/paused job for that `(video, stage)` has
  its priority lowered to 50 (user-initiated), or — if no job exists — a
  fresh job is enqueued at the requested stage with priority 50, and the
  response is `200 OK` with the resulting job id and current state.

**AC-2 — Reprocess from stage.**
- **Given** a video already past `from_stage`,
- **When** `POST /api/videos/{id}/reprocess` is sent with `{from_stage:
  "transcribe"}`,
- **Then** the video's `state` is rolled back to the predecessor of
  `from_stage`, the existing transcript artifacts are marked `superseded`
  (state transitions to `SUPERSEDED` per the FSM in arch §3 — see also
  Epic 9 Story 9.6 rehash mode), and a fresh chain of jobs from
  `from_stage` onward is enqueued at priority 200 (re-process default).
  A Postgres NOTIFY fires on `videos.state_changed`.

**Test cases:**
- Unit: `from_stage = "transcribe"` resolves the predecessor as
  `audio_extracted` against the FSM in §3.
- Integration: process-now on a `failed` job moves it back to `pending`
  and resets `attempts = 0`.
- Integration: reprocess fires Postgres NOTIFY `videos.state_changed`.
- Integration: reprocess preserves the old `transcripts` row but marks
  `superseded_at = now()`; a UI search still returns the old segments
  until the new ones are ready.
- Integration: two concurrent `/process` calls on the same video collapse
  to one job (idempotency).

**Edge cases:**
- Reprocess `from_stage = "scan"` is rejected (scan is whole-library, not
  per-video). Returns 400 `type: stage-not-per-video`.
- Reprocess on a video whose source file is missing — the job will fail
  at `probe`; the API returns 200 anyway (the worker is the source of
  truth for execution outcomes).
- Process-now bumps a job currently `running` — priority does not
  preempt; the API responds 200 with `note: "job already running"`. Test
  case: start a job, hit /process, observe no state change.
- Reprocess on a video with an in-flight transcribe job — the in-flight
  job is left to complete, the new job is enqueued *after* it, and the
  in-flight one is marked `superseded` on completion (its outputs are
  still useful as fallback if the new one fails).
