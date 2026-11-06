---
schema: 1
kind: decision
title: The PC's coordinating session and its review, merge and tag rules
date: 2026-10-20
session: f43fad60
source: claude-trail.md
---

## What happened
Session f43fad60 ("Maktaba worktree setup") ran from 2026-10-20 to 10-31. It kept the PC's node
and dizqueTV running, ran the PC side sessions (node copy, Windows and Linux apps, updates),
reviewed, fixed and merged about 70 PRs, and tagged about 17 releases between v0.3.0-rc.1 and
v0.3.33 (the Mac tagged the rest). On 10-31 it traced the PC sessions into the old trail.

## What we learned
- Compare with main's own runs before blaming a change: #254's apt changes were reverted in
  #259 when the slow step turned out to be the x86_64 runners' mirror.
- A v0.3.1 tag collided with an existing remote tag.
- The dizqueTV export carried the Plex token in about 2,800 files; it was redacted.

## Keep doing
- Review every PR from this side, then fix and merge it: a merge commit on the exact reviewed
  head (`--match-head-commit` with the full sha), every check green.
- Fix and merge instead of opening new PRs: fold findings and new CI failures into PRs already
  open.
- Poll CI yourself and merge on green.
- Tag every merge: the next patch tag at once, after main's `ci.yml` passes on the merge
  commit, pushed with the owner's `gh` so the release builds start (since 10-31, not while a
  release is held).
- A recurring manual step becomes a Maktaba feature.
- Check remote tags before tagging.
- Scan any export for tokens before sharing it.
