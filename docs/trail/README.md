# The Claude trail

What Claude sessions learned while working on Maktaba, kept so the next session starts from
it. It is not a changelog (CHANGELOG.md is) and not a list of pull requests (git is). It holds
what only the session saw: the approach that failed, the root cause found after the wrong
guesses, the rule that was corrected mid-task, the gotcha that cost an hour.

- **`events/`**: one file per piece of work, written by the session that did it.
- **`snapshots/vX.Y.Z.md`**: the lessons so far, built at each tag from the last snapshot plus
  the events since. **`current.md`** is a copy of the newest one: read it first.
- **`../claude-trail.md`**: the trail before 2026-11-05, frozen. Read-only history; its entries
  are also migrated into `events/` (`source: claude-trail.md`).

How to write each part: the `trail` skill (`.claude/skills/trail/SKILL.md`). The checker:
`python3 tools/trail/trail.py check` (the lint job runs it on every pull request).

## Events

One file per pull request, named for it and zero-padded so `ls` sorts:
`events/0634-youtube-sources-from-links.md`. Work with no pull request is named for its issue
(`events/i0649-player-shards-left-running.md`) or its day (`events/d20261104-live-db-sweep.md`).

```markdown
---
schema: 1
kind: pr                # pr | incident | decision | correction | release | migration
pr: 634                 # with an NNNN- name; issue: N with an iNNNN- name
title: YouTube sources from linked videos
date: 2026-11-04        # the day the work happened (or merged)
session: local_7c0a4c88-…   # the session that learned it; unknown for a migrated entry
prs: [628]              # optional: other pull requests this event is also about
corrects: 0608-epic-51  # kind correction only: the event it corrects
---

## What happened
Required. What was built or found, briefly: the change, the root cause, the numbers.

## What we learned
The things a later session should know: gotchas, causes, what the code or the tools really do.

## Keep doing
## Stop doing
## Open
What is left, with its issue number.
```

- Only `What happened` is required. An empty section is left out, never padded: do not
  invent a lesson to fill one.
- Real pull request numbers, issue numbers and shas only (`gh`, `git`), never guessed ones.
- **Append-only.** An event is not edited after it merges; a later fact or a fix goes in a new
  event (`kind: correction`, `corrects:`), or a second event for the same pull request
  (`0634-after-release.md`).
- Never write secrets (tokens, passwords, keys, the Plex token, DB URLs with passwords). The
  checker refuses the common shapes.
- Never quote the owner's words; state the requirement or decision neutrally (CLAUDE.md).

## Snapshots

```markdown
---
schema: 1
tag: v0.3.82
up_to_commit: 1a2b3c4d5     # the tag's commit
built_from: v0.3.81         # the previous snapshot, or full for a rebuild from every event
---

# Trail snapshot: v0.3.82

## Keep doing
- Branch stacks point HOME, MAKTABA_CLUSTER_DIR and MAKTABA_ARCHIVE_DIR at scratch folders. (#621)
## Stop doing
## What we learned
## Open
## Missing events
## Index
```

- Every item cites at least one event: `#634` (a pull request's events), `i649` (an issue's),
  or a `dYYYYMMDD-slug` name. The checker refuses an item with no citation or a citation that
  matches no event at `up_to_commit`.
- An issue is named `issue #649` (the word `issue`, a space, then `#N`): that form cites nothing
  and needs no event, so an item can point at its issue next to its citation
  (`- The cache key is still per path (issue #649). (#634)`). Any other `#N` is a pull request.
- `snapshots/` holds only `vX.Y.Z.md` files; the checker refuses anything else there.
- `Missing events` and `Index` are computed, never written by hand
  (`trail.py skeleton --tag vX.Y.Z` prints them). Missing lists each pull request merged after
  #653 that has no event, so a session that forgot to write one shows up.
- Each snapshot carries the last one forward, so a lesson keeps its citation and is dropped
  only on purpose. A rebuild from every event (`built_from: full`) at each minor version keeps
  the summary from drifting.
