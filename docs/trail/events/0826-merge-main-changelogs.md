---
schema: 1
kind: pr
pr: 826
title: tools/dev/merge-main.sh keeps both sides of the CHANGELOGs
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
---

## What happened
- `tools/dev/merge-main.sh` merges origin/main and merges a conflicted `CHANGELOG.md` or `CHANGELOG.ar.md` entry by entry from git's three stages (`tools/dev/changelog_merge.py`); any other conflict, or the same entry changed on both sides, stops it with the merge in progress. CLAUDE.md rule 8 and CONTRIBUTING.md point delegates at it; the lint job's `repo` step runs its test.

## What we learned
- A line-wise resolution of these files is not safe. Two new entries that share a line (a blank one, a `maktaba.dev:` tail) are split around it: git leaves the shared line outside the conflict, so resolving the hunk interleaves the entries and drops one's tail. `merge=union` in `.gitattributes` does the same with no conflict at all (tested: entry B lost its last line).
- A per-hunk resolver that happens to work (each conflict ending at a blank line) hides the bug until two entries end alike; the test proves the tool against the shared-line case.
- macOS's `/bin/bash` is 3.2: no `mapfile`, and an empty array under `set -u` needs `${a[@]+"${a[@]}"}`. Inside `${var/pattern/repl}` within double quotes, bash 3.2 reads an apostrophe as a quote.
- In this repository's agent shells (zsh), `set -e` across a long command line has let a failed step's later steps run; a sequence that pushes runs as a bash script file.

## Keep doing
- Run `tools/dev/merge-main.sh` to bring main in; resolve what it stops on by hand, then run it again.
