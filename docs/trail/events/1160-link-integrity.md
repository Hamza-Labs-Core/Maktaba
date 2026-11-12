---
schema: 1
kind: pr
pr: 1160
title: "feat(ci): maktaba.tv link check, public-image check, Reference images rule (60.08, 60.04)"
date: 2026-11-11
session: 577ef81e-0b1e-444e-84c0-40d580e54848
---

## What happened
- Epic 60 story 60.08 and 60.04's `Reference images:` rule landed in #1160 (merge 7fc2aab34).
- `web/packages/website/scripts/links.mjs` checks every maktaba.tv link in the tracked files
  (problem links against `shared/api/problems.json`, the rest against `dist-manifest.json`) as
  `lint / maktaba.tv links` on every pull request. 271 (file, link) pairs were dead, because the
  site does not exist yet; they sit in `links-baseline.json` with the story that fixes each
  (169 for 60.06, 90 for 60.05, 12 for 60.03). The check fails on a new dead link and on a
  baseline entry now live or gone, so the baseline only shrinks.
- `tools/release/image-public.sh` pulls the server image with no login: a leaf job in
  `release.yml` after the image is pushed and signed, and daily in `image-public.yml`. The
  image is private (owner action A1), so both are red until it is made public.
- `postgres-cdn.yml` ran only after a successful release run; a red public-image job would have
  stopped it on every release. It now also runs when that job alone failed.
- The first CI run failed twice, both on files that were untracked when checked locally: the
  link check flagged its own header comments (it reads `git ls-files`), and the ci-paths guard
  had no entry for the two new workflows and `reference_images.py`. Fixed in 8ce4a6531.
- A test's fake API matched `"page=1"` inside `per_page=100`, so the paginated loop never ended
  and pytest hung; caught locally before the push.

## What we learned
- A check that reads `git ls-files` sees nothing new until it is staged: run such checks
  (links.mjs, `ci_paths.py guard`) after `git add`.
- A new job that fails the release run on purpose also stops every workflow keyed to the run's
  conclusion (`workflow_run` with `conclusion == 'success'`); grep for them before adding one.
- The link check caught a placeholder link in the PR's own developer guide: write maktaba.tv
  in prose without a path.

## Keep doing
- Leaf jobs for checks that must be visible but hold nothing back, with the workflows downstream
  of the run's conclusion adjusted in the same pull request.

## Stop doing
- Running repository-wide checks on unstaged files and taking their pass as proof.

## Open
- Owner action A1: make the `maktaba-server` package public; the daily `Image public` run and
  each release's `image-public` job stay red until then.
- T07 (the manifest equals the site build): the site package's build should call
  `links.mjs --write-manifest dist`, and its CI `links.mjs --dist dist`.
- The baseline must be empty by the end of Epic 60's Phase 1; each page PR runs
  `links.mjs --prune-baseline`.
