---
schema: 1
kind: pr
pr: 1294
title: "pnpm/action-setup and cosign-installer are pinned to commits, not tags"
date: 2026-11-14
session: 43ed6e31-0740-4d65-b657-61c1a1d3ba1b
agent: claude-code
---

## What happened
- #1294 merged as 1b874efbe. A security review on #1290 (58.11) flagged
  `pnpm/action-setup@v6.0.8` in the TV web release workflow, whose upload job holds
  `contents: write`: a moved tag would run inside it. All 22 uses in the workflows named the
  tag, so the 58.11 session left its line as it was and the Epic 58 lead pinned every one at
  once to `0e279bb9` (the commit behind the annotated tag, read through the GitHub API), with
  `# v6.0.8` kept as a trailing comment.
- The new `tools/ci/tests/test_action_pins.py` found `sigstore/cosign-installer` pinned by
  commit in some workflows and by its `v3.10.0` tag in others; all 7 now pin `d7543c93`.
  The test fails on a use of either action by tag, without its version comment, or with two
  different commits; proven to fail with the tag put back in `web-ui.yml`.

## What we learned
- A finding against one line of a pattern used everywhere is a reason to fix the pattern in
  one pull request, with a guard, not to leave the line because the others match it.
- An annotated tag's ref points at a tag object, not a commit: dereference it
  (`git/tags/<sha>` → `.object.sha`) before pinning, or the pin names an object `uses:`
  cannot check out.
- A guard written for one action is cheap to widen to the next one found split: the second
  action's mixed pins were invisible until a test read every `uses:` line.

## Keep doing
- Pin by commit with the tag in a comment, so readers and update tools still see the version.

## Open
- Other actions in the workflows still pin tags. Widen `PINNED` in the test as each one is
  pinned, starting with any that run in jobs holding write permissions.
