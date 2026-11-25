---
schema: 1
kind: pr
pr: 1669
title: "A changelog entry is a pair of files in changelog.d/"
date: 2026-11-24
session: c9608b0a-6764-4bd7-84eb-1bfe023a95c3
agent: claude-code
---

## What happened
#1669: every user-visible pull request added lines at the top of both CHANGELOGs, so each merge conflicted every other open pull request there and restarted its CI. An entry is now `changelog.d/YYYY-MM-DD-slug.md` and its Arabic `.ar.md`. The lint job checks the pairs and refuses new lines under `[Unreleased]`, frozen as they were; a branch whose oldest commit predates the fragments may still add lines, with a warning. `tools/dev/changelog_fragment.py convert` moves a branch's lines to a pair, and `tools/dev/merge-main.sh` does it when they conflict. Run on the 16 open pull requests with CHANGELOG lines, 15 converted; #1574 stopped on its own other conflicts.

## What we learned
- **A fragment is never folded into a release section.** Releases are tags with no commit of their own, so folding would take a commit on main per release in the file everything conflicts on. maktaba.dev reads the first `vX.Y.Z` tag whose tree has the fragment; the tags' commits and trees are a 2 MB fetch (`--depth=1 --filter=blob:none`).
- **No gate ever required a changelog entry,** though the CHANGELOG's intro said so, and only maktaba.dev reads the file: release notes, the NAS app's changelog link and the stores' notes do not.
- **Merging main into a pull request reruns all its jobs whatever main changed:** the filters compare the whole pull request with its base and nothing reuses earlier results. Avoiding the conflict is what saves the run.
- **A pull request's checkout is the merge commit, whose first parent is main.** A check of the branch's own commits reads `github.event.pull_request.head.sha`.
- **Pairing an edited entry with the next added one mistook a new entry above it for the edit** (`changelog_merge.py`); it now takes the most alike, and an entry's indented second paragraph is part of it.
- **Stacked branches carry each other's entries,** and both may move them before either merges. A check of the whole tree for an entry in two fragments turned main and every other pull request red after the second merge. The page now shows an entry (its section, English and Arabic) once, so such a pair needs nothing done: the gate warns, and fails only on the same entry in two fragments the branch itself adds. `convert` skips an entry that a fragment in its tree already has, and nothing removes or edits a fragment.
- **Dropping the duplicates automatically was worse than keeping them.** A first version removed from a branch's fragments what main's had: a fragment the branch had renamed was its only copy and was deleted, with the gate green; and `merge-main.sh`, committing that, committed what a person had staged. Making the duplicate harmless where it is read took less code.
- **A gate that reads the checked-out tree reads main's state too,** because a pull request's checkout is its merge. What a pull request is blamed for has to come from its own head and its merge-base.
- **An old branch's first `merge-main.sh` is its own older script:** it keeps both sides in the CHANGELOGs, and `convert` moves the entries after.

## Keep doing
- Rehearse a tool for open pull requests on the real ones (`refs/pull/N/head` in a scratch worktree) before shipping it.

## Open
- Entries that older branches add to the frozen `[Unreleased]` show under "Earlier entries" on the page, not in their release.
