---
schema: 1
kind: pr
pr: 516
title: "affected-tests: changed code needs its tests changed"
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [469, 510, 528]
---

## What happened
New rule: a push that changes code also changes the tests that cover it, and the push fails at once, before CI.
The trigger: the Player's sign-in became a segmented choice, and the Mac UI tests (`PlayerMacTests/UI/PlayerMacMenuUITests.swift`, which tap "With a code") went stale with nothing running them. The old trail named #469 as the change; git shows the segmented `Picker` in `StartViews.swift` since b55059c9b (Epic 42) and #469 touched no sign-in file, so the exact change is unclear (corrected from git: the old trail said #469 made the sign-in a segmented choice). The test was fixed in 47fb1a75a (segments are radio buttons on the Mac).
`tools/affected-tests` lists a range's tests by platform, from a coverage map, the UI search (identifiers and the strings on the changed lines, keyed strings resolved) and naming; the web adds its import graph.
`gate` blocks in three cases: a source with none of its tests changed; a UI platform whose tests use the changed phrase but did not change; a test still using a string that no source has any more. A commit trailer `No-Test-Change: <reason>` lets a push through.
The hook: `tools/affected-tests/install-hook.sh` writes `.git/hooks/pre-push`, which runs `.githooks/pre-push`. It runs Go, Python, web, Android/JVM and MaktabaKit tests. Apple UI targets are only compiled (`build-for-testing`, unsigned), and `safeToRun` refuses any other `xcodebuild test`.
CI: the `affected-tests` job reports into `ci-success`. The map is built in its own `coverage-map.yml` (`workflow_run` after CI on main).

## What we learned
- `core.hooksPath` was rejected: the pre-commit framework refuses to install with it set.
- The map is not built in ci.yml: the tag guard needs CI's whole run on the commit green, so a map job there could block a release and would add to its wait.
- Granularity: Go per package (`-coverpkg` over the module); Python per test (`--cov-context=test`); MaktabaKit and the Mac Player per xccov target; Android per JaCoCo module, through an init script; web per test file, from its import graph. iPhone/iPad UITests are not measured: that run takes most of an hour, so coverage could hit its 60-minute limit.
- Failed first: the first pytest-context map pinned 100+ modules on whichever test imported them first. Now a `-p pytest_preimport` plugin imports the package before any test, and only lines inside functions count.
- Failed first: a UI hit on any platform satisfied the gate, which would not have caught the stale Mac tests. Strict terms (identifiers, phrases with a space) now need a change on each test platform that uses them.
- CodeQL alert #108 (cache poisoning) was raised against the first `coverage-map.yml`, which checked out the triggering run's `head_sha` and used setup-go/setup-uv caches. Fixed, not dismissed: it now runs only for a CI run of a push to main in this repo (or a manual run on main), checks out its own commit, has every cache off, and reads artifacts as data only.
- The `Player UI tests (mac)` job failed on this PR with "the home picker" in `PlayerMacMenuUITests`. It ran only because this PR touches `_apple-apps.yml`, and `feat/airplay` failed the same way: the existing Mac UI window bug that #510 fixes, not this change.
- Fitted to #528's path filters: `affected-tests` is `# ci-paths: always-run, pr-only` (every change may need its tests). `tools/affected-tests/**` sits in the `_go` group (it is in GO_MODULES). `.githooks/**` and `coverage-map.yml` are listed in `NOT_IN_CI`.

## Keep doing
- A code change ships with its tests changed in the same push.
- Keep `coverage-map.yml` cache-free and on its own commit: any `workflow_run` workflow with caches brings the CodeQL alert back.

## Open
- Not verified until it runs on GitHub: the coverage-map workflow, and the xccov and JaCoCo fragment steps (JaCoCo via the init script was checked locally for `:client` and `:core`).
