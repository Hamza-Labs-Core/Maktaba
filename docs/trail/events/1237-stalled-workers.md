---
schema: 1
kind: pr
pr: 1237
title: "A slow spell no longer restarts Postgres or stalls the queues, and admins can see and fix a stall"
date: 2026-11-14
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
agent: claude-code
---

## What happened
- On 2026-11-13 the live home's queues stopped. The home answered, but no job ran for hours.
  Two pull requests came of it: #1232 (merged as 13820675d) and #1237 (merged as c2f16020c).
- The root trigger was this session's own leftover: an Epic 57 agent had started 168 shell
  busy loops (three per core) as a CPU-stress harness around an Android test on 2026-11-12 and
  never stopped them. They ran two days on the Mac that hosts the live home, with two orphaned
  headless Firefox processes. Load average was about 500 on 28 cores; a probe of the home API
  went from milliseconds to seconds.
- On that starved machine a 15 s look of `pgbundle.WatchReach` timed out part way. A failed
  "is the cluster link required" statement was read as "not required", which looked like a
  changed setting, so Postgres was started again, and again one look later when the statement
  was answered: 50 such restarts on 2026-11-13.
- Most "hung" kills of Postgres came seconds after the supervisor had itself asked it to
  stop: the probe went on counting a server in shutdown, and the restart's 30 s grace ended in
  SIGKILL. After a kill the next four starts failed with `pre-existing shared memory block is
  still in use` (backends of the killed postmaster still attached), reaching "crash loop,
  retrying in 5m".
- Each restart dropped every connection. The pipeline workers stayed alive, answered
  `/healthz` with ok, and ran nothing: a task cancelled mid-statement on a dropped connection
  waited forever in asyncpg (the rollback at transaction exit, the pool release), and the
  LISTEN connection was never reopened. The main worker was stalled about two hours, the two
  transcription workers nineteen.
- #1232 made a half-answered look change nothing. #1237 added: a child asked to stop is not
  probed, Postgres counts as hung only after five minutes and gets two minutes to stop, a
  start waits for the old server's shared memory, Python workers get a five minute start
  grace; the worker bounds its database waits, reopens its listener, answers 503 when it has
  not looked at the queue or reached the database for five minutes, and exits 75 when it
  cannot recover; a plugin statement that meets a lost connection runs once more and a
  first-party job that meets a restarting database fails retryable; the Queues page names each
  stalled worker and an admin can restart the stalled ones; System health and the Queues page
  say when the machine is overloaded.
- #1232 merged within seconds of being opened, with only its first commit. The session had
  run `gh pr merge --merge --auto`; with no required check pending, `--auto` merges at once.
  Four later pushes went to the merged branch and reached nothing, and the session reported
  them as part of the open pull request until a status read showed it merged. The rest went
  into #1237.
- The new real-Postgres test of the start-after-kill wait had only been compiled locally. In
  CI it hung the package for ten minutes: the test kept the server's output in a buffer (a
  pipe), and the processes it stops on purpose held the pipe open, so `Wait` on the killed
  postmaster never returned. The output goes to a file now.
- The stalled workers on the live home were restarted by hand from a shell (SIGTERM was
  ignored; SIGKILL, as the supervisor's own escalation), with the owner's yes each time.

## What we learned
- A leftover test process on the machine that hosts the live home is an outage. When the live
  home is slow, read `uptime` and the parentless processes (`ps -ww -Ao pid,ppid,pcpu,command`
  with ppid 1) before reading the database.
- "Asked apart, so a database not migrated yet still answers" must tell "the table is not
  there" from "no answer": only the first is a value.
- A liveness probe must stop when its child has been asked to stop, or every slow shutdown is
  a hang.
- SIGKILL of a postmaster leaves backends holding its shared memory; a start made at once is
  refused. Wait for the segment, do not count the refusals as crashes.
- asyncpg waits without limit for a cancelled statement's acknowledgement on a connection the
  server dropped. Bound the release and never send a rollback on a cancelled or closed
  connection.
- A health answer of "ok" has to mean "can start a job", measured by the claim loop's own
  progress, not by the process being up.
- `exec.Cmd` with a buffer for output makes a pipe, and `Wait` waits for every holder of it,
  children included. A test that stops or orphans children gives the command a file.
- `gh pr merge --auto` is not "merge when green" on a pull request with no pending required
  check: it merges now.
- `affected-tests` knows Python tests through a coverage map built on main: a source file
  tested only by a new test file reads as untested until the map is rebuilt.

## Keep doing
- Read a session's or an agent's report as a claim: the status read of the pull request, not
  the memory of having pushed, is what showed #1232 merged.
- Hand a failing check on an agent's branch to that agent while it still works there.

## Stop doing
- Starting CPU-stress loops on this Mac, and closing a multi-agent effort without checking for
  parentless processes it left.
- Passing `--auto` to `gh pr merge`.
- Reporting pushed commits as "in the pull request" without reading the pull request's state.

## Open
- Which task hung on the live home is inferred; the hang was reproduced on the worker's
  heartbeat against a scratch Postgres, not on the claim loop.
- Nothing in #1237 was run end to end against a real supervisor with a truly hung worker, and
  the notices were not looked at in a browser.
- The last resort after Postgres's two minute stop grace is still SIGKILL, with no immediate
  shutdown first; stopping the whole server can now take two minutes.
- Stalled-worker detection per worker rests on the jobs a worker holds and the text of the
  `claimed` job event: the database keeps no record of which worker started which job.
- Third-party (wasm) plugins still fail a job for good on a lost database connection.
- The machine-overloaded notice covers the home machine only, and not Windows.
- The live home runs v0.3.135 and has none of this until a release carries it.
