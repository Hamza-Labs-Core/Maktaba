---
schema: 1
kind: pr
pr: 528
title: CI runs only the jobs a change touches
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- Requirement: CI runs only what a change touches. An Android-only PR ran pipeline integration, Go replica, e2e and all nine Player UI shards; a Remote rename ran the Player UI tests. ci.yml's `code: '**'` filter ran every job on every PR (Apple UI shards 27-53 min on a Remote rename).
- `.github/ci-paths.yml` holds one path filter per job or part, derived from what each workflow builds and tests (Go modules and what their tests read, the pipeline and the docs/help its tests read, web and the two api files help.coverage reads, tokens, each app). ci.yml's `changes` job evaluates it on a pull request only; on a push to main/release/* every output is true, so main and the tags get the full run (mac/win/linux app builds now run on every push too). The update rehearsal's triggers are unchanged.
- Parts `_lint.yml` (repo checks always; go/pipeline/web), `_unit.yml` (go/pipeline/web/tokens/desktop_shell), `_integration.yml` (integration/compat_prev), `_node-packaging.yml` (packaging/release_venv) and `_apple-apps.yml` (build/player_ui) take boolean inputs. Makefile `lockfile-check` split into -py/-web/-go.
- Apple: the Player UI shards run for Player/, Shared/, MaktabaKit sources, UITests/, PlayerMacTests/ and the Player schemes. The shared project.pbxproj counts only when `tools/ci/apple_player_scope.py` finds a Player object among its changes (it parses the file at HEAD^1 and HEAD; the Remote rename caaf91a0 and Remote icon 3912dca8 come out false, UI-test changes true).
- e2e and perf-ci need integration but now say `!cancelled()` and run when integration was skipped (else a web-only change would skip them silently).
- ci-success loops over every need: success/skipped pass, failure/cancelled fail, `changes` must succeed.
- Guard: `tools/ci/ci_paths.py guard` in the lint job's always-run `repo` part. It simulates ci.yml's real `if:`/`with:` expressions: every job is gated by filters (and runs when any one alone is true) or marked `# ci-paths: always-run|push-only|pr-only|not-a-gate`; every filter is used; every gate is in ci-success's needs; a push runs everything; every tracked file is matched by a filter or listed in NOT_IN_CI. `ci_paths.py which <files>` shows what a change runs.
- Tests: tools/ci/tests (Android-only, Remote-only, Player, tvOS, pipeline-only, API-only, docs-only, workflow change, ci.yml, shared migrations, push, fork, guard negatives) and the pbxproj scope tests on the real project file. pipeline's test_docs_links reads the `unit_pipeline` filter instead of the old `meta` one.
- Merged at fad707d8.

## What we learned
- A job that `needs` a skipped job is itself skipped unless it says `!cancelled()`; a path filter can silently skip downstream jobs.

## Keep doing
- Jobs run only when their related code changes. Until a filter exists for something, merge on the related checks.
