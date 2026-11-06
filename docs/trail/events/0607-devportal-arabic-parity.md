---
schema: 1
kind: pr
pr: 607
title: maktaba.dev: every page in English and Arabic, with parity checks
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge bc5c02a9d. Every page also exists in Arabic under `/ar/`, right to left, built from Arabic twin files: `name.ar.md` guides, `CHANGELOG.ar.md`, and an OpenAPI translation file (`shared/api/openapi.ar.json`) keyed by JSON pointer.
- The build fails when the two languages drift, including when an Arabic page is mostly English (over 20 % Latin prose).
- The plugin publishing guide follows Epic 54's store design.
