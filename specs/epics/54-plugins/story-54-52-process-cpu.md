# Story 54.52 — CPU of each process

> Epic 54 · Phase 5 · Depends on: 54.44, Epic 51 (51.14) · Built

## Why

The Plugins page shows the memory of each process that runs a plugin and says that processor use is not measured. The supervisor samples memory only. An admin deciding which plugin to turn off on a slow machine needs both.

## Description

- The supervisor samples each running process's CPU time with its memory, every five minutes, on macOS, Linux and Windows, and reports the share of one core used since the last sample as `cpu_pct` beside `rss_mb`, and per process in `cpu_parts` beside `rss_parts`. The same hysteresis keeps an idle machine from rewriting rows.
- `GET /api/plugins` carries it on each process of `resources.processes`; the page shows it beside memory and drops the sentence that says it is not measured.
- A shared process's CPU is the whole process's, shown with the plugins that share it, as memory is.

## Tests

- The reconciler's rows carry `cpu_pct` and `cpu_parts` from a fake sampler, and an idle process rewrites nothing.
- The page shows a shared group's CPU with who shares it.

## As built

- **Reading the processor time.** `hostinfo.ProcessCPU` reads each running pid's user and system
  processor time since the process started: `ps -o cputime` on macOS, `/proc/<pid>/stat`'s
  `utime` and `stime` at 100 ticks a second on Linux, and `GetProcessTimes` on Windows. A pid
  that is gone has no entry.
- **The reconciler.** The supervisor's reconciler reads processor time in the same five-minute
  sample as memory (`Reconciler.CPU`, set from `hostinfo.ProcessCPU` in `serve_reconcile.go`).
  `cpu_pct` is the processor time used since the last sample over the wall time between the
  two, times 100, so 100 is one whole core and a busy multi-threaded process can pass it.
  It is written beside `rss_mb` as the component's sum, and per `<role>/<instance>` in
  `cpu_parts` when more than one process runs, as `rss_parts` is.
- **When there is no number.** Each baseline is a pid together with its component, role,
  instance, restart count and start time. The start time is set at every start, including a
  restart asked for, which counts no crash. A process seen for the first time has no share until the next
  sample. So does a restart that got its pid back, another supervised process on the same pid,
  or a time that went back, whatever the new time is. While any running process of a component
  has no share, the component keeps the numbers it last had, or none: a sum of only some of its
  processes would read as the whole component's. A component with nothing running has no
  `cpu_pct`.
- **Linux units.** Linux ticks are converted as ticks × 10 ms, which cannot overflow; ticks ×
  1 s ÷ 100 overflowed after about 2.9 years of processor time.
- **Hysteresis.** A number is rewritten only when it moves by 5 points and by a tenth of the
  last written number (`cpuMoved`). Rows equal to the last written ones are never sent, so an
  idle machine writes nothing.
- **`GET /api/plugins`.** Each process in `resources.processes` carries `cpu_pct`. It is read
  as memory is (`measure`): the process's own entry in `cpu_parts`, including numbered ones
  such as `ml-0`, else the component's total. Its own `cpu_scope` says which, as `scope` does
  for memory. The two can differ for a sample: just after another process of a component
  starts, memory is per process at once while processor use keeps the component's last total.
  A shared process's number is the whole process's, shown with `shared_with`. The developer
  guide (`docs/developers/plugins/installing.md` and `.ar.md`) documents `cpu_pct` and
  `cpu_scope`.
- **The Plugins page.** The page shows a **Processor** column beside **Memory**: "25% of one
  core", or "…, with the other programs of the same part" for a component's total. The
  sentence saying processor use is not measured is gone, here and in the Plugins help topic,
  which now explains the number. The shared note covers both numbers. Words are in English and
  Arabic; `help.coverage.test.ts` refuses the old sentence in either language.
- **Tests.**
  - `internal/supervisor/cpu_test.go` (fake sampler): the shares and parts, no share from one
    sample, no rewrite for the same use, an idle machine writing nothing, a reused pid, a
    stopped component, and `cpuMoved`. From the review: a restart that got its pid back with
    more time than before, another process on the same pid, and a partial sample that keeps
    the component's last numbers. From the second review: a restart asked for, with the same
    pid and `Restarts` unchanged.
  - `internal/hostinfo/cpu_test.go`: the real reader grows while the test works, and ps's time
    forms. `cpu_linux_test.go` reads a stat line whose command name holds spaces and
    parentheses, and four years of processor time.
  - `api/internal/plugins/measure_test.go`, including per-process memory beside the
    component's processor use, and `published_pg_test.go` for the shared group's `cpu_pct`
    through the API.
  - `PluginsSection.test.tsx` T08: the shared group's processor use with who shares it, and no
    "not measured" sentence.

