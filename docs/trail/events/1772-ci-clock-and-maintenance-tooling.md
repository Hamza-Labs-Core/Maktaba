---
schema: 1
kind: pr
pr: 1772
title: CI clock fixtures and maintenance tooling follow-ups
date: 2026-11-26
session: 01a2189e-a6eb-7a72-bbcd-d86940c29fbc
agent: codex
---

## What happened
- [PR #1772](https://github.com/Hamza-Labs-Core/Maktaba/pull/1772) merged as `856bf3f6dc9e20a7b1f97171c684544ddb11feab`, closing [#1227](https://github.com/Hamza-Labs-Core/Maktaba/issues/1227), [#1481](https://github.com/Hamza-Labs-Core/Maktaba/issues/1481), [#1658](https://github.com/Hamza-Labs-Core/Maktaba/issues/1658), [#1475](https://github.com/Hamza-Labs-Core/Maktaba/issues/1475), [#1461](https://github.com/Hamza-Labs-Core/Maktaba/issues/1461), and [#1680](https://github.com/Hamza-Labs-Core/Maktaba/issues/1680).
- The batch stabilized clock-sensitive tests, passed the target branch ref through branch-head CI, and updated changelog fragment validation, portal changelog generation, and epic-status parsing.

## What we learned
- The parental live-time test computes the usage row's day key and the verdict from the same store clock and home time zone. A fixed instant keeps the fixture on one home day and allows exact assertions on the remaining allowance.
- The epic-status parser now accepts indented story tables and rejects epics with no parsed story rows before rewriting the summary.
