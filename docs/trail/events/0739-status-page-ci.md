---
schema: 1
kind: pr
pr: 739
title: What #739's CI taught after its event was written
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [739]
---

## What happened
- #739's first CI run failed on two checks that its event doesn't mention: the lint job's CI path guard and CodeQL. Both were fixed in the PR before it merged.

## What we learned
- A new top-level folder needs a line in `tools/ci/ci_paths.py`: a filter in `.github/ci-paths.yml` when ci.yml builds it, or a `NOT_IN_CI` entry naming the workflow that does (`status/**` → status-page.yml). The guard fails the lint job otherwise. Run `uv run --no-project --python 3.12 --with pyyaml==6.0.3 python tools/ci/ci_paths.py guard` before pushing.
- CodeQL reads test code too: a test that strips `<script>` with a regexp is flagged ("bad HTML filtering regexp", high), and a flagged line in a PR's own diff must be fixed in that PR. Cut known blocks by position instead.
- `tools/trail/trail.py` is gone: the trail's checks are the agent-trail CLI and Action. Run them locally with `uvx --from "git+https://github.com/Hamza-Labs-Core/agent-trail@v0" trail check`.

## Open
- CLAUDE.md still names `trail.py skeleton` and `trail.py check`; the agent-trail CLI's `trail` replaces them.
