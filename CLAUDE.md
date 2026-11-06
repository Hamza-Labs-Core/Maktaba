# Maktaba: notes for Claude sessions

1. **Start from the trail: the `trail` skill** (`.claude/skills/trail/SKILL.md`). Read
   [`docs/trail/current.md`](docs/trail/current.md), the newest snapshot of what sessions have
   learned, then the events in `docs/trail/events/` merged since its `up_to_commit`.
   [`docs/claude-trail.md`](docs/claude-trail.md) is the frozen trail before 2026-11-05:
   read-only history, searched when a topic needs it.
2. **For each pull request you merge, write its event** (`docs/trail/events/NNNN-slug.md`, in
   that pull request, before it merges): what happened, what you learned, what to keep or stop
   doing, what is open. Something learned after the merge goes in a second event, in a
   trail-only pull request. Events are append-only; real PR numbers and shas only. The hook in
   `.claude/settings.json` reminds a session that merged a PR with no event.
3. **After a v\* tag, build its snapshot** (`trail.py skeleton --tag vX.Y.Z`), as the skill
   says, in a trail-only pull request. `python3 tools/trail/trail.py check` must pass.
4. Never write secrets in the trail or anywhere in the repo.
5. **Never quote the owner's words in repo text** (specs, docs, the trail, comments, tests): state
   each requirement or decision neutrally, with no attribution to the owner.
6. **A spec change ships its Arabic:** translate each changed epic, story or plan text in
   `docs/wiki/db/ar/` and regenerate (`python3 docs/wiki/db/generate_wiki.py`); `--check` in CI
   fails, naming the entry and field, on any missing or stale Arabic.
