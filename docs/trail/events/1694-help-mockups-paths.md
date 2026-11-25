---
schema: 1
kind: pr
pr: 1694
title: "Help topic and title changes run the help mockups' check"
date: 2026-11-24
session: 76b80a46-8389-4697-ae6f-d431f462e736
agent: claude-code
---

## What happened
- `web/src/help/topics.ts` and `web/src/i18n/{en,ar}.json` are in `_tokens` of
  `.github/ci-paths.yml`, so the unit job's token step, which holds the mockups' check, runs for
  them. `tools/ci/tests/test_ci_paths.py` expects it.

## What we learned
- A check of generated files has to run for what they are generated from, not only for the files.

## Keep doing
- Adding the path filter with the fix of what it let through.

## Stop doing
- Nothing new.

## Open
- Nothing.
