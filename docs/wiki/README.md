# Maktaba Wiki

Wiki pages for the Maktaba platform — a self-hosted, RTL-first media library with Arabic-aware search, transcription, and multi-platform clients. Bridges Linear issues, story files, implementation plans, mockups, diagrams, and the API spec.

## Cross-reference pages

| Page | What it answers |
|------|-----------------|
| [INDEX.md](INDEX.md) | _Where do I start?_ — Top-level index across all wiki pages. |
| [linear-map.md](linear-map.md) | _Which files implement Linear issue HLB-N?_ — 220 issues mapped to story / plan / mockup / diagram / API endpoints. |
| [file-inventory.md](file-inventory.md) | _What is every file in this repo?_ — Annotated index of all mockups, specs, plans, diagrams, infra, and reviews. |
| [build-order.md](build-order.md) | _What do we build first?_ — Phase 0 → Phase 10 sequencing, critical-path stories, and parallelizable tracks. |
| [review-status.md](review-status.md) | _What audit findings remain?_ — Status of REVIEW.md and four PLAN_REVIEW files. |
| [features.md](features.md) | _What features exist?_ — Feature catalog across all epics. |
| [stories-map.md](stories-map.md) | _Where is story X?_ — Story-to-file mapping. |
| [entities.md](entities.md) | _What entities does the system model?_ — Domain entities. |
| [api-catalog.md](api-catalog.md) | _Which API endpoints exist?_ — REST endpoint catalog. |
| [diagrams.md](diagrams.md) | _Which diagrams exist?_ — Architecture and per-epic diagram index. |
| [mockups.md](mockups.md) | _Which mockups exist?_ — HTML mockup index. |
| [tech-stack.md](tech-stack.md) | _What's the stack?_ — Languages, frameworks, infra. |
| [deployment.md](deployment.md) | _How do we deploy?_ — Deployment topology and procedures. |

## Epic pages

### Pipeline (epics 01–06)

- [Epic 01 — Scanner](epics/epic-01-scanner.md)
- [Epic 02 — Audio Extraction](epics/epic-02-audio-extraction.md)
- [Epic 03 — Transcription](epics/epic-03-transcription.md)
- [Epic 04 — Subtitles](epics/epic-04-subtitles.md)
- [Epic 05 — Search Indexing](epics/epic-05-search-indexing.md)
- [Epic 06 — Job Queue](epics/epic-06-job-queue.md)

### API and clients (epics 07–13)

- [Epic 07 — API Server](epics/epic-07-api-server.md)
- [Epic 08 — Streaming](epics/epic-08-streaming.md)
- [Epic 09 — Library Management](epics/epic-09-library-management.md)
- [Epic 10 — Auth & Security](epics/epic-10-auth-security.md)
- [Epic 11 — Web UI](epics/epic-11-web-ui.md)
- [Epic 12 — Mobile](epics/epic-12-mobile.md)
- [Epic 13 — Desktop](epics/epic-13-desktop.md)

### Cross-cutting (epics 14–24)

- [Epic 14 — TV Apps](epics/epic-14-tv-apps.md)
- [Epic 15 — Discovery & Networking](epics/epic-15-discovery.md)
- [Epic 16 — Subscriptions & Monetization](epics/epic-16-subscriptions.md)
- [Epic 17 — UX Design System](epics/epic-17-ux-design-system.md)
- [Epic 18 — Performance](epics/epic-18-performance.md)
- [Epic 19 — Scalability](epics/epic-19-scalability.md)
- [Epic 20 — Testing](epics/epic-20-testing.md)
- [Epic 21 — Observability](epics/epic-21-observability.md)
- [Epic 22 — DevOps and Delivery](epics/epic-22-devops.md)
- [Epic 23 — Security](epics/epic-23-security.md)
- [Epic 24 — Data Integrity](epics/epic-24-data-integrity.md)

### Cloud (epics 25, 50)

- [Epic 25 — Cloud Relay](epics/epic-25-cloud-relay.md)
- [Epic 50 — Maktaba Cloud with no downtime](epics/epic-50-cloud-no-downtime.md)

### Server platform (epic 51)

- [Epic 51 — Every part of Maktaba can be turned on, off, added later or left out](epics/epic-51-configurable-components.md)

### Word review (epic 52)

- [Epic 52 — Word review: suspect words go to a person, in every language](epics/epic-52-transcript-word-review.md)

### Logs, errors and reports (epic 53)

- [Epic 53 — Logs, errors and problem reports, and a UI that never goes dark](epics/epic-53-logs-errors-reports.md)

### Plugins and the store (epic 54)

- [Epic 54 — Plugins: new functions from the queue, with no internet, from a Maktaba store](epics/epic-54-plugins.md)

## Cross-cutting wiki pages

- [Security architecture summary](security.md) — auth flow, JWT lifecycle, signed URLs, rate limits, input validation, TLS, secrets management. Cross-references Epic 10 + Epic 23.
- [Migration catalog](migrations.md) — canonical slots 0001–0028 plus per-epic claims for epics 07–24, reservation discipline, foundation tables, known collisions.
- [Glossary](glossary.md) — project-specific terminology with definitions and where-used references.

## Machine-readable index

The wiki database is generated from the specs by [`db/generate_wiki.py`](db/generate_wiki.py) and
read by the in-repo wiki app (`web/wiki-app`). A wiki app or build-tooling layer can consume it directly:

- [`../../web/wiki-app/public/db/wiki.json`](../../web/wiki-app/public/db/wiki.json): the meta, the
  names of its part files (`parts`) and every entry that is not a story or a plan.
- `../../web/wiki-app/public/db/wiki-epics-NN-MM.json`: the stories and plans of epics NN to MM.
- [`../../web/wiki-app/public/db/wiki-cross-refs.json`](../../web/wiki-app/public/db/wiki-cross-refs.json):
  Linear and file cross-references (issues, files, build_order, critical_path, parallel_tracks,
  reviews, epics, stories).
- `../../web/wiki-app/public/db/ar/`: the same files in Arabic (`"lang": "ar"`), below.
- [`db/wiki-schema.json`](db/wiki-schema.json): the JSON schema.
- [`db/curated.json`](db/curated.json): the hand-kept data the specs do not carry (epic names,
  phases and engines, hand-written summaries, Linear ids, the implementation phases, the
  cross-reference inventory). Edit it, never the generated JSON.

Every epic in `specs/epics/` is in the database. Regenerate after changing a spec, a mockup,
`shared/api/openapi.yaml` or `db/curated.json`:

```sh
python3 docs/wiki/db/generate_wiki.py
```

CI runs `generate_wiki.py --check` and fails when the committed database does not match the specs.

### In Arabic

The wiki is published in English at maktaba.dev/wiki/ and in Arabic, right to left, at
maktaba.dev/ar/wiki/. The Arabic database (`web/wiki-app/public/db/ar/`, the same files) is
generated from the translation store, [`db/ar/`](db/ar/): one file per epic (`epic-NN.json`,
the epic, its stories and its plans) and `common.json` (the phases, features, diagrams,
reviews, mockups, endpoints and tables). Each text is stored by entry id and field, with the
hash of the English it translates:

```json
{"entries": {"story-1.1": {"title": {"hash": "…", "ar": "…"}}}}
```

Code, identifiers, paths and endpoint names stay as written. An endpoint's summary reuses its
Arabic in `shared/api/openapi.ar.json`. **A spec change ships its Arabic:** `--check` fails,
naming the store file, the entry and the field, when a text has no Arabic, when its English
changed since it was translated, when the Arabic drops code the English has, or when an
entry's or a file's Arabic prose is mostly Latin script. To see what to translate, with the
hash to record:

```sh
python3 docs/wiki/db/generate_wiki.py --ar-todo [--epic N]
```

## Quick navigation by intent

- **"What's the next story to build?"** → [build-order.md](build-order.md) → Phase 0/1.
- **"I'm picking up HLB-47 — where's everything?"** → [linear-map.md](linear-map.md), search for `HLB-47`.
- **"Where does the OpenAPI spec live, and which stories own each endpoint?"** → [linear-map.md](linear-map.md) — every story row lists its endpoints.
- **"Are there open audit issues I should worry about before merging?"** → [review-status.md](review-status.md).
- **"Which mockups exist for Epic 11?"** → [file-inventory.md](file-inventory.md) → "HTML mockups" section.

## Source artifacts

The wiki is derived from:

- [`specs/epics/`](../../specs/epics/) — per-epic READMEs, story specs, and implementation plans.
- [`specs/architecture.md`](../../specs/architecture.md) — normative architecture document.
- [`shared/db/migrations/MANIFEST.md`](../../shared/db/migrations/MANIFEST.md) — canonical migration slot reservations.
- [`web/mockups/`](../../web/mockups/) — HTML mockups (admin, theme components, web UI, mobile, desktop, TV).
- [`shared/api/`](../../shared/api/) — OpenAPI 3.1 spec for the REST surface.

## Convention: how cross-references are wired

- **Story numbering.** `epic.story` (e.g. `03.06`). Two-digit zero-padded inside paths (`story-03-06-segment-commit.md`).
- **Linear IDs.** `HLB-N`. Sequential from HLB-5 = `01.01` through HLB-224 = `23.06`. Stories beyond HLB-224 are flagged.
- **Mockup filenames.** `mockup-{epic}-{story}-{slug}.html` for Web/Settings; `admin/{slug}.html` etc. for the platform-specific surfaces.
- **Diagram filenames.** `specs/diagrams/{kebab-name}.drawio`; per-epic story diagrams end in `-stories.drawio`.
- **API spec.** Single source: `shared/api/openapi.yaml` (and JSON twin). Every endpoint is owned by exactly one Epic 07 / 10 / 12 / 14 story.

---

_Generated from `docs/wiki/db/`. Edit the JSON and regenerate; do not hand-edit derived markdown._
