# Epic 53 research: what Maktaba has today, and how Plex and its peers do it

> Written 2026-11-03 for [the epic](README.md). §1 is the code as it is on `main` at
> `7aa1af482`. §2–§8 are outside sources, with a link after each claim. "Unverified" means no
> primary source confirmed it; forum and third-party sources are marked as such.

## 1. Maktaba today

### 1.1 How each part logs

| Part | Library and format | Where it goes | Gaps |
|---|---|---|---|
| Go API (`api/`) | `shared/log/go` (`log/slog`): JSON in prod, text in dev. Every line has `ts, level, service, version, env, msg`; `request_id, session_id, job_id, video_id, user_id` come from the context, but only on lines logged with `mlog.From(ctx)` (`shared/log/go/logger.go:150`, `ctx.go`). Redaction by key name and by value (`?token=`, `?sig=`, `?X-Plex-Token=`, since #385). Level change at runtime by SIGUSR1. | stdout, plus a 10,000-line in-memory ring (`ringbuffer.go`) served at `/logs/recent` on the admin port `:9100` | Most handler and background lines use plain `slog.*`, so they carry no request id. `POST /admin/log/level` (story 21.1) was never built. |
| Streaming (`streaming/`) | the same shared logger (`streaming/main.go:88`); every failed request logs `stream_request_failed` with chi's request id | stdout and its ring | No tracing. |
| Cloud (`cloud/`) | **not** the shared logger: plain `slog.NewJSONHandler` on stdout at Info (`cloud/cmd/maktaba-cloud/main.go:175`, `maktaba-cloudlink/main.go:53`) | stdout (container logs) | No ring, no redaction contract, no shared fields. |
| Python pipeline | `maktaba_pipeline/log` wraps structlog with the same field contract; JSON in prod (`log/__init__.py:151`). `log/hooks.py` routes stdlib logging, asyncio task errors and `sys.excepthook` in. `log/liveness.py`: `/healthz` answers 503 when the event loop is stuck. `observability.py`: `log_event` and a Prometheus text registry. | stdout, a ring at `GET /logs/recent` | It never reads the request id the API sends (`maktaba-request-id` gRPC metadata, `api/internal/grpcclients/pipeline/realclient.go:93`); `bind_request_id` has no callers. |
| Supervisor (`cmd/maktaba-server serve`) | children inherit its stderr (`supervisor/stderr_stamp.go`); its own `[serve]` lines get a timestamp. Supervisor events are JSON lines in `supervisor/eventlog.go` (rotated at 1 MB). | launchd sends the whole tree to `~/Library/Logs/Maktaba/server.log`, rotated at **50 MB with one `.1` copy** (`apps/desktop/src-tauri/src/agent.rs:88`, `cmd/maktaba-launcher/logfile.go:18`) | One file for every process; a noisy child can push the useful lines out in hours. No per-child tag beyond each line's own `service`. |
| Desktop shell (Tauri) | `src/applog.rs`: panic hook, `app.log` rotated at 1 MB; `keep_going` keeps a panicking loop from killing the tray | `app.log` next to `server.log` | Nothing leaves the Mac. |
| Web (`web/`) | `RouteErrorBoundary` per route and around the whole app (#384); `lib/errorLog.ts` writes `[uncaught]` to the console; `lib/telemetry/client.ts` sends scrubbed errors to `POST /api/telemetry/app` **only after opt-in**; player failures go to `POST /api/stream/sessions/{id}/errors` (`playback_failed`) | console, opt-in telemetry | No request id sent; no report button. |
| Apple apps | `MaktabaKit/Log.swift`: `os.Logger`, subsystem = bundle id, categories `network, session, keychain, ui, crash`, private values `.private`. `CrashReports.swift`: MetricKit `MXCrashDiagnostic`, last 5 kept under Application Support/CrashReports (#382) | unified log, local files | tvOS has no MetricKit, so the Apple TV app has no crash record. Nothing is uploaded. |
| Android phone (Player, Remote) | `core/diagnostics/Diagnostics.kt`: `AppLog` (tag `Maktaba`, kind/class/status, never the message), `CrashLog` default handler writing `files/last-crash.txt` (#380); the Player's Profile shows it | logcat, one local file | Android TV (`apps/tv/android`) does not install `CrashLog`. No ANR record. Nothing is uploaded. |
| `apps/mobile` | Capacitor around `web/dist` | as web | |

### 1.2 Correlation, tracing, metrics

- `X-Request-Id` (UUIDv7) is minted or accepted by `api/internal/middleware/requestid.go` and
  forwarded to the pipeline, which drops it. **No client sends it.** The cloud has its own
  request-id middleware.
- `shared/tracing/go` (OTLP/HTTP, W3C `traceparent`) is a no-op unless `MAKTABA_OTLP_*` is set, and
  is mounted only in the API router (`api/internal/router/router.go:131`).
- `shared/errrpt/go` gives an error a UUIDv7 `error_id`, a category and a trimmed stack, with an
  optional webhook; only the API's panic recoverer uses it.
- Prometheus `/metrics` on the API admin port (with the supervisor's update metrics); the
  pipeline has its own text exporter. Nothing scrapes or keeps history of either.
- Sentry is used nowhere.

### 1.3 Pages and endpoints that exist

| Page | Backed by |
|---|---|
| `/admin/logs` (`web/src/pages/Admin/Logs.tsx`) | `GET /api/admin/logs/stream` (the rings, polled), `/api/admin/logs/files` (server.log, app.log, launcher.log of a window, also a joined machine's over its channel), `/api/admin/logs/export` (tar.gz: `system-info.json`, three ring dumps, `error-summary.json`, `health-check.json`, `files/*.log`) |
| `/admin/health` | `/api/admin/system/status`, `/api/system/health`, `/api/system/processes` (the supervisor's `/processes`) |
| `/admin/analytics` | `/api/admin/analytics/*` (Epic 29, 44) |
| `/admin/audit` | `/api/security/audit` |
| `/queue/*` | `/api/queues/*`, `/ws/queues` (Epic 40) |
| Preferences → Account | `GET /api/diagnostics/export`: any signed-in user, scoped to their own lines |

Tables: `audit_log`, `events` (7-day WS replay), `job_events`, `health_samples`,
`dependency_health`, `telemetry_events`, `telemetry_web_vitals`, `telemetry_outbox` (home);
`telemetry_error_groups`, `telemetry_error_occurrences`, `telemetry_server_reports`,
`telemetry_app_installs`, `telemetry_app_events`, `server_health` (cloud, `00150001`). The cloud
error groups hold **anonymous** opt-in usage data (Epic 16 story 16.7): no account, server, user
or IP.

There is **no "report a problem"** anywhere, and **no home endpoint for client crash or log
uploads** (the open follow-up of `docs/audits/errors-logging-crashes.md`).

### 1.4 What happens when a part fails (the failure model)

The supervisor (`maktaba-server serve`) serves the embedded web app on the web port (8088,
fallback 8780), reverse-proxies `/api/` to the API and `/stream/` to streaming, and owns every
child. Restart policy (`cmd/maktaba-server/internal/config/supervisor.go:37`): restart always,
backoff 1 s doubling to 60 s, **5 exits in 10 min marks the role `failed`**, retried every 5 min,
backoff reset after 5 min healthy; `/healthz` probed every 10 s (3 s timeout), 3 misses = a hang,
killed. launchd restarts the supervisor itself (`KeepAlive`, `ThrottleInterval` 10 s, no
cut-off); the launcher rolls back on a boot loop.

| Fails | Web UI today | Native apps today |
|---|---|---|
| A pipeline worker | Works; System health shows it late or gone. | Work. |
| Streaming | Works; `/stream/` answers a themed 502 "Live TV is not reachable". | Browse works; playback fails. |
| **Postgres** | The page loads, but sign-in, sessions and every data call fail. **The logs, health and diagnostics pages need a signed-in admin, so they are unreachable.** No page works without the database. | Signed-in apps keep tokens (#382) but every list fails. |
| **The API** | The shell loads; `/api/` HTML gets "Maktaba is restarting" (reloads every 5 s), JSON gets a 502 with `Retry-After` (`cmd_serve.go:696`). Nothing is usable, including logs. | Every call fails. |
| **The supervisor** | Nothing answers on the web port until launchd restarts it (≥ 10 s); a panic in its own loop stops the stack. | Nothing. |

**Single points that take the whole UI down:** the supervisor process, the API (all data and
auth), and Postgres (auth). The admin's own tools for finding out *why* (logs, health) depend on
all three.

### 1.5 CI that already breaks things

The update rehearsal (`.github/workflows/update-rehearsal.yml`, `docs/update-rehearsal.md`)
updates a seeded home and breaks it on purpose (scenario 06 is `kill -9` mid-restore), measures
downtime against a budget, keeps the logs, and gates every tag. Supervisor unit tests cover a
crash, a hang, a crash loop and a backoff reset (`supervisor/run_test.go`). **Nothing kills the
API or Postgres and then checks the UI still loads.**

### 1.6 Earlier work this builds on

- Epic 21 (observability): stories 21.1–21.8. Built: the Go and Python loggers, metrics, API
  tracing, health probes, errrpt (recoverer only), the audit table. Not built: the log-level
  route, tracing in streaming and the pipeline, `x-error-id`, `shared/{log,errrpt,tracing}/py`,
  `shared/redact/list.yaml`, Sentry. Epic 53 takes over 21.1's leftovers and 21.5.
- PR #291 (`specs/ERROR_LOGGING_AUDIT.md`, `docs/troubleshooting-logs.md`): logs the reason
  every operation fails.
- PRs #378–#387 (`docs/audits/errors-logging-crashes.md`): swallowed errors, raw text on screens,
  panics, the web error boundaries, MetricKit and `CrashLog`.
- Epic 40 (queue health), Epic 41 (help), Epic 36 (Mac app), Epic 29 and 44 (analytics),
  Epic 16.7 (opt-in usage data to Maktaba Cloud).

## 2. Plex

- **Logs:** macOS `~/Library/Logs/Plex Media Server/`; Windows `%LOCALAPPDATA%\Plex Media Server\Logs`.
  https://support.plex.tv/articles/200250417-plex-media-server-log-files/
- **Rotation:** `Plex Media Server.log` plus `.1`–`.5` (forum); hidden pref `LogNumFiles`, default
  5 (forum); rolled on restart and on size. Size per file unverified.
  https://forums.plex.tv/t/pms-increasing-log-file-retention/791012
- **Debug and verbose:** debug for troubleshooting; verbose "not recommended for normal use".
  https://support.plex.tv/articles/201105343-advanced-hidden-server-settings/
- **Download Logs:** Settings > Manage > Troubleshooting gives a zip; plugin logs and crash dumps
  are not in it (crash dumps sit in a `Crash Reports` folder, forum). Exact contents not
  documented. https://support.plex.tv/articles/200250417-plex-media-server-log-files/ ,
  https://forums.plex.tv/t/pms-crashes-every-few-days/219362
- **Crash reports:** with "Send crash reports to Plex" on, the server uploads a crash on its next
  start. https://support.plex.tv/articles/200289496-general/
- **Dashboard:** Now Playing (free), Bandwidth, CPU, RAM, Top Users, Play History, Alerts (Plex
  Pass); alerts last only for the browser session; reached the mobile and TV apps in July 2026.
  https://support.plex.tv/articles/200871837-status-and-dashboard/ ,
  https://forums.plex.tv/t/server-dashboard-release/941143
- **Tautulli** fills the gaps (watch history, transcode checks, notifications on server problems):
  a gap Maktaba can close natively. https://plexguide.com/t/using-tautulli-to-monitor-and-improve-your-plex-usage/70
- Unverified: that Dashboard and Troubleshooting are owner-only; whether Plex's UI stays up with a
  corrupt database (users restore or rebuild by hand: https://forums.plex.tv/t/database-disk-image-is-malformed/873219).

## 3. Jellyfin and Emby

- **Jellyfin:** level in `logging.json` (Serilog), reloaded live; debug is very large.
  https://jellyfin.org/docs/general/administration/troubleshooting/ . `LogFileRetentionDays` and
  `ActivityLogRetentionDays` (30 by default, moderator).
  https://kotlin-sdk.jellyfin.org/dokka/jellyfin-model/org.jellyfin.sdk.model.api/-server-configuration/log-file-retention-days.html .
  Dashboard > Logs lists files to open inline; the Activity log records sign-ins, playback,
  library changes, errors (third party). https://jellywatch.app/blog/jellyfin-dashboard-complete-admin-guide-every-setting-2026 .
  No built-in alerting (Webhook plugin); no crash-reporting plugin found (unverified absence).
- **Emby:** a new log file every midnight; **downloading logs through the UI anonymises them**;
  raw files are not. https://emby.media/support/articles/Log-Files.html . Apps can send their log
  to Emby ("Send Log" on Android TV with debug on). https://emby.media/community/topic/792-how-to-report-a-problem/

## 4. Home Assistant

- **System log:** warnings and errors, a condensed view of `max_entries` (default 50) and the raw
  log; events for automations. https://www.home-assistant.io/integrations/system_log/ (the
  count and first/last occurred in the UI are not in the docs: unverified).
- **Repairs:** integrations raise issues in a registry; fixable by a flow or a link; persistent;
  can be ignored. https://developers.home-assistant.io/docs/core/platform/repairs
- **Diagnostics download:** `async_redact_data` replaces listed keys with `**REDACTED**`, nested
  too, keeps empty values visible; authors review by hand because users post them publicly.
  https://contributors.home-assistant.io/core/integrations/diagnostics/
- **Recovery mode** starts by itself when the config prevents a normal start: minimal load, but
  the **UI, logs, file editor and backup restore still work**. **Safe mode** is user-triggered,
  drops custom parts, lasts one restart. https://www.home-assistant.io/docs/troubleshooting_general/
- **Analytics** opt-in; crash reports to Sentry a separate opt-in.
  https://www.home-assistant.io/integrations/analytics/

## 5. Tools

- **Sentry self-hosted:** 4 cores, 16 GB RAM + 16 GB swap (32 GB recommended), 20 GB disk.
  https://develop.sentry.dev/self-hosted/ . Too big for a home Mac.
- **GlitchTip:** 256–512 MB, Postgres 14+, Redis optional, 90-day default retention, "any
  Sentry-compatible SDK". https://glitchtip.com/documentation/install
- **Sentry envelope:** a JSON header line, then items (header line with `type`, `length`, then
  payload), `POST /api/<project>/envelope/`, `application/x-sentry-envelope`; unknown item types
  are skipped. https://develop.sentry.dev/sdk/data-model/envelopes/
- **W3C trace context:** `version-traceid-parentid-flags`. https://www.w3.org/TR/trace-context/
- **OpenTelemetry Go logs:** `otelslog` bridge; parts of the Logs SDK still experimental, pin
  versions. https://openobserve.ai/opentelemetry/go/
- **Loki:** monolithic ~1.5 GB steady, spikes past 4 GB on bad queries (community).
  https://community.grafana.com/t/loki-monolithic-memory-consumption/112032
- **Prometheus:** ~3 kB per head series. https://www.robustperception.io/how-much-ram-does-my-prometheus-need-for-ingestion
- **Postgres as the store:** dropping a partition is "far faster" than DELETE, no VACUUM.
  https://www.postgresql.org/docs/current/ddl-partitioning.html . pg_trgm is trusted.
  https://www.postgresql.org/docs/current/pgtrgm.html
- **SQLite FTS5** (our choice, see README §6.2): full-text index in the SQLite file the
  supervisor already can open (`modernc.org/sqlite` is in `cmd/maktaba-server/go.mod`, pure Go,
  so `CGO_ENABLED=0` builds keep working). https://www.sqlite.org/fts5.html

The research agent recommended a Postgres store. The epic keeps the idea (an embedded store,
no extra stack) but puts it in a separate SQLite file; README §6.2 says why.

## 6. Mobile crash reporting

- **MetricKit** `MXCrashDiagnostic`: iOS 14+, macOS 12+, visionOS; **no tvOS**; unsymbolicated
  call stack tree. https://developer.apple.com/documentation/metrickit/mxcrashdiagnostic.md .
  Next-launch delivery on iOS 15+ is from a third-party source only (unverified), and iOS
  17.2–17.5 reportedly failed to deliver. https://developer.apple.com/forums/thread/806457
- **Xcode Organizer:** App Store users must opt in; TestFlight users share automatically; reports
  take days. https://help.apple.com/xcode/mac/current/en.lproj/deve2819c518.html
- **tvOS:** PLCrashReporter supports tvOS (since 1.4.0; 1.11.3 needs tvOS 12).
  https://github.com/microsoft/plcrashreporter/blob/master/CHANGELOG.md
- **Android vitals:** bad behaviour at 1.09 % user-perceived crash rate, 0.47 % ANR rate, 8 % per
  phone model. https://developer.android.com/vitals
- **ApplicationExitInfo** (API 30+): past exits with reason, ANR traces, native tombstones from
  API 31. https://commonsware.com/R/pages/chap-dataaccess-002
- **Crashlytics:** can start disabled; but Google dependency, data leaves the home, a store
  disclosure. https://firebase.google.com/docs/reference/swift/firebasecrashlytics/api/reference/Classes/Crashlytics
- **ACRA** posts to your own endpoint. https://github.com/ACRA/acra

## 7. Privacy

- A dynamic IP address is personal data where the operator can identify the person (CJEU
  C-582/14, *Breyer*). https://twobirds.com/en/insights/2016/global/cjeu-decision-on-dynamic-ip-addresses-touches-fundamental-dp-law-questions .
  GDPR Recital 30 names device and app identifiers. https://gdpr-info.eu/recitals/no-30/
- ePrivacy Art. 5(3) covers reading device information, not just cookies (EDPB Guidelines 2/2023).
  https://www.edpb.europa.eu/documents/530_en . Whether crash reporting is "strictly necessary"
  is unsettled: opt-in for anything leaving the user's own server is the safe course.
- Data minimisation and storage limitation, GDPR Art. 5(1)(c),(e).
- **Apple App Privacy:** disclosure of diagnostics is optional only when collection is
  user-initiated each time through a form that shows the user's name; automatic crash upload
  must be disclosed. https://developer.apple.com/app-store/app-privacy-details/
- **Google Play Data safety:** "collected" means it left the device, pseudonymous or not; declare
  crash logs and diagnostics. https://support.google.com/googleplay/android-developer/answer/10787469
- **Scrubbing:** Sentry's default key list (`password, secret, passwd, api_key, apikey, auth,
  credentials, privatekey, private_key, token, bearer`), card numbers, optional IP drop.
  https://docs.sentry.io/security-legal-pii/scrubbing/server-side-scrubbing/

## 8. Restart and crash-loop patterns

- **launchd:** at most one spawn every 10 s (`ThrottleInterval`), **no cut-off**: a crashing job
  loops forever. https://www.unix.com/man-page/osx/5/launchd.plist/
- **systemd:** `StartLimitIntervalSec`/`StartLimitBurst`; once hit, no more restarts until reset.
  https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html
- **Kubernetes:** 10 s doubling to 300 s, reset after 10 min clean.
  https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/
- Maktaba's supervisor already backs off (1 s → 60 s) and cuts off at 5 exits in 10 min; the gap
  is the supervisor itself under launchd, and the UI depending on the API and Postgres.
