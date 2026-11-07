# Maktaba: notes for coding agents

These notes are for any coding agent working here: Codex, Cursor, Claude Code and others.
[`CLAUDE.md`](CLAUDE.md) holds the repository's rules, and they apply to every agent:
- never write secrets;
- never quote the owner's words in repo text;
- a spec change ships its Arabic.

The trail below is written by [agent-trail](https://github.com/Hamza-Labs-Core/agent-trail)
(`trail init --agent agents`). Edit outside its markers.

<!-- agent-trail:begin (written by `trail init --agent`; edit outside the markers) -->

## The agent trail

This repository keeps what coding-agent sessions learned in its trail, so the next session
starts from it (https://github.com/Hamza-Labs-Core/agent-trail). The settings are in `.trail.toml`.

**At the start of a session**, read `docs/trail/current.md` (the newest snapshot of the lessons), then
the events added since its `up_to_commit`:

```bash
git fetch -q origin main
git diff --name-only --diff-filter=A "$(sed -n 's/^up_to_commit: //p' docs/trail/current.md)" origin/main -- docs/trail/events
```

Before the first snapshot exists, `trail index` lists every event; read the ones that touch
your task.

`docs/claude-trail.md` is a frozen trail from before the events: search it with `grep -n` when a topic needs history; don't read it whole.

**For each pull request you merge, write its event** in that pull request, before it merges:
`docs/trail/events/NNNN-slug.md`, named for the PR's number (zero-padded). Write what only this
session knows, not what the diff shows:

```markdown
---
schema: 1
kind: pr                # pr | incident | decision | correction | release | migration
pr: 634
title: One line on what changed
date: 2026-11-04
session: <this session's id>
agent: <the agent: claude-code, codex, cursor, ...>
---

## What happened
The change, the root cause, the numbers (required).

## What we learned
The gotcha, the wrong first guess and why, what a tool or the code really does.

## Keep doing
## Stop doing
## Open
What is left, with its issue number.
```

Leave out a section with nothing in it. Something learned after the merge goes in a second
event (`NNNN-after-merge.md`) in a trail-only pull request; work with no pull request is named
`iNNNN-slug.md` (an issue) or `dYYYYMMDD-slug.md` (a day). Events are
append-only: a later fix is a `kind: correction` event with `corrects:`. Use real PR, issue and
sha numbers only; never write secrets; state decisions neutrally, without quoting anyone.

**Check before you push:** `trail check`. **Before you finish:** `trail remind` lists merged
pull requests with no event and tags with no snapshot.

**After a `vX.Y.Z` tag** (a scheduled release session pushes each tag, no workflow does:
`docs/updates.md`, "How a release is made"), a session builds its snapshot:
`trail skeleton --tag vX.Y.Z > docs/trail/snapshots/vX.Y.Z.md`. Fold each new
event the skeleton lists into Keep doing, Stop doing, What we learned and Open, citing it
(`#634`, `i649`, `d20261104-slug`; `issue #649` names an issue without
citing it); leave `Missing events` and `Index` as printed; copy it to `docs/trail/current.md`; run
`trail check`; merge it as a trail-only pull request.

`trail` is the agent-trail CLI: `uv tool install git+https://github.com/Hamza-Labs-Core/agent-trail` (or
`uvx --from git+https://github.com/Hamza-Labs-Core/agent-trail trail ...`).

<!-- agent-trail:end -->
