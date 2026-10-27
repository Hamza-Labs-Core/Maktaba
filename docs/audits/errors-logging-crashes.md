# Audit: errors, logging, error pages and crashes (2026-10-27)

Scope: every part of Maktaba. The services are the Go API, streaming, the
launcher/supervisor and the Python pipeline. The clients are the web app, the
Apple apps (iPhone, iPad, Mac, Apple TV), the Android apps (phone, Remote,
Android TV) and the desktop shell (Tauri). This audit builds on
[`specs/ERROR_LOGGING_AUDIT.md`](../../specs/ERROR_LOGGING_AUDIT.md) (PR #291),
and fixes most of that audit's deferred list.

What we looked for:
- errors that are swallowed;
- raw errors that reach people (JSON, Go errors, stack traces, server paths);
- missing, noisy or leaky logging;
- crashes, and whether something records them;
- screens with no loading, empty or error state.

## Fixes, by area

| Area | PR | Critical | High | Medium | Low |
|---|---|---|---|---|---|
| Go API | #385 | 1 | 10 | 9 fixed, 3 deferred | 4 deferred |
| Streaming and supervisor | #383 | 2 | 6 | 10 | 1 fixed, 5 deferred |
| Python pipeline | #379 | — | 3 | 4 | 4 deferred |
| Web (also the phone apps and the desktop window) | #384 | 1 | 6 | 8 fixed, 3 deferred | 1 deferred |
| Apple apps | #382 (on #381) | — | 3, plus 1 in #381 | 9 | 2 fixed, 5 deferred |
| Android apps | #380 | — | 6 | 8 fixed, 1 deferred | 4 deferred |
| Desktop shell | #378 | 1 | 4 | 6 fixed, 2 deferred | 3 deferred |
| CI flake found on the way | #386 | — | — | — | — |

Playback error screens in the web and native players are in #381
(`fix/app-error-screens`) and the web player branch. NAS path leaks and the
Unicode form of NAS paths are in #373.

## Findings, ranked

### Critical (all fixed)

1. **API: one panic stopped the whole API.**
   - About 65 background goroutines ran without `recover`: schedulers, reapers, the update driver, cluster links, switchover, the TV remote, the event bus and GraphQL subscriptions.
   - Fix: they now run under `internal/safego`, which logs `goroutine_panic` with the stack and keeps serving. (#385)
2. **Supervisor: a panic in its own loop killed the launcher.**
   - The children were left running with no supervisor.
   - Fix: the panic is recovered and logged at error with the stack, and the stack is then stopped cleanly. If it does not stop within 3 minutes, the launcher exits with 70. (#383)
3. **Supervisor: the bundled Postgres app-role password was in the bootstrap SQL.**
   - Postgres echoes a failed statement into its log, password included.
   - Fix: the supervisor now sends a SCRAM verifier computed in Go, and sets `log_min_error_statement = panic`. (#383)
4. **Web: a crash in the app shell or a provider blanked the whole window.**
   - This also hit the phone and desktop apps.
   - Fix: an app-wide error boundary offers Try again and Reload. (#384)
5. **Desktop: no panic hook.**
   - The Windows build has no console, so a panic left no trace at all.
   - Fix: every panic goes to `app.log`, with its thread and location. (#378)

### High (all fixed)

- **Raw errors reached people.**
  - About 170 API 5xx answers put `err.Error()` in the problem `detail`: SQL errors, upstream URLs, supervisor answers, the update binary's path. A source-scan test now blocks new cases. (#385)
  - Streaming put cache paths and database errors in `detail`. (#383)
  - Every Android TV screen showed `The home answered 404: {json}`. So did sign-in, the Player's library and the Remote. (#380)
  - The Apple apps showed raw HTTP bodies and `localizedDescription`. (#381, #382)
  - Web viewer screens and about 35 toasts showed raw problem text, in English even in the Arabic UI. So did the page-crash screen and the desktop setup page. (#384)
- **A dropped error left a home in a bad state.**
  - API:
    - 74 update-driver store writes were dropped;
    - the switchover undo steps were dropped, which could leave a machine drained;
    - ending a database copy dropped its errors, and a replication slot left behind holds the home's WAL;
    - Cloudflare cleanup in the TV remote dropped its errors. (#385)
  - Desktop: the home's update-order state was dropped, so the home waited forever. (#378)
  - Pipeline: path migration read any error as "table missing", which could leave a table out of a backup. (#379)
- **Apps signed people out when the home blinked.**
  - The Apple apps cleared their tokens on any refresh failure, a 5xx or an unreachable home included. They now sign out only when the home refuses with a 4xx.
  - At launch, an unreachable home dropped the person on the sign-in form, and the Apple TV asked for a new pairing code. (#382)
- **Hangs.**
  - Thumbnail ffmpeg could block forever on a full stderr pipe, while its heartbeat made the job look alive. It now has a 10-minute timeout. (#379)
  - Streaming stopped a transcoder (up to 15 s) while holding the session store lock, which stalled every playlist. (#383)
- **Retries.** API gRPC calls looked for "UNAVAILABLE" in the error text, which real gRPC errors never contain, so retries never ran. They now read the status code. (#385)
- **Playback.** The master playlist declared H.264 Main 4.0, but the encoders write High. It now declares High 4.2 (`avc1.64002a`), and every rung is encoded at High. (#383)
- **Logs.** The LLM status URL was served with its credentials on System health. (#383)
- **Crash survival.**
  - On the desktop, a panic while a lock was held froze the tray for good.
  - A panic in the status poll or the update check stopped that loop for good. (#378)

### Medium (fixed unless marked)

- **Logging.**
  - Redaction now also matches key suffixes (`_token`, `_secret`, `_password`) and credentials in URL query strings.
  - Every log line written with a request's context carries its request id.
  - gRPC failures name the method and address, and the circuit breaker logs when it opens. (#385)
  - The supervisor logs restarts and "gave up" at error. A child's `panic:`, `fatal error:` or `Traceback` lines now show as errors on the logs page. (#383)
  - The pipeline logs:
    - `background_task_failed` when a loop dies;
    - `job_stalled` after 30 minutes with no progress;
    - why a speech-to-text backend is not ready. (#379)
  - "Older schema" catch-alls in the pipeline now log a missing table at DEBUG, a lost connection at INFO, and anything else at WARNING with the traceback. (#379)
  - The Apple apps gained `os.Logger` categories. Problem details are `.private`, and no tokens are logged. (#382)
  - The Android apps log failures under the tag `Maktaba`, with the error kind and status, never the message. (#380)
- **Stalls.**
  - A VOD encoder that writes nothing for 2 minutes is stopped (`vod_transcode_stalled`).
  - A direct-play read that hangs for 30 seconds fails (`vod_direct_read_stalled`). (#383)
- **Crash records.**
  - Apple: MetricKit crash diagnostics are kept locally on iOS and macOS. tvOS has no MetricKit. (#382)
  - Android: `files/last-crash.txt` keeps class names and stack frames only. (#380)
  - Desktop: `app.log`. (#378)
  - None of these is uploaded, because the home has no endpoint for client logs.
- **Error states.**
  - Web:
    - one `LoadError` component, with Try again, Sign in again on a 401, and Back on a 404 or 403;
    - an offline banner;
    - a translated 404 page;
    - uncaught errors and rejections are logged to the console without the thrown value. (#384)
  - Android TV and Apple TV: Choose another home and Sign in again appear where they fit. (#380, #382)
  - A failed read no longer shows "No results", "Nothing needs attention" or "No videos". (#382, #380)
  - A failed next page can be retried. (#380, #382)
- **Deferred:**
  - The admin-only web screens and the API's admin `reason` fields still show raw supervisor and problem text, about 60 places. It helps admins diagnose, but it needs a closed "Details" disclosure.
  - The web Live TV player does not report playback failures to the server. That needs a channel variant of `POST /api/stream/sessions/{id}/errors`.
  - "Choose another home" is missing from the web bundle, which has no home picker. The desktop menu's switch-server event has no handler.
  - Four web profile tabs offer Retry but not Sign in again on a 401.
  - Desktop error dialogs show the program's own error under the translated sentence. The owner chose this in the review of #202.
  - A failed rules read in node mode is not logged.

### Low (mostly deferred)

- Internal API→streaming endpoints answer with `err.Error()`. Only the API calls them.
- Streaming's 51 background goroutines are crash-only by design: the supervisor restarts streaming and now logs it at error.
- Pipeline job failure messages can name NAS paths. They are shown only on the admin failures list.
- Hard-coded English strings remain in the Android Player and Remote and the Apple Player and Remote, outside the error paths.
- The remaining safe `!!`, `fatalError` in `init?(coder:)` and decorative `try?` were left as they are.

## Restarts and crash reporting, per part

| Part | Restarted by | Reported in |
|---|---|---|
| API, streaming, pipeline, Postgres | the supervisor, with exponential backoff and crash-loop marking | System health (state, restarts, last exit), the event log, and `server.log` at error |
| Supervisor | launchd `KeepAlive`, the Windows agent loop and Task Scheduler, systemd | `app.log` and `server.log` |
| Web, phone and desktop windows | the error boundaries | the console |
| Apple apps | the OS | MetricKit payloads and unified logging |
| Android apps | the OS | `last-crash.txt` and logcat |

## Follow-ups

- A home endpoint for client crash and log reports, so the records above can be uploaded on request.
- An admin "Details" disclosure that replaces the raw text on the admin screens.
- Live TV playback reports, and a web home picker for "Choose another home".
- Fix `errorKind.ts` so it treats only a fetch network `TypeError` as "unreachable" (the web player branch).
- Make `pipeline/tests/cluster/test_config.py::test_paths_per_os` deterministic. It went over its time limit under heavy machine load.
- The replica `sign-in on the node` 401 flake: root cause fixed in #386.

Where to look in the logs: [`docs/troubleshooting-logs.md`](../troubleshooting-logs.md).
