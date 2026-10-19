# Story 34.10 — Deployment pipeline

## Description

*As the owner, I want Maktaba to put the public site on my Cloudflare account, try new versions on
staging first, promote them, and roll back with one click; as a developer, I want to run and deploy the
site with wrangler.*

Design: [design.md](design.md) §10; README R10.

## Acceptance criteria

- **AC1** `web/tv-site` builds with `pnpm build` into `dist/` (assets) and `worker.js`; CI runs
  typecheck, lint, unit (Vitest + Workers pool), Playwright e2e against `wrangler dev`, the no-home-links
  scan and a bundle size budget (initial JS ≤ 150 KB gzip, hls.js lazy).
- **AC2** `release.yml` attaches `maktaba-tv-site-<version>.tar.gz` with `dist/`, `worker.js`,
  `deploy-manifest.json` (bindings, DO classes and migration tags, schema, required secrets) and SHA-256
  sums; the server release knows the matching version.
- **AC3** `tv_public_deploy` job (queue engine) deploys an environment through the Cloudflare API in the
  order of design §10.3 (bucket, widget/app, assets upload session, script with bindings and DO
  migrations, secrets, custom domain, health check), recording `tv_public_deploys`.
- **AC4** "Test connection" lists missing token permissions for the chosen options (Workers Scripts,
  R2, Custom Domains/DNS, Turnstile, Access, Stream, Analytics) before any deploy.
- **AC5** Admin buttons: **Deploy to staging**, **Promote to production** (same tarball as staging),
  **Roll back** (previous production version); each shows progress in the control centre.
- **AC6** A failed health check after deploy re-deploys the previous version automatically and reports why.
- **AC7** Staging: separate Worker, bucket and DOs, always `noindex`, Turnstile gate even in `public`,
  no production video unless `staging_video`.
- **AC8** Developer path: `pnpm -C web/tv-site dev`, `deploy:staging`, `deploy:production` (wrangler
  `--env`) work against a test account with `.dev.vars`; `tv-site.yml` (manual dispatch) deploys staging
  to the project's test account only.
- **AC9** The Worker reports `SITE_VERSION` and `SCHEMA` at `/api/v1/version`; the home refuses to
  publish a snapshot schema the deployed Worker does not support and suggests a deploy.

## Test cases

- **TC1** Go `TestDeployOrder`, `TestDeployRollbackOnHealthFail`, `TestPromoteUsesStagingTarball`
  (fake CF API recording calls).
- **TC2** `deploy-manifest.test.ts` (manifest matches `wrangler.jsonc` bindings and DO migrations).
- **TC3** CI job `tv-site` green on a PR touching `web/tv-site/**`.

## Edge cases

- **EC1** DO migration in a new version: additive only (new classes/tags); a rollback keeps the newer
  storage (code tolerates unknown fields).
- **EC2** Cloudflare API rate limit (1,200 / 5 min): the job batches asset uploads and backs off on 429.
- **EC3** Token rotated by the owner mid-deploy: the job fails cleanly and keeps the live version.

## Dependencies

34.4 (Worker), queue engine, Epic 32.6 (token storage and test connection), `release.yml`.
