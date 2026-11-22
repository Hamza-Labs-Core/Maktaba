---
schema: 1
kind: pr
pr: 1452
title: "Story 54.46: remove a Maktaba plugin, and install it again (partial)"
date: 2026-11-19
session: 9aa2417d-57dc-4c99-a4b6-462f5098d816
agent: claude-code
---

## What happened

Story 54.46 shipped partially in #1452 (merge 980587913). A Maktaba plugin's state is its component's desired row: `absent`, `installed` or `enabled`.

- `DELETE /api/plugins/{id}` on a Maktaba plugin sets `absent`. Its queued jobs park behind the component gate, and its data is kept: `?purge=true` is refused with `plugin_data_kept`.
- `POST /api/plugins/{id}/install` takes it back from the release: installed, or on with `enable`. Only ids of this release's bundle with a switchable component are accepted.
- The Plugins page shows a removed Maktaba plugin as not installed, with an install button. The strings ship in English and Arabic, and the developer guide (`installing.md`) says how it works.
- Update rehearsal scenario 15 checks that a removed `tv.maktaba.dedup` stays removed through an update, a rollback and the next update.

Installing a store or file package waits for 54.49 and Maktaba's key.

GLM-5.3's review, with a tool-call budget after a first run hit its 100-turn limit, found no blockers or majors. Its three minors are filed in #1463: two spec test bullets not observed end to end, the asset-phase wording, and `?purge=true` hiding `component_required_by`.

Retargeted from #1450's branch to main, the full CI found three gaps the stacked base had not run:

- the new route missing from `shared/api/openapi.json`, `openapi.yaml` and `openapi.ar.json` (`TestOpenAPIDescribesEveryRoute`);
- the website's `dist-manifest.json` not listing the new problem page;
- the wiki database not listing the new endpoint.

A unit-job time budget breach in `api/internal/system`, a test this PR does not touch, passed on re-run.

## What we learned

- A new `/api` route must be described in the OpenAPI files in the same pull request, in English, YAML and Arabic. `testdata/openapi_undocumented.txt` only shrinks.
- A new problem code adds pages to maktaba.tv (`npm ci` and `npm run build` in `web/packages/website`, then commit `dist-manifest.json`). A new OpenAPI operation adds a wiki endpoint (`generate_wiki.py`). Run both before pushing a route or a problem.
- A stacked pull request runs only part of CI against its base. Expect the first full run after retargeting to main to find what the base's run skipped.

## Keep doing

- Give a reviewer the exact diff command, an ordered list of what to check and a tool-call budget. Broad briefs ran out of turns.

## Open

- #1463: the three minors above.
- Installing a Maktaba plugin from a store or file package (54.49, and Maktaba's key).
