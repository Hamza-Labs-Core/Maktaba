# Story 11.6 — Settings page (STT engine config, library paths, user preferences)

A `/settings` route with sections: Libraries, STT Backends, Search,
Playback, Account, Appearance, About. Backend-driven (`GET /api/settings`,
`PATCH /api/settings`, `GET /api/settings/stt-backends`).

**Anchors:** [`architecture.md` §6.2](../../architecture.md), §11. Depends
on Epic 7 Stories 7.3 (libraries), 7.15 (settings); Epic 9 (library
config); Epic 10 Stories 10.5, 10.11; Stories 11.13 (PAT API), 11.14
(sessions API).

## AC

- Libraries section: add / edit / delete (with `purge=true|false`
  confirmation). Per [REVIEW §5.6](../../REVIEW.md), `purge=true` requires
  the user to type the library name into a confirmation field that maps
  to a `?confirm={name}` query parameter on `DELETE /api/libraries/{id}`.
- STT Backends section: list available (`whisper-mlx`, `whisper-cpu`,
  `whisper-cuda`, `openai-api`); show health (`OK` / `unavailable`);
  per-backend config (model size, monthly cap for paid backends).
- "Test" button on each backend runs `POST /api/settings/stt-test` and
  shows latency / sample output.
- Search section: hybrid weights (0.0–1.0 sliders for FTS / semantic),
  default mode, segment grouping, default top-K.
- Playback section: default subtitle language, default audio language,
  default playback rate, default quality cap (`Auto`, `1080p`, `720p`,
  `480p`), data-saver toggle (mobile only).
- Account section: change password; **list active sessions with revoke**
  via [Story 11.14](story-11-14-active-sessions-api.md); **PAT (Personal
  Access Token) management** via [Story 11.13](story-11-13-pat-management-api.md).
- Appearance section: theme (Light / Dark / System), UI language (Arabic
  / English), density (Comfortable / Compact).
- About section: server version, build, uptime, license, link to
  changelog.
- Admin "Unlock user" affordance maps to the
  `POST /api/users/{id}/unlock` endpoint (owner: Epic 10 Story 10.11; the
  endpoint was orphaned per [REVIEW §3.2](../../REVIEW.md) and should be
  added there).

## TC

- Admin adds a new library at `/mnt/films`: a `POST /api/libraries`
  succeeds; "Scan now" enqueues a scan job; UI updates within 2 s.
- Switch STT backend on `Films` library from `whisper-mlx` to
  `openai-api`: a confirmation modal warns about cost; on confirm, future
  scans use the new backend, in-flight transcriptions continue on the old.
- Set monthly cap to $10, then issue a job that would exceed it: backend
  refuses claim; UI surfaces "Budget exceeded — bump cap or wait until
  next cycle".
- Change UI language to Arabic: layout flips to RTL within one route
  transition; previously visited views re-render correctly.
- Issue a PAT, copy it once, then list and revoke it via the Settings UI.

## EC

- A path that doesn't exist on the server: server returns 422 with a
  problem+json `path-not-found`; UI surfaces inline next to the field.
- Two admins editing the same library concurrently: optimistic lock via
  `If-Match` on `updated_at`; second write returns 409, UI offers
  "Reload and merge".
- Removing the only admin's password without setting a new one: backend
  refuses; UI shows the rule in advance.
- Test STT backend with no audio sample available on the server: the
  endpoint returns "no test fixture installed" — show a "Run smoke
  transcribe on any 30-second video" affordance instead.
- Library `purge=true` confirmation: typing the wrong name disables the
  Delete button.
