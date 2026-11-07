---
schema: 1
kind: incident
title: Sixteen orphaned `yes` processes held the owner's Mac for three days and crashed WindowServer
date: 2026-11-07
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
---

## What happened
- On 2026-11-04 at 08:41, a subagent recreating a failing streaming-cluster test under CPU load started two groups of `yes` busy loops (6 and 10 processes) in the background.
- It cleaned up with `kill $(jobs -p) 2>/dev/null`. The command ran in a different subshell from the one that started the jobs, so `jobs -p` was empty, nothing was killed, and `2>/dev/null` hid the error. Earlier runs in the same session used `pkill yes` and cleaned up fine.
- The processes were reparented to launchd and each used about 87% of a core until 2026-11-07, about 4,000 CPU-minutes each. That is roughly 14 of the Mac Studio's 28 cores. The load average sat at 77–103, the CPU was 0% idle, and logd, mds_stores and audiomxd logged CPU-overuse reports overnight. The delegates' builds and tests ran slower for those three days.
- At 04:33 on 2026-11-07, WindowServer aborted, which ended the owner's login session; everything restarted at about 06:49. It aborted in `iosurface_create_common` while capturing a window (`_XWindowListCreateImage`), and the kernel's triage note was `vm_map_copyout_kernel_buffer failed`. The machine had no idle CPU, and 73% of its time went to the system, so the kernel could not serve WindowServer. Browser tests from another project were taking screenshots at the time. They made the request that failed, but the saturation is why it failed. The crash ended every agent session's background watchers, including the one that was to merge #760.
- A diagnosis found the processes by start time and parent PID 1, and they were killed by PID.

## What we learned
- A background load generator outlives the agent that started it. Nothing reports it: the test passed or failed, the session moved on, and the cores stayed busy.
- A saturated machine does more than run slowly: system services fail, and the user's whole session can go with them.
- `kill $(jobs -p)` only works in the shell that started the jobs. Agent tool calls often run each command in a new shell, so the job table is empty.

## Keep doing
- Start CPU load generators with a hard time limit (`timeout 120 yes > /dev/null &`), record their PIDs (`$!`) in a file, kill them by those PIDs, and then confirm none are left (`pgrep -x yes` prints nothing).
- When a machine is slow, look for long-running processes whose parent is PID 1 before suspecting the app.

## Stop doing
- Cleaning up background load with `kill $(jobs -p)`, or hiding a cleanup command's errors with `2>/dev/null`.
