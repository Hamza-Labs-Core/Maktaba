# Maktaba: notes for Claude sessions

1. **Start from the trail: the `trail` plugin** ([agent-trail](https://github.com/Hamza-Labs-Core/agent-trail),
   enabled in `.claude/settings.json`; its skill is `trail`, its settings `.trail.toml`; other
   agents follow the same trail through `AGENTS.md`). Read
   [`docs/trail/current.md`](docs/trail/current.md), the newest snapshot of what sessions have
   learned, then the events in `docs/trail/events/` merged since its `up_to_commit`
   (`/trail:read` does both). [`docs/claude-trail.md`](docs/claude-trail.md) is the frozen
   trail before 2026-11-05: read-only history, searched when a topic needs it.
2. **For each pull request you merge, write its event** (`docs/trail/events/NNNN-slug.md`,
   `/trail:event`): what happened, what you learned, what to keep or stop doing, what is open.
   The event goes in its **own trail-only pull request**, never in the code pull request: a code
   pull request carries no `docs/trail/` change (check `gh pr diff N --name-only` before
   merging). Trail-only pull requests run no CI and no tests; one may batch several events.
   Open it alongside the code pull request and merge it after. Something learned later goes in
   a second event, also trail-only. Events are append-only; real PR numbers and shas only.
   The plugin's hook reminds a session that merged a PR with no event.
3. **A session pushes each tag**: a scheduled release session (every 30 minutes, on the
   owner's machine) tags the newest commit of main whose CI and update rehearsal both passed as
   the next patch version (`tools/release/next_tag.py --green FILE`), and its tag push starts the
   release workflows (`docs/updates.md`, "How a release is made"); no workflow pushes tags. Do
   not tag by hand unless a release needs it. The same session then builds each new tag's
   snapshot (`/trail:snapshot`, or `trail skeleton --tag vX.Y.Z`), as the skill says, in a
   trail-only pull request; a session that finds a tag without one builds it. `trail check`
   must pass (the lint job runs it through the agent-trail Action). Without the plugin, run
   it as `uvx --from git+https://github.com/Hamza-Labs-Core/agent-trail@v0.2.0 trail check`.
4. Never write secrets in the trail or anywhere in the repo.
5. **Never quote the owner's words in repo text** (specs, docs, the trail, comments, tests): state
   each requirement or decision neutrally, with no attribution to the owner.
6. **A spec change ships its Arabic:** translate each changed epic, story or plan text in
   `docs/wiki/db/ar/` and regenerate (`python3 docs/wiki/db/generate_wiki.py`); `--check` in CI
   fails, naming the entry and field, on any missing or stale Arabic.
7. **maktaba.dev follows what ships:** a pull request that ships or changes a user-facing
   feature, an app screen, a developer-facing API or the plugin system updates maktaba.dev
   (`web/packages/devportal`, and the guides it publishes from `docs/developers/`) in the same
   pull request, in English and Arabic: the Apps pages (`scripts/apps.mjs`, with screenshots on
   test data), the Plugins section, or a guide. The `affected-tests` job warns when such a pull
   request leaves the portal alone (`tools/ci/devportal_follows.py`); a `No-Portal-Change:
   <reason>` trailer says it needs nothing.
8. **Bring main in with `tools/dev/merge-main.sh`:** it merges origin/main and keeps both sides
   of `CHANGELOG.md` and `CHANGELOG.ar.md` entry by entry (never line by line: two entries that
   share a line would interleave), and stops, the merge left in progress, on any other conflict
   or a CHANGELOG change it cannot place. Never resolve a CHANGELOG hunk by hand without reading
   the lines around it, and never use `merge=union` on them.
