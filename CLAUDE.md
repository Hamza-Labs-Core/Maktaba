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
   owner's machine) tags the newest commit of main whose CI and update rehearsal both passed, and
   whose broadcast code's last broadcast rehearsal nightly passed, as the next patch version
   (`tools/release/next_tag.py --green FILE`, which reads the nightly runs itself with `gh`),
   and its tag push starts the release workflows (`docs/updates.md`, "How a release is made"); no workflow pushes tags. Do
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
8. **A changelog entry is a pair of files of its own** under `changelog.d/`, in English and
   Arabic. Follow [the fragment format](changelog.d/README.md). CI validates the pairs and
   protects the frozen release sections of both CHANGELOGs. The portal reads fragments and tags
   directly for each release. **Bring main in with `tools/dev/merge-main.sh`.** The first run on
   a branch that predates `tools/dev/changelog_fragment.py` uses its older merge script, which
   keeps both sides. Run `python3 tools/dev/changelog_fragment.py convert` afterward or when a
   clean merge leaves entries in the CHANGELOGs. The current script moves a branch's entries to
   a fragment pair where it can and merges the other CHANGELOG entries individually. It leaves
   the merge in progress on other conflicts or changes it cannot place. Read the surrounding
   entries before resolving a CHANGELOG conflict by hand. Keep `merge=union` off these files.
   Bring main in when the branch conflicts or needs its changes, since a push restarts CI.
9. **Decisions for the owner go through the decisions inbox, never chat** (every agent, every
   provider): file a request with `~/.maktaba/decisions/bin/ask request.json` (title, context,
   2+ options with exactly one recommended, optional images, `from`), write
   `DECISION NEEDED: <id> — <title>` in your report, keep working on what doesn't depend on it,
   and read the answer with `~/.maktaba/decisions/bin/answer <id>`. The owner answers every
   decision on one page, synced every 15 minutes. Don't ask decisions in chat, in pull requests
   or on pages of your own. Images: dark theme, English and Arabic pairs, demo media with no
   women or girls. See [`docs/decisions.md`](docs/decisions.md) and `~/.maktaba/decisions/README.md`.
