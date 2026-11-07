---
schema: 1
kind: incident
title: A PR body's "resolved" closed another open pull request
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
prs: [795, 846]
---

## What happened
- The body of PR 846 (a fix of tools/dev/merge-main.sh) explained how the tool had been used: the words "resolved", then the number sign and 795, then "'s merge". "resolved #N" is one of GitHub's closing keywords. When PR 846 merged at 16:32:09, GitHub closed PR 795 at 16:32:11, as the account that merged.
- PR 795 (the Updates page names the plugins an update will pause) was open, green and waiting on its merge watcher. Its watcher could no longer merge it, and nothing said why. The close showed as a plain `closed` event by the merging account, with no comment and no app, so it looked like a person's.
- Sessions looked for a session that had run a close command and found none. The cause was found in the merging PR's body, and PR 795 was reopened.

## What we learned
- GitHub's closing keywords (close, closes, closed, fix, fixes, fixed, resolve, resolves, resolved, before a reference to an issue or pull request in this repository) act on pull requests too, not only issues. They act when the PR is merged, from its body, and from commit messages that reach the default branch.
- A close made this way is attributed to whoever merged, with no trace of the PR that caused it beyond the timing: look for a merge a few seconds earlier, and read its body.

## Keep doing
- In PR bodies and commit messages, write close/closes/closed, fix/fixes/fixed or resolve/resolves/resolved directly before a `#N` only to close that issue on purpose. To mention another PR, write "the merge for PR 795" or "(#795)".

## Open
- A CI check on the PR body: it fails when a closing keyword names a number that is an open pull request rather than an issue (tools/ci, in the PR that adds it).
