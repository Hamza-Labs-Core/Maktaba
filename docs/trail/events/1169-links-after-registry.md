---
schema: 1
kind: pr
pr: 1169
title: "The maktaba.tv link check after the registry and the site"
date: 2026-11-11
session: 8377b6f8-6210-4839-9195-25422fd07a3d
agent: claude-code
---

## What happened
- Three pull requests merged within the hour and turned main's `lint / maktaba.tv links` red: #1160 (the link check), #1162 (the problem registry) and #1161 (the site). Each was green on its own base.
  - #1162's literal types left their files, and its registry made the baselined `/problems/<slug>` links live. Both are baseline entries the check refuses to keep.
  - #1161's site pages were missing from the empty `dist-manifest.json`.
  - The registry's bare base `https://maktaba.tv/problems/` counted as a dead link.
- #1169 merged as b6a687285838ac90d6638a6252b5750f33c05108 and changed the check's data only:
  - `--prune-baseline` dropped 266 entries;
  - `--write-manifest` was run from the site build;
  - the base prefixes were allow-listed with a reason;
  - the guide's `/problems/` index links were baselined under 60.06.
- The first push failed `lint / lint`: `--write-manifest` output is not Prettier-formatted, so `prettier --write` was run on it.

## What we learned
- The link check reads every tracked file. A pull request that moves or adds maktaba.tv strings (problem types, site pages) must run `links.mjs` before merging, even when its own CI ran without it, because the job may have landed after the branch was cut.
- `links.mjs --write-manifest` writes JSON that Prettier rejects. Run `prettier --write` on `dist-manifest.json` after it.

## Keep doing
- When main is red right after concurrent merges, fix it in one small data-only pull request, and check that no open pull request already touches the same files.

## Stop doing
- Merging a branch cut before a new repo-wide check without merging main and re-running it first.

## Open
- The baseline holds 9 entries: 60.06's `/problems/` index and its pages, and 60.05's install guides.
- The site build could run `--write-manifest` and Prettier itself, so the manifest cannot fall behind.
