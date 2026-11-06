---
schema: 1
kind: pr
pr: 698
title: The review page's key tests flush the listener's effect before pressing
date: 2026-11-06
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
prs: [672]
---

## What happened
- Two keyboard tests of Epic 52's review page failed three times on loaded runners (issue #697) and passed on re-run. The tests now flush effects before each key press and wait 3 s.

## What we learned
- A window `keydown` listener that closes over state is re-attached by a `useEffect` after render, so a test that finds the element and then dispatches a key can hit the previous listener. `await act(async () => {})` before dispatching closes that gap.
- Not reproduced locally even with 8 runs at once: CI's 2–3× slowdown under load is hard to copy on a fast Mac, so a flake fix there is a reading of the code until CI stops failing.

## Stop doing
- Ending a shell command with a cleanup `git checkout <ref> -- <file>` in a worktree with uncommitted edits: it threw away the edit twice in this session.
