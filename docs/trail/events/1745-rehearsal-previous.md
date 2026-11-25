---
schema: 1
kind: pr
pr: 1745
title: "Update now goes on past a machine's copy; the update rehearsal's old-release scenarios are pinned to v0.3.162"
date: 2026-11-25
session: af4db1a0-1123-496c-869f-9a21bd3c8aad
agent: claude-code
prs: [1605, 1609, 1647, 1732]
---

## What happened
- **v0.3.163 was published at 08:12 UTC and became the rehearsal's previous release.** Four scenarios (10a, 17, 21, 21b) failed at once, on every branch, with nothing changed in them.
- **Three were the rehearsal's:** 21 and 21b try this commit under a release that puts a backup back by itself, and v0.3.163 is the first that does not (#1605); 17 sets up by hand the state slots 0237 and 0238 put right, and v0.3.163 has them. They run from v0.3.162, pinned in the kit (`guard/`). 21b also runs under the previous release's launcher, which must hold the update.
- **One was the product's, shipped in v0.3.163:** Update now was refused (409 `machine_copying`) while a machine copies the home's database. #1609's text, the docs and the code's comment all say it does not wait; `RunNow` ran every blocker and passed only a recent crash. Passed now, audited. A blocker passed no longer ends the look: what comes after it is still asked.

## What we learned
- **"The previous release" is a moving premise.** A test about how the first update to a change goes is true for one release only: the day that release is published, it tests nothing, or fails. Such a scenario pins the last release from before, with the reason.
- **A rehearsal from the new release is where a new gate is first met.** The refusal could not be seen while the previous release had no such gate; the unit test asked the blocker, never `RunNow` with it.
- **A state set up by hand has to be one that can exist.** 17's was real for a home before 0238; from a release at 0238 no code makes it. Ask who makes it before calling it a bug, and before calling it an artefact.
- **On a Mac a killed server's children live on** (Linux ends them with it): 21's old release then finds its port taken. Run it on Linux.
- A background command's shell may be killed (exit 144) while the command goes on: look for the process before starting it again.

## Keep doing
- Run the whole rehearsal against a release the day it is published, before the next pull request meets it.

## Open
- 10a tolerates the refusal where the home runs the previous release: drop it once v0.3.163 is not it. An automatic run has no upper bound on the wait: #1667.
