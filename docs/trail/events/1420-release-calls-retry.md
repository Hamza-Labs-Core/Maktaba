---
schema: 1
kind: pr
pr: 1420
title: "Every GitHub release call goes through gh-retry.sh, not only the uploads"
date: 2026-11-15
session: 781cf353-02d8-405a-a6d2-24b6ebc07354
agent: claude-code
prs: [1404]
---

## What happened
- v0.3.147's Release run 43732871322 (attempt 1, job "sign + publish release", 132876243219) failed at 16:30:17Z with `stream error: stream ID 1; CANCEL; received from peer`. That was 11 s after `release-assets.sh ensure` printed "release v0.3.147: there already (draft)". No gh-retry warning was printed, so the failure was not an upload: it was a lookup or the notes' `release edit`. #1404 (62f8be748) had wrapped only the `gh release upload` calls in tools/release/gh-retry.sh. v0.3.147 stayed a draft; attempt 2 was started by hand.
- #1420 merged as 6aefc9d2b. Every gh call in release-assets.sh now goes through `gh_`, which runs gh-retry.sh: the lookups, `release edit`, `release list`, downloads (now `--clobber`) and uploads. The create runs as `create-once` under gh-retry.sh, and each try looks the release up first. release.yml's downloads and release list, web-release.yml's view and upload, and tag-guard.sh's release list go through gh-retry.sh as well.
- gh-retry.sh now buffers stdout and writes it only for the try that succeeds.

## What we learned
- On main, gh-retry.sh passed stdout straight through. A `gh api --paginate` cut after one page had already printed that page, so `$(...)` got the first page twice (once from the failed try, once from the retry). The new test shows it against main's script. A failed gh call does not always print nothing to stdout.
- A `[ "$(gh api ...)" = true ]` inside a function that is called from an `if` hides a failed read, because `set -e` is off there. is_draft therefore treated a failed read as "published". A read that decides a branch must fail explicitly (`d="$(...)" || exit 1`).
- Retrying `gh release create` blindly risks a second draft if the first create succeeded but its answer was lost. Drafts are not known to reserve a tag, and this was not verified against GitHub. Look the release up at the start of each try and treat a failed lookup as a failed try, never as "no release".

## Keep doing
- When adding a retry, wrap every network call in the script, and add a guard test that fails on a bare call (gh-retry-test.sh's `bare`).
