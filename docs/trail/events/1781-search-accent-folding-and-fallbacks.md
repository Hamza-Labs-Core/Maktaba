---
schema: 1
kind: pr
pr: 1781
title: Latin accent folding and search passage fallbacks
date: 2026-11-26
session: 01a218c5-8e0d-71e0-bebb-8bcbb2172778
agent: codex
---

## What happened
- [PR #1781](https://github.com/Hamza-Labs-Core/Maktaba/pull/1781) merged to main as `fc5b3ddec84983f48a40dc4515b7dc232aa6e815`. The work was part of [#1492](https://github.com/Hamza-Labs-Core/Maktaba/issues/1492).
- Search passage matching and highlighting handle Latin folds such as Straße to strasse and œuvre to oeuvre while preserving the visible text.

## What we learned
- The normalisedLines budget fallback is exercised at query, scan, and partial iteration boundaries. The tests assert passage IDs, counts, and marks, including the combination of a PostgreSQL hit already read and the fallback hits.
