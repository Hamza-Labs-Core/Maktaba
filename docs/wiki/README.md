# Maktaba Wiki

Wiki pages for the Maktaba platform — a self-hosted, RTL-first media library with Arabic-aware search, transcription, and multi-platform clients.

## Epic pages

### Cross-cutting infrastructure (this batch)

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

### Core platform (epics 01–13)

Specs live under [`specs/epics/01-scanner/`](../../specs/epics/) through [`specs/epics/13-desktop/`](../../specs/epics/). Wiki pages for these are out of scope of this batch.

## Cross-cutting wiki pages

- [Security architecture summary](security.md) — auth flow, JWT lifecycle, signed URLs, rate limits, input validation, TLS, secrets management. Cross-references Epic 10 + Epic 23.
- [Migration catalog](migrations.md) — canonical slots 0001–0028 plus per-epic claims for epics 07–24, reservation discipline, foundation tables, known collisions.
- [Glossary](glossary.md) — project-specific terminology with definitions and where-used references.

## Structured data

- [`db/wiki-epics-14-24.json`](db/wiki-epics-14-24.json) — JSON wiki database with one entry per epic + story + cross-cutting page. Fields: `id`, `type`, `title`, `epic`, `content`, `tags`, `related`, `file_paths`, `linear_issue`, `api_endpoints`, `db_tables`, `migrations`.

## Source artifacts

The wiki is derived from:

- [`specs/epics/`](../../specs/epics/) — per-epic READMEs, story specs, and implementation plans.
- [`specs/architecture.md`](../../specs/architecture.md) — normative architecture document.
- [`shared/db/migrations/MANIFEST.md`](../../shared/db/migrations/MANIFEST.md) — canonical migration slot reservations.
- [`web/mockups/`](../../web/mockups/) — HTML mockups (admin, theme components, web UI, mobile, desktop, TV).
- [`shared/api/`](../../shared/api/) — OpenAPI 3.1 spec for the REST surface.
