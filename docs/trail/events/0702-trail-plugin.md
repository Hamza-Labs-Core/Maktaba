---
schema: 1
kind: pr
pr: 702
title: The trail's tools move to Hamza-Labs-Core/agent-trail (CLI, agent integrations, Action)
date: 2026-11-06
session: 6af8f425-40d3-4468-845d-6e9105123858
agent: claude-code
prs: [655, 674, 699]
---

## What happened
`tools/trail/` (the checker and the hook), the `trail` skill and `test_trail.py` moved to their
own public repository, Hamza-Labs-Core/agent-trail (v0.2.0; v0.1.0 was "claude-trail"). It is
a Python package (`trail` CLI, no runtime dependencies, Python 3.9+), a Claude Code plugin
that is its own marketplace (the `trail` skill, the hook, `/trail:event`, `/trail:snapshot`,
`/trail:read`, `trail` on PATH), integrations for Codex and Cursor and any agent that reads
AGENTS.md (`trail init --agent ...`), and a composite GitHub Action. Maktaba keeps every event
and the format; its settings are `.trail.toml`, lint runs `Hamza-Labs-Core/agent-trail@v0`,
`.claude/settings.json` enables `trail@agent-trail`, and AGENTS.md carries the trail block for
other agents. Events may now name their agent (`agent:`, free text).

## What we learned
- A public repository's workflows cannot use an Action from a private repository, whatever
  the private repository's Actions access setting says; nor can its GITHUB_TOKEN clone one.
  The first version (private) failed lint with "Unable to resolve action ... not found"; the
  repository was made public.
- Claude Code reserves plugin names starting with `claude-` (`claude plugin validate`
  errors), so the plugin is `trail` in the `agent-trail` marketplace, and its commands are
  namespaced (`/trail:event`).
- A plugin's `bin/` folder is on the Bash tool's PATH while the plugin is enabled.
- Codex's hooks (`.codex/hooks.json`) take the same shape as Claude Code's, and Stop can block
  with a reason, but Codex documents its transcript format as unstable for hooks: its adapter
  records `gh pr merge N` from PostToolUse's `tool_input.command` instead, and its SessionEnd
  gets at most 3 seconds (no fetch). Cursor's `.cursor/hooks.json` uses `afterShellExecution`
  and answers `stop` with a `followup_message`.
- The machines the hook and the Action run on have an older `python3` (macOS 3.9, Ubuntu
  22.04 runners 3.10), without `tomllib`; a small built-in reader for `.trail.toml` keeps the
  zero-install property.
- The old hook's SessionEnd log and fetch stamp were per machine, not per repository; with the
  hook in a plugin for every repository, one repo's reminder would show in another. Records now
  name their repository.
- GitHub shows a workflow by its path instead of its `name:` when the file does not parse, and
  starts no run: a `run: printf -- '---…'` line broke agent-trail's first CI silently.

## Keep doing
- Keep CI's paths-ignore, `next_tag.py`'s TRAIL_ONLY and `.trail.toml`'s `[trail_only]`
  paths equal (test_ci_paths.py and test_next_tag.py check it). (#699)

## Open
- PyPI publishing waits for the trusted publisher on PyPI (agent-trail's README, "PyPI");
  until then the CLI runs from git.
