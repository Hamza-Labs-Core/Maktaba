---
schema: 1
kind: pr
pr: 1786
title: Shared web font tokens and help mockup counts
date: 2026-11-26
session: 01a21956-bf2a-7aa2-b815-9dc4f89b27ee
agent: codex
---

## What happened
- [PR #1786](https://github.com/Hamza-Labs-Core/Maktaba/pull/1786) merged to main as `f80f2de399e03eaecfabdda1ee889498ee21c058`, closing [#1651](https://github.com/Hamza-Labs-Core/Maktaba/issues/1651) and [#1696](https://github.com/Hamza-Labs-Core/Maktaba/issues/1696).
- Mapped `--type-body-family` and `--type-display-family` to `--font-family` and `--font-heading` in `web/src/styles/tokens.css`, shared by web, cloud, admin, and TV.
- Fixed the mockup-only filter in `web/packages/website/design/shell.js`. The help index renders `41 topics` and `41 موضوعًا` in English and Arabic. Regenerated all 12 English and Arabic help index and topic PNGs.

## What we learned
- Strip CSS comments before asserting font token declarations so a commented-out alias cannot satisfy the regression test. Check browser `pageerror` in `render.mjs` so a script failure cannot silently produce a stale count.
