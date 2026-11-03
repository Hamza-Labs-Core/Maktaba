# Story 53.05 — Safe mode and crash-loop cut-offs

> Epic 53 · Phase 1 · Depends on: 53.03 · Needed by: 53.07

## Why

The supervisor already backs off a crashing child (1 s doubling to 60 s) and marks it `failed`
after 5 exits in 10 minutes, then retries every 5 minutes. Two gaps remain:

- **The supervisor itself** runs under launchd, which restarts it at most every 10 s **forever**
  with no cut-off and no reason shown. The launcher rolls back a boot loop of a *new* version,
  but a supervisor that crashes on the current version just loops.
- **There is no way to start with less.** When a worker, an extension, the LLM or TV staging
  takes the home down at start, the owner can only wait or use a terminal.

## Description

### 1. Safe mode

A one-start minimal mode, like Home Assistant's:

- **Turned on by:** the recovery console (53.04), the Mac app's tray ("Start in safe mode"), the
  "can't start" page (§3), or a file `<data>/state/safe-mode` (for support).
- **Runs:** Postgres, the API, the web port, the `logs` role. **Doesn't run:** pipeline workers,
  streaming and TV slots, the LLM and speech servers, scheduled jobs (updates check, backups,
  library scans), extensions, the cloud link. The queue is not paused in the database (machines
  joined to the home keep working); only this machine's workers stay off.
- **Lasts one start:** the flag is removed as soon as the supervisor reads it; the next restart
  is normal unless asked again. The console and the tray offer "Stay in safe mode" (writes the
  flag again before a restart).
- `/status.json` says `"mode": "safe"`; every app shows the safe-mode strip (53.06).

### 2. Cut-offs for children (existing, completed)

| Rule | Today | After |
|---|---|---|
| Backoff | 1 s → 60 s | same |
| Crash loop | 5 exits in 10 min → `failed`, retried every 5 min | same, and: after **3** slow retries fail (15 min), the role stays `down` until a person acts or the machine restarts; a **repair item** is raised |
| Repair item | — | A notification (existing `notifications` table, admins) and a row on System health: "{component} keeps stopping", the last error line, buttons Restart / Logs / Safe mode. In the console when the API is down. |
| A role that must run for the UI (Postgres, API) | crash loop like any other | same backoff, never left `down` silently: the restarting page and 53.03 say so, the console offers safe mode and rollback |

### 3. Cut-off for the supervisor

The launcher (`cmd/maktaba-launcher`, the process launchd starts) already watches its child
`serve`. It adds:

- **3 starts of `serve` within 2 minutes that exit before reaching "ready"** → stop starting it,
  write `<data>/state/start-failed.json` (`{at, exits:[{code, at}], last_lines:[…]}` from the
  supervisor's segment, scrubbed), and serve a **minimal "can't start" page** itself on the web
  port: the reason, the last 50 lines, and buttons **Try again**, **Start in safe mode**, **Go
  back to version X** (its existing rollback), **Download diagnostics**. The launcher is small
  and has no database, so this page can't fail the same way.
- **On the Mac,** the desktop app reads `start-failed.json` (the tray already reads `server.log`'s
  tail for "why it stopped", `agent.rs:270`) and shows its **own bundled page** with the same
  buttons, so the owner sees it even if the launcher can't bind the port.
- A boot loop of a **new** version keeps its existing automatic rollback (`run.go:175`); this
  cut-off is for the current version.

### 4. Strings

| Key | English | Arabic |
|---|---|---|
| `safe.strip` | Maktaba is in safe mode. Playback, processing and Live TV are off until it restarts. | مكتبة في الوضع الآمن. التشغيل والمعالجة والبث المباشر متوقفة حتى إعادة التشغيل. |
| `safe.stay` | Stay in safe mode | البقاء في الوضع الآمن |
| `repair.keepsStopping` | {component} keeps stopping | {component} يتوقف باستمرار |
| `cantStart.title` | Maktaba can't start | تعذّر تشغيل مكتبة |
| `cantStart.body` | It stopped {count} times in a row. Here is what it said last. | توقف {count} مرات متتالية. هذا آخر ما سجّله. |
| `cantStart.retry` | Try again | حاول مجددًا |

Component names in `{component}` use the user-level names of 53.03, translated
(`component.streaming` = Playback / التشغيل, `component.processing` = Processing / المعالجة,
`component.database` = Database / قاعدة البيانات, `component.api` = Maktaba service / خدمة مكتبة,
`component.search` = Search / البحث, `component.logs` = Log search / البحث في السجلات).

## Acceptance criteria

- **Given** a pipeline worker that crashes at start,
  **when** it has failed 5 times in 10 minutes and 3 slow retries,
  **then** it stays `down`, the other roles keep running, and admins get "Processing keeps stopping" with the last error line.
- **Given** the admin clicks "Start in safe mode",
  **when** the home restarts,
  **then** only Postgres, the API, the web port and `logs` run; `/status.json` says `safe`; every app shows the safe-mode strip.
- **Given** the home is in safe mode,
  **when** it restarts again without "Stay in safe mode",
  **then** it starts normally.
- **Given** `serve` exits 3 times in 2 minutes before ready on the current version,
  **then** the launcher stops starting it and serves the "can't start" page, and the Mac app shows its own page with the same buttons.
- **Given** that page,
  **when** the owner clicks "Start in safe mode",
  **then** `serve` starts with the safe-mode flag.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | supervisor policy, fake clock | 5 exits + 3 retries | `down`, repair item |
| T02 | unit | safe-mode flag | start | allowed roles only; flag removed |
| T03 | unit | launcher with a stand-in `serve` that exits 1 | 3 starts | stops, writes `start-failed.json`, serves the page |
| T04 | unit | the page | each button | the right order to the launcher |
| T05 | unit (Rust) | `start-failed.json` fixture | desktop app | its own page shows, buttons wired |
| T06 | unit | strings | en/ar parity | all present |
| T07 | **CI only** (53.07) | scenarios `supervisor-crash-loop`, `safe-mode` | | as the acceptance criteria |

## Edge cases

- **Safe mode while an update is pending:** updates don't run in safe mode; the Updates page says
  why.
- **Postgres itself is what crashes:** safe mode doesn't help; the console offers restore and
  rollback.
- **A machine joined to the home in safe mode:** it keeps working on the queue; the home's own
  workers are what stops.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/supervisor/policy.go`, `run.go` | Slow-retry cut-off; repair item event. |
| `cmd/maktaba-server/internal/supervisor/safemode.go` (new) | Flag read, allowed roles. |
| `cmd/maktaba-server/internal/config/supervisor.go` | `failed_retries` (default 3). |
| `cmd/maktaba-launcher/run.go`, `cantstart.go` (new) | §3. |
| `api/internal/notify/` (where notifications are written) | Repair item notification from the supervisor event. |
| `web/src/pages/Admin/SystemHealth.tsx` | Repair rows. |
| `apps/desktop/src-tauri/src/agent.rs`, `tray.rs`, `src/cantstart.html` (new, bundled) | §3 Mac page; "Start in safe mode". |
| `web/src/i18n/en.json`, `ar.json`; `apps/desktop/src-tauri/src/lang.rs` | Strings. |

### Tests

T01–T06 in unit jobs. **CI only:** T07 in the fault rehearsal.

### Rollout

Supervisor and launcher change: `update-rehearsal` on the exact commit, and its scenarios 06 and
09 must still pass (they exercise the launcher's rollback).

### Rollback

Revert; the old endless retry comes back.

### Dependencies

53.03. Needed by 53.07.
