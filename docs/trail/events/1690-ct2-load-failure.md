---
schema: 1
kind: pr
pr: 1690
title: "test_ct2's flake was a product race: a failed model load stood its thread down without the lock"
date: 2026-11-24
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- `test_a_load_failure_reaches_every_waiting_file_and_is_tried_again` failed on #1669 with
  `assert 1 == 2` (issue #1678, job 138865201207).
- `BatchedTranscriber._run` told the waiting files of a failed load and then set `_thread = None`,
  without the lock `_ensure_thread` takes. A file sent in between got the old failure untried, or
  was queued for a thread already ending and waited until another file came.
- #1690 empties the queue and stands down under the lock, and tells the files after it. With a
  1 µs thread switch main's code hung in 90 of 240 loops of that test; the new code passed 9,000.
- It also sets the TV launcher e2e's `pauseAt` a minute ahead (#1679's review).

## What we learned
- `sys.setswitchinterval(1e-6)` turns a window of a few bytecodes into a common failure.
- A `queue.Queue` whose `get_nowait` sends a file as it finds nothing more makes the window itself
  the test.
