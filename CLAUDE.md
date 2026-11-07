# Maktaba: notes for Claude sessions

1. **Start from the trail: the `trail` plugin** ([agent-trail](https://github.com/Hamza-Labs-Core/agent-trail),
   enabled in `.claude/settings.json`; its skill is `trail`, its settings `.trail.toml`; other
   agents follow the same trail through `AGENTS.md`). Read
   [`docs/trail/current.md`](docs/trail/current.md), the newest snapshot of what sessions have
   learned, then the events in `docs/trail/events/` merged since its `up_to_commit`
   (`/trail:read` does both). [`docs/claude-trail.md`](docs/claude-trail.md) is the frozen
   trail before 2026-11-05: read-only history, searched when a topic needs it.
2. **For each pull request you merge, write its event** (`docs/trail/events/NNNN-slug.md`, in
   that pull request, before it merges; `/trail:event`): what happened, what you learned, what
   to keep or stop doing, what is open. Something learned after the merge goes in a second
   event, in a trail-only pull request. Events are append-only; real PR numbers and shas only.
   The plugin's hook reminds a session that merged a PR with no event.
3. **Tags are automatic**: `auto-tag.yml` tags each green commit of main as the next patch
   version and starts the release workflows (`docs/updates.md`, "How a release is made"); do
   not tag by hand unless a release needs it. A scheduled session builds each new tag's
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
