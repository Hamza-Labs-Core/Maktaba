---
schema: 1
kind: pr
pr: 1023
title: "Update rehearsal scenario 16.7: wave 7's TV change across an update"
date: 2026-11-08
session: 7dbed937-db8e-4df9-929f-611b727311e5
---

## What happened
- Added update-rehearsal row 16.7 (story 54.34, T02). It was the one Epic 54 Phase 4 wave row missing from issue #804. The merge commit is 1368dd70a.
- `api/rehearsal_wave7_e2e_test.go` takes one TV change through a real Epic 33 swap across the update. The home runs its TV slots and each release's own pipeline.
  - On the previous release (0.3.105), a TV setting saved with staging on creates the change. `tv_stage` stages it in-process; the stage's switch is set to `inproc` because that release already has the plugin. The admin then approves the change to swap six hours later.
  - The update runs while that swap is still pending. Afterwards the change is still approved on its release.
  - On this commit, a `tv_gc` runs in the runner first. Then Swap now: `tv_swap` runs in the runner, the front door makes the change's slot live (blue -> amber), and the change goes `live`.
  - The admin's warm rollback runs `tv_rollback` in the runner. The previous release is live again on blue.
- `tools/rehearsal/sleepy` gains `MAKTABA_REHEARSAL_WITNESS`. Stages named there do their real work, with the same start step and side/release summary that the sleepy stages write.
- 16.7 passed in dispatched run 42485998908 and in the PR's own run, both on head 562ff78d7.

## What we learned
- The change's verification is the only part the rehearsal cannot run. The soak starts only once a warm channel airs real media (`ev.warm > 0` in `streamverify.go`). Everything else runs as the real code with zero channels: stage, approve, swap job, front door swap and rollback operations, and the reverse commit. A swap operation with no channel rows ends after its settle time.
- A test-made TV release must take its `sched_gen` from `nextval('tv_sched_gen_seq')`. The first run's fixed generation 1 collided with the stage's release (`tv_releases_sched_gen_key`) and cost the stage job a second attempt. Scenario 11 never hit this because it plays the stage.
- `POST /api/tv/rollback` answers 202, not 200.
- An admin's stage-switch flip sets `impl_changed_at`, so the update's automatic flip to `plugin` (`engine/sync.py`) leaves that stage alone. That is why 16.7 flips only `tv_stage`.
- Approve with a distant `swap_at`, then use Swap now after the update. This keeps a change in flight across an update deterministically: the update hold leaves approved changes alone.

## Keep doing
- When a wave's stages must do real work in the rehearsal, witness them with `MAKTABA_REHEARSAL_WITNESS` instead of sleeping them, and judge each job by its `payload` keys.

## Open
- Issue #804 was left open for 16.9's workflow row, which came with wave 9 (#998, merged the
  same day). That row is now on main; issue #804 is still open.
