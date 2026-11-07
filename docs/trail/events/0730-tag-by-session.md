---
schema: 1
kind: pr
pr: 730
title: Tags are pushed by the release session; auto-tag.yml is removed
date: 2026-11-06
session: 63f3835c-f038-4cd2-af45-0af48a3e89dc
prs: [725]
---

## What happened
- `.github/workflows/auto-tag.yml` is deleted (it was already disabled in the repository settings), and with it the plan for a `RELEASE_TAG_DEPLOY_KEY` deploy key from #725.
- A scheduled release session pushes each tag instead, every 30 minutes on the owner's machine: it collects the main commits whose `ci.yml` and `update-rehearsal.yml` push runs both succeeded, runs `tools/release/next_tag.py --green FILE` to choose the commit and tag, pushes the annotated tag with the owner's git credentials, then builds the trail snapshot.
- `next_tag.py` and its rules are unchanged. The tests that loaded auto-tag.yml are gone; the choice tests, the `TRAIL_ONLY` = push paths-ignore test, release.yml push-only and the other tag workflows' by-hand `tag` input stay.

## What we learned
- Why a session and not a workflow: the homes' `releaseverify` accepts only a release signed by a `push` run, and a tag pushed from a workflow with `GITHUB_TOKEN` starts no run, so a workflow would need a long-lived write key in the repository's secrets. A session's push with a person's credentials starts the `push: tags` runs and needs no stored key.

## Keep doing
- A tag workflow that is missing a leg is re-run (release.yml) or dispatched by hand on its tag (the others); release.yml is never dispatched.

## Stop doing
- Ending a shell command with `git checkout <ref> -- .`: it silently overwrites every uncommitted edit. In this session it wiped the whole change, which was rebuilt from the edit scripts. Commit WIP early.
