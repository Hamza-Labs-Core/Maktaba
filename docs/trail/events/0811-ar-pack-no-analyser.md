---
schema: 1
kind: pr
pr: 811
title: The Arabic pack's "no analyser" test hides CAMeL Tools itself
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- `test_without_the_analyser_nothing_is_unknown` passed `analyse=None`, and `ArabicPack` then falls back to CAMeL Tools when it is installed. It failed on every machine with camel_tools and passed on CI only because CI lacks it. The test now sets `morphology.available` to False.

## What we learned
- A test of "without an optional dependency" must hide that dependency itself. Running on a machine that happens to lack it proves nothing.
- `uv sync --all-extras` locally finds such tests; CI's narrower install does not.

## Open
- Nothing.
