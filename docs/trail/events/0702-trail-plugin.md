---
schema: 1
kind: pr
pr: 702
title: The trail's tools move to Hamza-Labs-Core/claude-trail (plugin, CLI, Action)
date: 2026-11-06
session: 6af8f425-40d3-4468-845d-6e9105123858
prs: [655, 674]
---

## What happened
`tools/trail/` (the checker and the hook), the `trail` skill and `test_trail.py` moved to a new
private repository, Hamza-Labs-Core/claude-trail, v0.1.0. It is three things in one repo: a
Python package (`trail` CLI, no runtime dependencies, Python 3.9+), a Claude Code plugin that
is its own marketplace (the skill, the hook, `/trail:event`, `/trail:snapshot`, `/trail:read`,
`trail` on PATH), and a composite GitHub Action. Maktaba keeps every event and the format;
its settings are `.trail.toml`, lint runs the Action, and `.claude/settings.json` enables
`trail@claude-trail` instead of wiring `tools/trail/hook.py`.

## What we learned
- Claude Code reserves plugin names starting with `claude-` (`claude plugin validate` errors),
  so the plugin is `trail` inside the `claude-trail` marketplace: `/plugin install
  trail@claude-trail`, and its commands are namespaced `/trail:event` and so on.
- A plugin's `bin/` folder is on the Bash tool's PATH while the plugin is enabled, so the
  skill can say `trail check` without an install step.
- The machines the hook and the Action run on have an older `python3` (macOS 3.9, Ubuntu
  22.04 runners 3.10), so `tomllib` is not there; a small built-in reader for the subset
  `.trail.toml` uses keeps the zero-install property. CI runs the Action on 22.04 for that.
- The old hook's SessionEnd log and fetch stamp were per machine, not per repository: with the
  hook installed as a plugin for every repo, a log written in one would be reported in
  another. Records now carry the repository (its common git dir) and the stamp is per repo.
- GitHub shows a workflow by its path instead of its `name:` when the file does not parse; a
  `run: printf -- '---…'` line (a plain scalar holding `: `) broke claude-trail's first CI run
  silently, with no run at all. actionlint catches it.

## Keep doing
- Keep CI's paths-ignore equal to `.trail.toml`'s `[trail_only]` paths (test_ci_paths.py checks
  it); tag automation asks `trail is-trail-only` so a trail-only change is never tagged. (#699)

## Open
- PyPI publishing waits for the trusted publisher to be added on PyPI (claude-trail's README,
  "PyPI"); until then the CLI runs from git with `uvx --from git+…@v0.1.0`.
