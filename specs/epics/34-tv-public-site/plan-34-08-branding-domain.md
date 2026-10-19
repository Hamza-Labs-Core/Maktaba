# Plan 34.8 — Branding and domain settings — implementation

> Implementation plan for [story-34-08](story-34-08-branding-domain.md). Design: [design.md](design.md) §3.1, §4, §10.3.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Branding is data (`site.json`), not a build. | Changes apply in seconds through the publisher; no redeploy. |
| D2 | Domain via Workers Custom Domains. | Cloudflare creates DNS and the certificate; no origin needed. |
| D3 | Domain switch order: attach → cert → origins/widget/Access → canonical → remove old. | Playback never breaks during the change. |

## 1. Files

**Create**
- `api/internal/tvpublic/branding.go` — validation (sizes, AA contrast via WCAG formula, Markdown sanitise)
- `api/internal/tvpublic/domain.go` — zone lookup, conflict check, custom domain attach/detach, cert poll,
  ordered switch, 301 redirect config for the old host
- `web/src/pages/LiveTV/PublicSite/Branding.tsx`, `Domain.tsx`, live preview (iframe of staging/workers.dev)

**Modify**
- `api/internal/handlers/rules/schema.json` — `tv_public.branding.*`, `domain`, `staging_domain`
- `api/internal/tvpublic/snapshot.go` — `site.json` from branding
- `api/internal/tvpublic/inputs.go` — origins sync entry point
- `web/tv-site/src/theme.ts` — CSS variables from `site.json`; `worker/src/index.ts` — old-host redirect

## 2. Steps

1. Branding settings + validation + publish.
2. Site theming live from `site.json` (and on LiveHub `site` events).
3. Domain attach for staging, then production; ordered switch with progress in the control centre.
4. Redirect for the previous custom domain (30 days).

## 3. Tests

Story TCs.

## 4. Rollout

Phase C, when the owner has chosen the domain; until then workers.dev.
