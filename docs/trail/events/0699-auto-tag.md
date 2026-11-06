---
schema: 1
kind: pr
pr: 699
title: Releases are automatic: auto-tag.yml tags each green commit of main
date: 2026-11-06
session: 84d3d9a2-3f4d-45b9-86ed-f87f73ca5379
---

## What happened
- Tags had stalled at v0.3.82 with about 25 merges after it. `auto-tag.yml` now runs when CI or the update rehearsal finishes green on main. It tags the newest commit both passed on as the next patch version (`tools/release/next_tag.py`), then dispatches every `on: push: tags` workflow on that tag.
- Each tag workflow gained `workflow_dispatch` with a `tag` input. `_tag-guard.yml` refuses a dispatched run whose ref is not the tag. `relay.yml` and `packages.yml` treated only `push` as a tag run before, and now also accept a dispatch on the tag.

## What we learned
- A dispatch with `--ref vX.Y.Z` runs with `github.ref` = the tag, `github.ref_name` = the tag name and `github.sha` = its commit. The release workflows read only those, so apart from the trigger they needed no change. The two places that checked the event were relay.yml (`github.event_name == 'push'`, `gh run list --event push`) and packages.yml (`workflow_run.event == push`).
- web-release.yml is not a tag workflow on purpose: release.yml already uploads the web bundle, so auto-tag leaves it out.
- The auto-tag choice is strict: CI must have concluded `success`. A run that failed only on the flaky iPhone/iPad shards, which the tag guard accepts, is not tagged; the next green commit is.

## Open
- Not yet confirmed: that `packages.yml`'s `workflow_run` fires after a dispatched release (workflow_run chains are limited in depth; the twice-weekly schedule covers it if not), and that `GITHUB_TOKEN` may push a tag on a commit that changes workflow files. The first auto tag will show both.
