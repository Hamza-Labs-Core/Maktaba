# Plan 34.10 — Deployment pipeline — implementation

> Implementation plan for [story-34-10](story-34-10-deployment-pipeline.md).
> Uses slot 0122 (`tv_public_deploys`). Design: [design.md](design.md) §10.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The product deploys through the Cloudflare API from the home. | Each owner uses their own account and domain; no CI secrets for user accounts; one-click from the admin page. |
| D2 | CI builds and tests; releases carry a versioned tarball. | Reproducible; promote = same bytes as staging. |
| D3 | Wrangler for developers only. | Fast local loop; never the product path. |
| D4 | DO migrations additive only. | Rollback without down-migrations. |

## 1. Files

**Create**
- `web/tv-site/wrangler.jsonc` (envs `staging`, `production`), `web/tv-site/deploy-manifest.json`,
  `web/tv-site/scripts/pack.mjs` (tarball + sums), `web/tv-site/package.json` scripts
  (`dev`, `build`, `test`, `e2e`, `deploy:staging`, `deploy:production`)
- `.github/workflows/tv-site.yml` — manual dispatch: build + deploy staging to the project test account
- `api/internal/tvpublic/deployer.go` — job `tv_public_deploy`: steps of design §10.3, Cloudflare calls
  (R2 bucket, assets upload session, script upload with metadata/bindings/migrations, secrets, custom
  domain), health check, auto-rollback; tarball cache on the home
- `api/internal/tvpublic/permissions.go` — token permission check per option (extends Epic 32.6 test)
- `api/internal/handlers/tvpublic/deploy.go` — deploy/promote/rollback endpoints
- `web/src/pages/LiveTV/PublicSite/Deploy.tsx`

**Modify**
- `.github/workflows/ci.yml` — `tv-site` job (paths `web/tv-site/**`, `web/packages/**`)
- `.github/workflows/release.yml` — build + attach `maktaba-tv-site-<version>.tar.gz`
- `deploy/` docs — mention the tarball

## 2. Steps

1. Wrangler project and local dev; unit + e2e in CI.
2. Pack script and manifest; release attachment.
3. Deployer against a fake CF API (record/replay), then against the test account (manual).
4. Health check (version, home WS, test commit) and rollback.
5. Admin page with progress in the control centre.

## 3. Tests

Story TCs.

## 4. Rollout

Phase A: staging and production deploys of the guide-only site.
