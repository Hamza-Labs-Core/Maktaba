---
schema: 1
kind: pr
pr: 1443
title: "Story 54.52: the supervisor measures each process's processor use, and the Plugins page shows it"
date: 2026-11-19
session: 9aa2417d-57dc-4c99-a4b6-462f5098d816
agent: claude-code
---

## What happened

Story 54.52 shipped in #1443 (merge 168fce430).

- `hostinfo.ProcessCPU` reads each running pid's processor time: `ps -o cputime` on macOS, `/proc/<pid>/stat` on Linux, `GetProcessTimes` on Windows.
- The reconciler turns it into `cpu_pct` (the share of one core since the last five-minute sample) beside `rss_mb`, and `cpu_parts` beside `rss_parts`.
- `GET /api/plugins` carries `cpu_pct` with its own `cpu_scope`, and the Plugins page shows a Processor column.
- The developer guide and the Plugins help topic were updated in English and Arabic.

Three Kimi K3.1 Pro review rounds found ten real defects, all fixed. The seven code defects each got a test that fails without the fix:

- A pid reused with more processor time than the old process had.
- A restart that got its pid back. A requested restart does not bump `Restarts`, so the baseline is now keyed by pid, component, role, instance, restart count and start time.
- A partial sample written as the component's total. While any running process has no share, the component now keeps its last numbers.
- A Linux tick overflow after about 2.9 years of processor time (ticks × 1 s ÷ 100).
- A low reading that never went back to 0.
- A total that disagreed with its parts under per-part hysteresis. The total is now the sum of the written parts.
- Processor use dropped by the API for the one sample after a second runner starts. Hence `cpu_scope`.

The other three were fixed without a red-first test:

- A stale "not measured" sentence in the Plugins help topic. `help.coverage.test.ts` now refuses it.
- A stale "CPU is not measured" sentence in the developer guide (`installing.md`, en and ar).
- The macOS and Windows readers not run in CI. The launcher matrix now runs them: a workflow step, not a test.

## What we learned

- `Supervisor.Restart` does not increment `Restarts`, so the restart count alone cannot identify every process start. The baseline also needs `ChildStatus.StartedAt`.
- Hysteresis applied to a sum and to its parts separately lets them disagree for good. Apply it to the parts and derive the sum.
- A rule "moved by N and by a tenth" never lets a value under N return to 0. Writing any drop to 0 once keeps an idle machine quiet and the number true.
- `ps -o cputime` and `/proc/<pid>/stat` are cumulative since start. A share needs two samples of the same process: a first sample, or another process on the pid, has none.
- The Kimi provider sometimes ended a run within two seconds with no output, several times in a row. Retrying after a pause worked; an empty run is not a review.
- The supervisor reports each process's processor use and the component's total, and does not divide a shared process's use among the plugins it runs, by design.

## Keep doing

- Ask the reviewer for file:line and a failing scenario, turn each check off to see its test fail, and record every finding and what was done with it in a PR comment.
