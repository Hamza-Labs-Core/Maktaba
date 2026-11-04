# Story 53.07 — The fault rehearsal in CI

> Epic 53 · Phase 1 · Depends on: 53.03, 53.04, 53.05, 53.06 · Needed by: release gate

## Why

The rule since the 2026-10-28 outages: failure paths are proven in CI before they reach the
Mac. The update rehearsal does that for updates. Nothing yet kills
the API or Postgres and checks the UI still loads. That one crash never takes the UI down is a
promise only a test can keep.

## Description

### 1. The harness

`.github/workflows/fault-rehearsal.yml`, verdict job **fault-rehearsal**, built on the update
rehearsal's kit (`tools/rehearsal/build.sh`, `postgres.sh`, the Mac app's install layout on
Linux, the small seed): one home of **this commit**, started the way the launcher starts it, with
the web built in (`-tags embed_web`; unlike the update rehearsal, which doesn't build the web, the
UI is what this checks).

Each scenario is its own job and test (`api/fault_rehearsal_e2e_test.go`). A headless browser
(Playwright, already used by the web's tests) loads the web app before, during and after the
fault and records:

- the shell's first byte and whether the app's script ran (`window.__maktabaReady`);
- the status strip's text (53.06) and when it appeared;
- `/status.json` every 250 ms;
- the recovery console: sign in with the admin's recovery pass, load logs, run the scenario's fix;
- after recovery: sign-in and a library list work again.

Logs (all segments, supervisor events, the browser's console and screenshots) are kept as an
artifact when a scenario fails, as the update rehearsal does.

### 2. The scenarios

| # | Fault | How | Asserts | Budget |
|---|---|---|---|---|
| F01 | Pipeline worker crash | `kill -9` a worker | shell loads; viewers no strip; admins "Processing is paused"; restarted | strip ≤ 15 s |
| F02 | Pipeline crash loop | stand-in worker that exits at start | `down` after the cut-off; repair item; others fine | as 53.05 |
| F03 | Streaming crash | `kill -9` streaming | shell loads; strip `streaming_down`; player says the sentence; restarted | strip ≤ 15 s; back ≤ 60 s |
| F04 | **Postgres stopped** | `pg_ctl stop -m immediate`, then refuse to start (bad `postgresql.auto.conf`) | shell loads ≤ 1 s; strip `database_down`; console signs in with the recovery pass and shows Postgres's last error; "Restore a backup" listed | console ≤ 10 s |
| F05 | Postgres refuses connections | `pg_hba.conf` reject + reload | `database: degraded`; strip; recovers when fixed | strip ≤ 25 s |
| F06 | **API crash** | `kill -9` the API | shell loads; restarting page with what is down; restarted | back ≤ 30 s |
| F07 | API crash loop | an API build that exits at start (the rehearsal's "refuses what it migrated" trick) | console reachable; "Go back to version" listed; clicking it restores the previous release | console ≤ 10 s |
| F08 | `logs` role crash | `kill -9`, and a corrupt `logs.db` | viewers nothing; Logs page falls back to segments; index rebuilt from segments | rebuilt ≤ 5 min (small seed) |
| F09 | Supervisor crash loop | a `serve` that exits 1 before ready | launcher's "can't start" page on the web port; Safe mode button starts in safe mode | page ≤ 20 s |
| F10 | Safe mode | console "Start in safe mode" | only the allowed roles; `mode: safe`; strip; next restart normal | — |
| F11 | Disk nearly full | a small loop volume for `<data>/logs` (as update rehearsal 08) | `info` stops, `warn` kept, nothing else breaks | — |
| F12 | Two at once: streaming + pipeline | both killed | shell loads; strip priority right | — |
| F13 | **Off is not down** | `maktaba-server components disable livetv.slots --yes` and `components stop transcription --yes` (Epic 51 CLI, `api/components_cli.go`) | `/status.json` says `off` for both, `overall` stays `ok`; no strip, no repair item; the console lists them as off and held; turning them back on returns `ok` | — |
| F14 | Reconcile kill switch | `[components] reconcile = "off"`, then kill a worker | the processes run as `server.toml` builds them; status rows say `reconcile_off`; a crash is still `restarting`/`down` as usual | — |

### 3. When it runs, and the gate

- On every PR that touches `cmd/maktaba-server/**`, `cmd/maktaba-launcher/**`, `web/src/components/StatusStrip.tsx`,
  the recovery console, or the rehearsal itself; on every push to main; nightly.
- **Before every release:** the tag guard (`tools/release/tag-guard.sh`) requires
  `fault-rehearsal` green on the exact commit, as it does for `update-rehearsal`.
- `tools/rehearsal/fault-expected-failures.txt` lists scenarios whose story isn't merged yet,
  with the PR that fixes them (the update rehearsal's mechanism); from that PR's merge on, the
  scenario is required.

## Acceptance criteria

- **Given** each scenario F01–F14,
  **when** it runs on this commit,
  **then** its asserts hold within its budget.
- **Given** a commit that makes the web shell depend on the API (e.g. the SPA's index fetched through `/api/`),
  **when** F06 runs,
  **then** it fails.
- **Given** a tag on a commit where `fault-rehearsal` failed or didn't run,
  **when** the release workflow starts,
  **then** the tag guard refuses it.
- **Given** a failed scenario,
  **then** its artifact holds every service's segments, the browser console and screenshots.

## Test cases

The scenarios are the tests. Plus:

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `tag-guard.sh` with a fake checks API | missing / failed / passed fault-rehearsal | refuse / refuse / allow |
| T02 | unit | expected-failures file | scenario fails, PR not merged | reported, not counted |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `.github/workflows/fault-rehearsal.yml` (new) | Jobs per scenario, verdict job. |
| `api/fault_rehearsal_e2e_test.go` (new), `api/faultkit/` (new) | Harness, faults, browser driver. |
| `tools/rehearsal/build.sh` | `--with-web` to embed the SPA. |
| `tools/rehearsal/fault-expected-failures.txt` (new) | §3. |
| `tools/release/tag-guard.sh` | Require `fault-rehearsal`. |
| `docs/fault-rehearsal.md` (new) | Like `docs/update-rehearsal.md`. |

### Tests

CI only (it starts real processes and a browser). Runs locally like the update rehearsal, never
against a live install.

### Rollout

Lands first with every scenario in the expected-failures file except F01, F03 and F06 (which
should pass on today's code for the shell part); each story removes its own lines.

### Rollback

Remove the tag guard line; the workflow can stay as information.

### Dependencies

53.03, 53.04, 53.05, 53.06 (each turns on its scenarios). The harness can start right after
53.03.
