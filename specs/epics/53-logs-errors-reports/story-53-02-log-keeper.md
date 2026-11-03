# Story 53.02 — The log keeper: every component's log on disk

> Epic 53 · Phase 1 · Depends on: 53.01 · Needed by: 53.04, 53.09, 53.17

## Why

Today every child inherits the supervisor's stderr and launchd writes the whole tree into one
`server.log` (50 MB, one `.1` copy). A chatty pipeline pushes a crash of the API out of the file
within hours, and the in-memory rings die with their process. Logs must be on disk, per
component, capped, and written by something that needs nothing else running (README D1).

## Description

### 1. Capture

The supervisor gives each child its own stdout and stderr pipes instead of its own stderr. A
reader goroutine per pipe:

1. reads lines (≤ 64 KB; longer lines are cut and marked `attrs.cut: true`);
2. keeps a line that already parses as a 53.01 object; wraps anything else (Postgres, ffmpeg,
   a Python traceback) as `{"msg":"raw_line","level":<guessed>,"attrs":{"text":…}}`, reusing the
   level guess of `cmd/maktaba-server/internal/logfiles` (`panic:`, `fatal error:`, `Traceback` →
   `error`, since #383);
3. fills `service` (the role), `node`, and `ts` when missing; applies the 53.01 rules once more
   (defence in depth for raw lines);
4. appends to the component's current segment; and also writes the line to the supervisor's
   stderr **only if** it is `warn` or above (so `server.log` keeps the important lines for the
   tray and old tools, at a fraction of the volume).

A reader never blocks a child: each pipe has a 4 MB buffer; when the disk writer falls behind,
`debug` lines are dropped first, then `info`, and a `log_lines_dropped` line with counts is
written once a minute while it lasts. `warn` and above are never dropped while the disk accepts
writes.

### 2. Segments

```
<data>/logs/segments/<service>/<yyyymmdd-hh>.ndjson        (the open hour)
<data>/logs/segments/<service>/<yyyymmdd-hh>.ndjson.zst    (closed hours, zstd level 3)
<data>/logs/segments/<service>/<yyyymmdd-hh>.idx            (byte offset every 1,000 lines + first/last ts)
```

- One file per component per hour; a file that reaches 64 MB rolls early (`-1`, `-2`).
- `fsync` every second and on roll; a crash loses at most one second.
- `<data>` is the data folder the home already uses (on the big disk on the live Mac,
  PRs #350–#353); joined machines write their own under their data folder (and their share
  copy stays as today).
- The desktop shell's `app.log` and the launcher's log move into the same tree as
  `desktop/` and `launcher/` segments.

### 3. Retention and the disk cap

A janitor goroutine runs every 5 minutes:

| Rule | Default | Setting (`[logs]` in `server.toml`, and the admin page in 53.11) |
|---|---|---|
| `debug` lines | 24 h (they are written to `<service>/debug/`, so dropping is deleting files) | `debug_retention` |
| everything else | 14 days | `retention` |
| total segment size | 2 GB | `max_segments_size` |
| data volume free space floor | 5 GB: below it, stop writing `debug` and delete the oldest hour until above | `min_free` |
| hard floor | 1 GB free: write only `warn` and above | — |

Oldest files go first, across all components, except the newest 24 h of `warn`+ lines of every
component, which are kept whatever the cap (a component that logged little is never wiped out
by a noisy one).

### 4. Reading without the index

`cmd/maktaba-server/internal/logfiles` gains a reader over segments: by component, level and a
time window, newest first, using the `.idx` offsets, bounded to 50 MB read per request. The
recovery console (53.04) and the existing `/api/admin/logs/files` use it. It works with the
`logs` role, the API and Postgres all down.

### 5. The rings stay

The in-memory rings and `/logs/recent` stay (the diagnostics bundle uses them and they cost
little); the viewer moves to the index in 53.11.

## Acceptance criteria

- **Given** the API logs 100 lines and crashes,
  **when** the supervisor restarts it,
  **then** all 100 lines are in `segments/api/` in order, with nothing from the pipeline mixed in.
- **Given** Postgres writes a plain-text line,
  **when** the keeper reads it,
  **then** it is stored as a 53.01 line with `service: postgres`, `msg: raw_line` and the text scrubbed.
- **Given** the pipeline writes 20,000 debug lines a second for a minute,
  **when** the disk writer can't keep up,
  **then** the pipeline never blocks, debug lines are dropped, every `warn`+ line is kept, and `log_lines_dropped` says how many.
- **Given** segments total 2.2 GB,
  **when** the janitor runs,
  **then** the oldest hours are deleted until ≤ 2 GB, and each component's newest 24 h of `warn`+ lines remain.
- **Given** the data volume has 900 MB free,
  **when** a child logs `info`,
  **then** the line is not written, a `warn` line still is, and System health shows "Low disk: logs reduced".
- **Given** the `logs` role, the API and Postgres are stopped,
  **when** the console asks for the last 15 minutes of `api` errors,
  **then** the segment reader answers in ≤ 1 s.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | fake child writing JSON and raw lines | capture | segments hold 53.01 lines, raw ones wrapped |
| T02 | unit | writer blocked | 100k lines | child not blocked; drop counts right; no `warn` lost |
| T03 | unit | files over the cap, fake clock | janitor | oldest removed, 24 h `warn`+ floor kept |
| T04 | unit | 65 MB in one hour | write | rolls at 64 MB |
| T05 | unit | segments + `.idx` | read window | right lines, ≤ 50 MB read |
| T06 | integration | real supervisor with stand-in children | kill -9 a child mid-write | at most the last second lost; next line in the same file |
| T07 | unit | `debug` vs `info` | retention | debug gone after 24 h, info kept |

## Edge cases

- **Clock jumps back:** the segment name uses the hour at write time; a reader sorts by `ts`
  inside the `.idx` range, so a repeated hour appends to the same file.
- **A line with a secret the child didn't scrub:** the keeper's own pass (§1.3) catches the
  rule list's cases.
- **Windows and Linux apps** (Epics 38, 39): the same supervisor code; the paths follow the data
  folder.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/supervisor/run.go`, `stderr_stamp.go` | Per-child pipes; hand lines to the keeper. |
| `cmd/maktaba-server/internal/logkeeper/` (new): `capture.go`, `segment.go`, `janitor.go`, `reader.go` | §1–§4. |
| `cmd/maktaba-server/internal/logfiles/logfiles.go` | Read segments as well as the old files. |
| `cmd/maktaba-server/internal/config/` | `[logs]` section and defaults. |
| `apps/desktop/src-tauri/src/agent.rs`, `applog.rs` | `server.log` stays (smaller); `app.log` into `segments/desktop/`. |
| `cmd/maktaba-launcher/logfile.go` | Launcher lines into `segments/launcher/`. |
| `docs/troubleshooting-logs.md` | Where the logs are now. |

### Tests

T01–T07 in `cmd/maktaba-server`'s unit job. **CI only:** the fault rehearsal (53.07) checks
that a killed child's last lines are in its segment.

### Rollout

Touches the supervisor, so `update-rehearsal` must pass on the exact commit before a release.
Ship with `server.log` unchanged for one release (both written), then switch `server.log` to
`warn`+ only.

### Rollback

Revert: children inherit stderr again; the segment files stay on disk and age out by hand
(documented).

### Dependencies

53.01. Needed by 53.04, 53.09, 53.17.
