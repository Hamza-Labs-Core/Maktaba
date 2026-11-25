---
schema: 1
kind: pr
pr: 1694
title: "What the help mockups are drawn from runs their check"
date: 2026-11-24
session: 76b80a46-8389-4697-ae6f-d431f462e736
agent: claude-code
---

## What happened
- `_tokens` of `.github/ci-paths.yml` has the files `make.mjs` reads outside the design folder: the
  help topics, the Search topic's text, the two language files and the icon. The unit job's token
  step, which holds the mockups' check, runs for them. `tools/ci/tests/test_ci_paths.py` has a
  case for each.
- The first version had only the topics and the languages. An independent review traced every file
  the check reads and found the other three.

## What we learned
- A check of generated files has to run for everything they are generated from. Tracing what the
  generator reads finds it; guessing from what broke last does not.

## Keep doing
- Adding the path filter with the fix of what it let through.
