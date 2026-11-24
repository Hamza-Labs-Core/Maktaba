---
schema: 1
kind: pr
pr: 1571
title: "Broadcast's Ctl tests wait for Run to stop before their temp dir is removed"
date: 2026-11-24
session: 71f9924d-5382-420c-bb6a-d0e85685bd01
agent: claude-code
prs: [1571]
---

## What happened
- Main's run 45298651218 (cb2529b69) failed `integration (rest)` on
  `TestCtlAlwaysOnIgnoresSleep`'s cleanup: "TempDir RemoveAll cleanup: unlinkat …/src:
  directory not empty".
- Three `Ctl` tests ran `go c.Run(ctx)` and only cancelled on return. `Run`'s `stopAll` waits
  for each channel's session, but it finished after the test, while `t.TempDir`'s cleanup
  removed the directory the always-on channel was still writing segments into.
  `TestCtlAlwaysOn`, in the same file, already waited.
- #1571 adds `startCtl`: it runs the `Ctl` and registers a cleanup that cancels and waits for
  `Run`. Registered after `newTestCtl`'s temp dir, that cleanup runs first.
- The same run's iPad 6/6 was cancelled 9 minutes into "Make, boot and settle the simulator",
  after choosing the iPad and before any test ran. The log says nothing more, and it was the
  run's only cancelled job.

## What we learned
- `t.Cleanup` runs last first. A helper that registers its own cleanup after `t.TempDir` gets
  to stop the writers before the directory is removed.
- It did not reproduce locally: the original file passed 100 runs under `-race` on a 28-core
  Mac. The CI runner's load opens the window.

## Keep doing
- Wait for a goroutine started in a test to return, not just cancel it, when it writes into
  the test's temp dir.

## Open
- The iPad shard cancelled during simulator setup, with no word on why.
