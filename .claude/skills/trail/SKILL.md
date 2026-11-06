---
name: trail
description: Maktaba's Claude trail. Use at the start of every session in this repo (read the current snapshot), whenever a pull request is about to merge or has merged (write its event), when the trail hook says a merged PR has no event, before a session ends or compacts, and after a v* tag (build the snapshot). Covers docs/trail/events, docs/trail/snapshots and tools/trail/trail.py.
---

# The Claude trail

The trail keeps what sessions learned, so the next one starts from it. The format is in
`docs/trail/README.md`; this is when and how.

## 1. At session start: read

```bash
cat docs/trail/current.md
```

Then the events merged since its `up_to_commit` (the snapshot does not have them yet):

```bash
git fetch -q origin main
git diff --name-only --diff-filter=A "$(sed -n 's/^up_to_commit: //p' docs/trail/current.md)" origin/main -- docs/trail/events
```

Read those files. (Before the first snapshot exists, `trail.py index` lists every event: read
the ones that touch your task.) `docs/claude-trail.md` is the frozen old trail: search it when a topic needs
history (`grep -n`), don't read it whole.

## 2. For each pull request you merge: write its event

Write the event **in the pull request itself**, before it merges, while you remember. Events
are one file each, so they never conflict. Name it `docs/trail/events/NNNN-slug.md` with the
PR's number (open the PR first to get it).

Write what only this session knows, not what the diff already says:
- **What happened**: the change in two or three sentences, the root cause, the numbers.
- **What we learned**: the gotcha, the wrong first guess and why it was wrong, what a tool or
  the code really does, a rule that was corrected.
- **Keep doing / Stop doing**: only if the work showed one. Never pad: leave a section out.
- **Open**: what is left, with its issue number (file the issue first).

Front matter: `schema: 1`, `kind: pr`, `pr: N`, `title`, `date`, `session` (your session id; it
is the name of your transcript file, or `local_…` in the desktop app), plus `prs: [...]` or
`issue: N` if they apply.

Something learned **after** the merge (CI on main, the release, the live update) goes in a
second event for the same PR, `NNNN-after-merge.md`, in a trail-only pull request (only
`docs/trail/**`: CI runs just the repo checks, merge it when green). Work with no PR: name the
event for its issue (`iNNNN-slug.md`) or day (`dYYYYMMDD-slug.md`) with the right `kind`.

Check before you push:

```bash
python3 tools/trail/trail.py check
```

Rules: real PR, issue and sha numbers only; no secrets; never quote the owner, state it
neutrally; an event is never edited after it merges (write a `kind: correction` event with
`corrects:` instead).

## 3. When the hook speaks

`.claude/settings.json` runs `tools/trail/hook.py` at Stop, PreCompact, SessionStart and
SessionEnd. It lists pull requests this session merged (`gh pr merge N` in the transcript,
merged on origin/main) that have no event on any branch or worktree. When it blocks a Stop or
reminds you after a compact, write those events now. At startup it may list PRs earlier
sessions left without one: write them if you have the context.

## 4. After a v* tag: build the snapshot

The session that tags (or the next one) builds `docs/trail/snapshots/vX.Y.Z.md`:

```bash
python3 tools/trail/trail.py skeleton --tag vX.Y.Z > docs/trail/snapshots/vX.Y.Z.md
```

The skeleton carries the last snapshot older than the tag forward (its sections) and lists the new events in a
comment. Fold each new event in: add its lessons to the right section with its citation
(`#634`, `i649`, `d20261104-slug`; an issue that is only mentioned is written `issue #649`,
which cites nothing), merge items that say the same thing (keep all their
citations), drop an item only when a newer event shows it is no longer true. Delete the comment,
leave `Missing events` and `Index` exactly as printed, copy it to `docs/trail/current.md`,
run `trail.py check`, and merge it as a trail-only pull request.

At a minor version (v0.4.0, v0.5.0 …) rebuild from every event instead (`built_from: full`):
read all events and write the sections fresh, so the summary does not drift.
If a tag was skipped, the next snapshot covers every event since the last one.
