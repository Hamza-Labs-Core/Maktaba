# Story 53.05 — Safe mode and crash-loop cut-offs

> Epic 53 · Phase 1 · Depends on: 53.03, Epic 51 (51.04) · Needed by: 53.07

## Why

The supervisor already backs off a crashing child (1 s doubling to 60 s) and marks it `failed`
after 5 exits in 10 minutes, then retries every 5 minutes. Two gaps remain:

- **The supervisor itself** runs under launchd, which restarts it at most every 10 s **forever**
  with no cut-off and no reason shown. The launcher rolls back a boot loop of a *new* version,
  but a supervisor that crashes on the current version just loops.
- **There is no way to start with less.** When a worker, an extension, the LLM or TV staging
  takes the home down at start, the admin can only wait or use a terminal. Epic 51 can turn
  each of them off, but only through the API and Postgres, which may be what is failing.

## Description

### 1. Safe mode

A one-start minimal mode, like Home Assistant's:

- **Turned on by:** the recovery console (53.04), the Mac app's tray ("Start in safe mode"), the
  "can't start" page (§3), or a file `<data>/state/safe-mode` (for support).
- **Runs** (Epic 51 catalog ids, [`catalog.toml`](../../../shared/components/catalog.toml)):
  `core.postgres`, `core.api` (its process and its loops), the web port (the supervisor itself),
  `core.updates` (a manual update or rollback stays possible) and `logs.index` (53.18).
  **Doesn't run:** every other component, the always-on ones included: `core.pipeline`,
  `core.streaming`, `core.node`, `core.switchover` and every switchable one (`transcription`,
  `search.semantic`, `llm`, `livetv`, `livetv.slots`, `ext.*`, `youtube`, `cloud.link`,
  `backups`, `updates.auto`, …). The queue is not paused in the database (machines joined to
  the home keep working); only this machine's workers stay off.
- **Desired state is never written.** Safe mode is a boot flag, not an admin's wish: the
  `components` rows, holds and generations stay as they are, and the next normal start runs
  exactly what they say. Each component kept off reports phase `off` with condition
  `Reconciled` false, reason **`boot_flag`**, params `{"flag": "--safe-mode"}` (an existing
  reason code with English and Arabic words, `components.reason.boot_flag`), as the reconciler
  already does for `--no-mdns`.
- **What Epic 51 does not do yet:** its reconciler forces a component off for one boot only for
  the supervisor's own loops (`SupervisorLoop.Forced`, `cmd/maktaba-server/internal/supervisor/reconcile.go`),
  and it always opens `core.*` (`!comp.Switchable`). This story adds a boot-wide forced-off set
  to the reconciler (`Reconciler.SafeMode`), checked before the `core.*` rule, and tells the
  API's and the pipeline's runners through an env var (`MAKTABA_COMPONENTS_SAFE_MODE=1`, as
  `MAKTABA_COMPONENTS_RECONCILE` is passed today) so their loops and stage groups stay off too.
  Unlike `[components] reconcile = "off"` (every runner reports and acts on nothing, every
  process as `server.toml` builds it), safe mode runs less, not as built.
- **Lasts one start:** the flag is removed as soon as the supervisor reads it; the next restart
  is normal unless asked again. The console and the tray offer "Stay in safe mode" (writes the
  flag again before a restart).
- `/status.json` says `"mode": "safe"`; every app shows the safe-mode strip (53.06).

### 2. Cut-offs for children (existing, completed)

| Rule | Today | After |
|---|---|---|
| Backoff | 1 s → 60 s | same |
| Crash loop | 5 exits in 10 min → `failed`, retried every 5 min | same, and: after **3** slow retries fail (15 min), the role stays `down` until a person acts or the machine restarts; a **repair item** is raised |
| Crash loop, as Epic 51 reports it | the reconciler reports the component `failed`, reason `crash_loop` (part `supervisor`), and 51.14's health shows `restarts` and `last_error_code` | unchanged; the cut-off below keeps that phase and adds the repair item. The component's desired state is not touched: the cut-off is not a hold |
| Repair item | — | A notification (existing `notifications` table, admins) and a row on System health (item kind `components`, code `components_failed`, as 51.14 already raises): "{component} keeps stopping", the last error line, buttons Restart / Logs / Turn off (Settings → Components, `PUT /api/components/{id}` with `want = installed`) / Safe mode. In the console when the API is down, without Turn off. |
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
  buttons, so the admin sees it even if the launcher can't bind the port.
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

`{component}` is the Epic 51 component's own title (`components.<id>.title`, already in
English and Arabic; `components.transcription.title`, …). For the always-on parts the status
group names of 53.03 are used, translated (`status.group.streaming` = Playback / التشغيل,
`status.group.processing` = Processing / المعالجة, `status.group.database` = Database / قاعدة
البيانات, `status.group.api` = Maktaba service / خدمة مكتبة, `status.group.search` = Search /
البحث, `status.group.logs` = Log search / البحث في السجلات). The prefix is `status.group.`, not
`component.`, so it never collides with Epic 51's `components.*` keys.

## Acceptance criteria

- **Given** a pipeline worker that crashes at start,
  **when** it has failed 5 times in 10 minutes and 3 slow retries,
  **then** it stays `down`, the other roles keep running, and admins get "Processing keeps stopping" with the last error line.
- **Given** the admin clicks "Start in safe mode",
  **when** the home restarts,
  **then** only `core.postgres`, `core.api`, `core.updates`, the web port and `logs.index` run; `/status.json` says `safe`; every app shows the safe-mode strip;
  every other component reports `off` with reason `boot_flag`, and no `components` row, hold or generation changes.
- **Given** the home is in safe mode,
  **when** it restarts again without "Stay in safe mode",
  **then** it starts normally.
- **Given** `serve` exits 3 times in 2 minutes before ready on the current version,
  **then** the launcher stops starting it and serves the "can't start" page, and the Mac app shows its own page with the same buttons.
- **Given** that page,
  **when** the admin clicks "Start in safe mode",
  **then** `serve` starts with the safe-mode flag.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | supervisor policy, fake clock | 5 exits + 3 retries | `down`, repair item |
| T02 | unit | safe-mode flag | start | allowed roles only; flag removed |
| T02b | unit | reconciler with `SafeMode` and a desired state with `transcription` enabled | pass | `core.pipeline` and `transcription` closed, phase `off`, reason `boot_flag`; the store saw no write |
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
| `cmd/maktaba-server/internal/supervisor/safemode.go` (new) | Flag read, the allowed catalog ids. |
| `cmd/maktaba-server/internal/supervisor/reconcile.go`, `env.go` | `SafeMode` forced-off set before the `core.*` rule; `MAKTABA_COMPONENTS_SAFE_MODE` to every child. |
| `shared/components/runner/runner.go`, `pipeline/src/maktaba_pipeline/components/runner.py` | Read `MAKTABA_COMPONENTS_SAFE_MODE`: keep loops and stage groups off, report `boot_flag`. |
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

53.03; 53.18 (`logs.index`). Epic 51 (built): the reconciler (51.04), the loop and stage
runners (51.05, 51.06), health and the `components` system-health item (51.14). Needed by 53.07.
