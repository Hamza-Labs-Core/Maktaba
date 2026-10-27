# Error-logging audit (2026-10-25)

**Why.** A 1080p h264/aac video failed to play in the web player, and
server.log held only three session opens within 3 s and their cleanup: no
reason. Earlier the same week, manifest 404s, stale service-worker pages and
NAS reads stalling under macOS privacy denials each took long to debug,
because nothing was logged.

**What was checked.** For every operation in the API, the streaming service,
the supervisor (`cmd/maktaba-server`), the Python pipeline, and the web, TV and
phone players:

- Are failures logged with enough context to diagnose them (ids, paths, HTTP
  status, the underlying error, the stage)?
- Are they logged at the right level?
- Are any secrets leaked (DSNs, tokens, signed URLs)?
- Are any errors swallowed (`_ =`, a bare `except`, `.catch(() => {})`,
  `return` without a log in a goroutine)?

Where to look when something fails: [docs/troubleshooting-logs.md](../docs/troubleshooting-logs.md).

## Findings and fixes

### Playback (the known gap)

| Gap | Fix |
|---|---|
| Failures after a session opened (decode errors, playlist/segment 404/410, direct-file stalls) showed only in the browser console | The player reports them: `POST /api/stream/sessions/{id}/errors`, logged by the API as **`playback_failed`** (video_id, session_id, mode, engine, outcome, code, error, http_status, url without query, position_sec, user_agent). `attachVod` gained an `onError` callback that describes every failure ending in a reopen or `onFatal`. The player also reports direct-file media errors, the reopen limit and a 15 s stall. Rate-limited to one line per session and outcome per second, and 30 per user per minute. |
| The streaming service logged no failed `/stream/*` request | **`stream_request_failed`** for every response ≥ 400 (path without query, status, problem name, bytes, duration). It is Warn, Error from 500, and Info for a playlist or segment a cold transcode has not written yet. Wired on the public server and the TV slot server. |
| ffmpeg's exit code and stderr were never logged; a dead transcode just made the playlist answer 410 | **`vod_transcode_exited`** (outcome finished/stopped/failed, exit_code, err, the last 15 stderr lines). **`vod_transcode_fallback`**: a hardware run that failed early, running again in software. |
| Session open, reuse, close and reap were invisible in streaming | `vod_session_opened` (mode, verdict, source codecs, height, pid), `vod_session_reused`, `vod_session_closed` (reason api / transcode-failed + stderr), `vod_session_reaped` (idle_ms). Refusals: `vod_open_not_probed`, `vod_open_probe_failed`, `vod_slots_exhausted`, `vod_transcode_start_failed` (with the spawn error; the player still gets the bare `failed-precondition`, since the error names host paths). |
| The API logged no session open or failure | `stream_session_opened`, `stream_open_failed`, `stream_close_failed`. |
| Direct play: open errors (EPERM, unmounted share) became a silent 404; mid-copy NAS read errors were swallowed as "client disconnect" | `vod_direct_open_failed` (ERROR, or WARN for a missing file), `vod_direct_read_failed` (offset, written, err). A client leaving is still not logged. |
| Playlist/segment reads failing for reasons other than "not written yet" were silent 404s | `vod_file_read_failed`. |

The likely trace for the failure that started this, per the streaming
audit: ffmpeg exited early → the rendition index answered 404, then 410
`transcode-stopped` → the player reopened twice → the dead sessions were
closed as `transcode-failed`. Every step of that now has its own line.

### API

| Gap | Fix |
|---|---|
| About 260 handlers returned a 5xx through `httperror` and dropped the underlying error; the 5xx was logged only as `http_request` at INFO | `httperror.Write` logs every 5xx as **`http_error`** (ERROR: method, path, status, type, detail, request_id) with its **cause**. `(*Error).Wrap(err)` attaches the cause, which is never sent to the client. 227 call sites were wrapped (each where `if err != nil` directly precedes it). |
| Request log at INFO for every status, and no line at all for a panicked request | `http_request` is WARN for a 5xx; a panic still gets its line (status 500). |
| Panic line had no request id, method, path or event | `http_panic` with all of them; the 500 it writes carries the panic as its cause. |
| `adduser`, `migrate` and `plexlink` used `$DATABASE_URL` as a flag default, which `--help` and flag errors print (password included) | The env var is read after parsing. |

### Streaming (other)

- **Watchdog:** a failing probe was never logged before the supervisor killed
  the process. Now `watchdog_probe_failed` / `watchdog_probe_recovered` are
  logged on each transition.
- **Live TV:** the errors after a TS stream's 200 were dropped. Now
  `hdhr_stream_failed`, `channel_ts_ended` (channel handler and front door)
  and `hdhr_tuners_busy`.
- **Reaper:** sweep, stop and close errors are logged (`vod_reap_failed`,
  `vod_reap_stop_failed`, `vod_session_close_failed`).

### Supervisor (`cmd/maktaba-server`)

- **Levels:** `[tag]` lines had none, so failures dropped out of an
  errors-only view. Failure lines now carry `level=error` / `level=warn`
  after the tag, and the logs page reads it.
- **Updater:**
  - Revert errors after a failed switch were dropped: `undoing the switch after …`.
  - Journal write failures were silent: logged once per distinct error.
  - Rollback verdicts said "answers 0": they now name the transport error.
  - A missing superuser URL silently skipped the rehearsal and dump: now a WARN.
- **Local token:** a failed write made the logs page and update endpoints
  answer 403 silently. It is logged now.
- **Restarts:** the restart line carries `restart #n, k/max exits in <window>`,
  and a child killed after ignoring its stop is logged.
- **Proxies:** the stream, home-stream and API proxy errors are logged.
  Client cancellations are skipped, and lines are rate-limited to one per
  30 s per error.
- **Stream gate:** an unreachable `/api/auth/me` answers 502 and is logged
  (it was a silent 401).
- **TV slots:** a failed slot-exit report to the front door is logged.
- **LLM URL:** the `[llm]` URL line loses its userinfo and query.
- **Fatal error:** the error that ends `serve` is logged with its stamp.

### Pipeline

- **Stage failures:** `StageFailure` and `GraphViolation` wrote only DB rows.
  Now `stage_failed` is logged, then `stage_failure_outcome`
  (retry/parked/terminal) or `stage_failure_noop`. Also
  `dispatch_unknown_stage`.
- **Log context:** job lines carry `stage` and `video_id`, not only `job_id`.
- **gRPC:** `grpc_call_failed` / `grpc_call_raised` (in-band errors were silent).
- **Shares:**
  - `share_listing_failed`: `os.listdir` errors became an empty listing;
    rate-limited, and ENOENT is not logged.
  - `share_root_unreachable` / `share_root_reachable` on each transition.
- **Scanner:** non-ENOENT stat errors are WARN with `errno` (they were DEBUG).
  Tracebacks were added to `scanner.save_failed`, `watcher.dispatch_failed`
  and the debouncer callback.
- **Silent failures:**
  - `storage_canonicalize_release_failed` (it was a silent `except`).
  - `nvml_init_failed` (it was DEBUG).
- **Root logging:** stdlib logging (grpc, asyncio, httpx…) is forwarded into
  structlog at WARNING and up. `sys.excepthook`, `threading.excepthook` and
  the asyncio loop handler log `unhandled_exception`.
- **Redaction:**
  - Keys that contain token/secret/password/dsn/database_url/api_key are masked.
  - Values (messages and tracebacks included) are scrubbed of `…token=`,
    `sig=`, `password=` and URL passwords.

## Deferred (follow-ups)

- **TV apps (Android TV, tvOS)** have no player error handling at all
  (Android `onPlayerError`, tvOS `AVPlayerItem` failure observers). They also
  open no streaming session, so they have no session id to report. Their VOD
  URL `/api/stream/{videoId}/index.m3u8` matches no route we could find, so
  TV VOD playback may be broken today. This needs a decision: open sessions
  like the web does, or report without one.
- **Live TV web player** (`web/src/pages/LiveTV`): errors are not posted.
  `reportPlaybackFailure` could be reused with a channel variant.
- **VOD stall watchdogs:** no stall watchdog on VOD ffmpeg (a hung NAS read
  produces no segments until the idle reap), and no read deadline on
  direct-play copies.
- **Session close under the store mutex:** `session.Close` calls `Stop`
  while holding the store mutex.
- **Supervisor secrets:**
  - `dbsafe` passes the superuser DSN on `maktaba-api migrate`'s command line,
    where `ps` can see it.
  - The pgbundle bootstrap SQL has the role password inline, and Postgres
    echoes the statement on error.
  - The LLM `Status.URL` is served unredacted.
- **API (medium):**
  - gRPC client errors lack method and target, and the circuit breaker opens
    silently.
  - Update-driver `_ = d.Store.*` writes, the drain/undrain error paths and
    `Store.Block` failures are dropped.
  - Switchover `undo` and `copyroles.endCopy` errors are dropped (a leftover
    replication slot holds WAL).
  - Swallowed Cloudflare cleanup errors in tvremote.
  - Debug-level failures in homelink and sharepath.
- **Pipeline:** done in `fix/errors-pipeline` (`job_stalled`; the
  "older schema" handlers go through `db/schema_compat.fallback`).
- **Log context:** request ids are missing on most handler and background
  failure lines, which use `slog.*` rather than `mlog.From(ctx)`.
- **Redaction (Go):** still by exact key only. `secret.RedactURL` exists but
  is unused.
- **Streaming bug to check:** `ffmpeg/transcode.go` hard-codes
  `CODECS="avc1.4d4028,…"` (Main profile, level 4.0) in the master playlist,
  while libx264 and VideoToolbox usually emit High profile. A strict MSE
  player may refuse the stream. Check it against the next
  `vod_transcode_exited` / `playback_failed` lines for the failing video.
