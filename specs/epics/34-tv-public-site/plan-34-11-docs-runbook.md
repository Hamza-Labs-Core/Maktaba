# Plan 34.11 — Docs and runbook — implementation

> Implementation plan for [story-34-11](story-34-11-docs-runbook.md).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | One runbook in English and Arabic next to the Epic 32 runbook. | The owner's working languages; remote and public share the Cloudflare account. |
| D2 | Costs explained with the pricing page link and the setting that holds the price. | Prices change; the doc stays correct. |

## 1. Files

**Create**
- `docs/tv-public-site.md`, `docs/ar/tv-public-site.md`
- `docs/diagrams/tv-public-site.svg` (from the README architecture)

**Modify**
- Epic 32 runbook — link to the public site section; account-wide budget
- control-centre help links (`web/src/...` help map)

## 2. Steps

1. Draft with the phase A content (guide-only), extend per phase.
2. Record the 34.3/34.4 spike results with dates; update the README facts table.
3. Dry run on the test account by a second person; fix gaps.

## 3. Tests

Docs lint in CI (links, headings).

## 4. Rollout

With each phase.
