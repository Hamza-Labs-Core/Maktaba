# Plan 33.16 — Docs and runbook — implementation

> Implementation plan for [story-33-16](story-33-16-docs-runbook.md). No migration.

## 1. Files

**Create**
- `docs/tv/staging.md` — concepts, operation, settings reference
- `docs/tv/staging-playbook.md` — failure playbook
- `docs/ar/tv/staging.md` — Arabic summary

**Modify**
- `docs/tv/README.md` (or the TV docs index) — links
- Epic 32 runbook (32.12) — push relay location, simulcast continuity note

## 2. Steps

1. Concepts and operation with Phase B.
2. Playbook entries as each failure path is implemented and tested.
3. Upgrade section with Phase D.
4. Settings reference generated from `rules/schema.json` (script) and checked in CI.

## 3. Tests

Link check; settings-reference check.
