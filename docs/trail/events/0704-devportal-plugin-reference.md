---
schema: 1
kind: pr
pr: 704
prs: [683, 693, 703]
title: maktaba.dev's plugin reference, generated from the plugin system's own files
date: 2026-11-06
session: a9659d13-25bf-46c0-a91b-5c78cc44b65e
---

## What happened
- The Plugins guides (written 2026-11-03 from the spec) still said nothing was built. They now say what #683 and #693 put on main and what is coming in the next release (install, grants, health, settings, the Plugins page, the SDK and harness), and their API table matches the WIT.
- `/plugins/reference/` is generated at build time by `scripts/plugins.mjs` from the schema, `capabilities.toml`, `events.toml`, the WIT, the API's `Code*` and limit constants, the package limits, the failure causes and the app's EN/AR words. The build fails on any code, limit or WIT comment it does not cover. The schema is served at its `$id`.

## What we learned
- The spec-era guide was wrong in ways only the code shows: `log.write` (not `log`), no `data.list` or `library.list`, `suggest.add`, `media.read(handle, …)` (not `stream.read`), and `files.json` lists `manifest.toml` too. Check a guide written before its code against the WIT, not the spec.
- `media.info` is gated by `library.read` in `server.go`'s `capabilityOf`, not in `capabilities.toml`. A generator that reads only the TOML would say "none". The reference keeps that case with a check tied to the line in `server.go`.
- `rewriteHref` joins an absolute `/path` onto the source's folder: a generated page must link in-site pages by full URL, and guides link a generated page as a sibling file (`reference.md`) mapped in `published`.
- Adding `web/src/i18n/*.json` to the `devportal` filter changes which jobs a language-file change runs, and `test_ci_paths` pins that. It was left out on purpose.
- `ci_paths.py guard` only sees tracked files: run it after `git add`, or a new script passes locally and fails in lint (#703's first run).

## Open
- The SDK and harness guide is filled in by Epic 54 PR 4 (54.10–54.13), under CLAUDE.md rule 7.
