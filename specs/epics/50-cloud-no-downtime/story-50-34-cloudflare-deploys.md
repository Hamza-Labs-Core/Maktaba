# Story 50.34 — Cloudflare deploys from CI

> Epic 50 · Phase 1 · Depends on: 50.01 (names) · Needed by: 50.30, 50.31, 50.32, 50.21

## Why

README §15 puts four kinds of Cloudflare configuration in front of the cloud: two web Workers
(50.32), the zone's security rules (50.30), the Tunnel's ingress and DNS records (50.31), and
later the broadcast edge (50.21). Changed by hand in a dashboard, they drift from the repo, can't
be reviewed and can't be rolled back. The developer portal already shows the pattern:
`devportal-deploy.yml` builds and tests on pull requests, deploys from main with the org secrets
`CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`, skips cleanly when they are missing, and
smoke-checks the live site.

This story applies that pattern to every Cloudflare piece of the cloud: **deploys happen from
CI only**, each piece's token permissions are written down, and pull requests never call
Cloudflare.

## Description

### 1. The workflows

| Workflow | Pull request (offline) | Push to main (deploys) |
|---|---|---|
| `cloud-web-deploy.yml` (new) | build `dist-cloud/` and `dist-admin/`, the bundle check (50.32 T06), the Workers' Miniflare tests | `wrangler deploy` for `maktaba-cloud-web` and `maktaba-admin-web` (the built bundles from the same run), then the smoke check (§3) |
| `cloudflare-zone.yml` (new) | `zone.mjs check` (50.30), `tunnel.mjs check` (50.31) | `zone.mjs plan` then `apply`; `tunnel.mjs plan` then `apply` (the ingress and the hostnames' DNS records); read back |
| `cloudflare-ips.yml` (new, weekly) | — | fetches Cloudflare's published IP ranges (no token), opens a pull request when they change (50.31's fallback) |
| `devportal-deploy.yml` (existing) | unchanged | unchanged |
| `broadcast-edge.yml` (50.21) | as 50.21 says | its deploy job joins this pattern (same secrets, same skip rule) |

Common to every deploy job:

- **Path-filtered** to its own files (`.github/ci-paths.yml`), so a change elsewhere deploys
  nothing; `workflow_dispatch` for a by-hand run.
- **`concurrency`** per workflow, never cancelled in progress (one deploy at a time, a newer run
  waits).
- **`environment: production`** on the deploy job, so the `production` environment's rules
  (required reviewers, if the owner sets them) apply. These jobs read only the two org secrets
  (and, in `aop` mode, `CLOUDFLARE_AOP_CERT` / `CLOUDFLARE_AOP_KEY`); the Tunnel token belongs
  to the nodes' deploy (50.08, 50.31).
- **The skip rule:** when `CLOUDFLARE_API_TOKEN` or `CLOUDFLARE_ACCOUNT_ID` is empty, the job
  prints a `::notice` naming the missing secret and the README section, and passes, so main
  stays green before the owner sets them up.
- **Pinned tools:** `cloudflare/wrangler-action` at a pinned version with `wranglerVersion` from
  the package's lockfile; `tools/cloudflare/*.mjs` on Node 22 from `.nvmrc`.
- **No secrets in logs:** the `plan` output prints rule names, expressions and settings, never a
  token, and the job masks the account id.

### 2. The token: what each piece needs

One org token, `CLOUDFLARE_API_TOKEN`, holds the union of the CI rows, limited to this account
and the two zones (README §15.6 repeats the table for the owner):

| Piece | Account permissions | Zone permissions |
|---|---|---|
| Developer portal (existing) | Workers Scripts: Edit | `maktaba.dev`: Workers Routes: Edit, Zone: Read, DNS: Edit |
| User and admin web Workers | Workers Scripts: Edit | `maktaba.tv`: Workers Routes: Edit, Zone: Read |
| Zone security (WAF, managed rules, rate limits, settings) | — | `maktaba.tv`: Zone WAF: Edit, Zone Settings: Edit, Zone: Read |
| Tunnel ingress and DNS records | Cloudflare Tunnel: Edit | `maktaba.tv`: DNS: Edit |
| Per-hostname AOP certificate (50.31 fallback only) | — | `maktaba.tv`: SSL and Certificates: Edit |
| Broadcast edge (50.21, later) | Workers Scripts: Edit, Workers R2 Storage: Edit | `maktaba.tv`: Workers Routes: Edit, DNS: Edit |

- **Not in the CI token:** the nodes' DNS-01 token (50.02, DNS Edit on `maktaba.tv` only, kept
  on the nodes) and the Tunnel's own token (50.31, lets one `cloudflared` connect, nothing else).
- **Splitting later.** If the owner prefers one token per piece, each workflow reads its own
  secret name (`CLOUDFLARE_API_TOKEN_ZONE`, `…_WORKERS`) falling back to `CLOUDFLARE_API_TOKEN`;
  the code supports both from the start.
- **A permissions check.** `tools/cloudflare/token-check.mjs` runs first in every deploy job:
  it calls `GET /user/tokens/verify` and lists what the token can do on the account and zone,
  and fails with the missing permission's name before any write.

### 3. Smoke checks after a deploy

Each retries for up to 5 minutes (a first deploy's certificate or route takes time):

- `https://app.maktaba.tv/` answers 200 with the user app's marker and its CSP (50.32 §5);
  `https://app.maktaba.tv/v1/admin/accounts` answers **404**.
- `https://admin.maktaba.tv/` answers 200 with the console's marker, `X-Robots-Tag: noindex`
  and its CSP; `https://admin.maktaba.tv/v1/admin/accounts` without a session answers **401**.
- `https://api.maktaba.tv/readyz` answers 200 through the Tunnel (only once 50.31 is live; until
  then the check is skipped with a notice).
- After a zone apply: one request to `https://api.maktaba.tv/v1/admin/accounts` gets the edge's
  404 JSON (50.30 §5).

A failed smoke check fails the run; the fix or `wrangler rollback` follows (§5).

### 4. What never happens

- **Pull requests never call Cloudflare.** They have no secrets (GitHub doesn't pass them to
  pull requests from forks, and these jobs don't ask for them on any pull request).
- **No deploy from a laptop.** `wrangler deploy` and the `apply` scripts are only in the
  workflows; the READMEs say so. The owner's dashboard is for reading and for emergencies, and an
  emergency change is copied back into the repo (the next `plan` shows the drift).
- **No VPS deploy here.** The cloud binary and `cloudflared` on the nodes deploy through 50.08's
  pipeline, which writes the Tunnel token file.

### 5. Rollback

- Workers: `wrangler rollback` to the previous version, from a `workflow_dispatch` input
  (`rollback: true`) in `cloud-web-deploy.yml`.
- Zone rules and the Tunnel's ingress: revert the pull request; the next run applies the
  previous file.

## Acceptance criteria

- **Given** a pull request that changes a web Worker, the zone file or the Tunnel file,
  **when** CI runs,
  **then** it builds, tests and checks them, and no job calls a Cloudflare API.
- **Given** a push to main with the secrets missing,
  **when** a deploy job runs,
  **then** it prints the notice naming the secret and passes.
- **Given** a token missing Zone WAF: Edit,
  **when** `cloudflare-zone.yml` deploys,
  **then** `token-check` fails naming that permission, before any write.
- **Given** a successful deploy of the web Workers,
  **when** the smoke check runs,
  **then** `app.maktaba.tv/v1/admin/accounts` is 404 and `admin.maktaba.tv/v1/admin/accounts`
  without a session is 401.
- **Given** two pushes to main a minute apart,
  **when** both trigger a deploy,
  **then** the second waits for the first and neither is cancelled.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (python, `tools/ci/tests`) | `ci-paths.yml` and the new workflows | the existing paths check | every new job is gated by a filter; every filter is used |
| T02 | unit (node:test) | `token-check.mjs` against a recorded `tokens/verify` response and a permissions fixture | full token; token missing one permission | pass; fail naming it |
| T03 | unit (shell, `actionlint`) | the three new workflows | lint | clean; deploy jobs have `environment: production`, `concurrency` without cancel, the skip step |
| T04 | unit (node:test) | the smoke-check script with a local fake server | right headers; missing CSP; admin path 200 on the user host | pass; fail; fail |
| T05 | CI (pull request) | a PR changing the zone file | the workflow runs | only the check job; no secrets read |

## Edge cases

- **A first deploy of a Worker route** when the hostname has no DNS record yet: the Tunnel's
  apply (which writes the records) runs before the Workers in the same push, through
  `needs:`; on a by-hand Worker run before that, the smoke check waits, then fails clearly.
- **A rule the plan no longer allows** (after a downgrade): `zone.mjs check` fails the pull
  request (50.30), so a deploy never meets it.
- **The token expires.** `token-check` fails with Cloudflare's reason; the owner renews it and
  updates the org secret.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `.github/workflows/cloud-web-deploy.yml`, `cloudflare-zone.yml`, `cloudflare-ips.yml` (new) | The workflows. |
| `.github/ci-paths.yml`, `.github/workflows/ci.yml` | Filters and the pull-request jobs. |
| `tools/cloudflare/token-check.mjs`, `smoke.mjs` and tests (new) | Permissions check and smoke checks. |
| `web/packages/cloud-web/README.md`, `web/packages/admin-web/README.md` (new) | How each deploys, the token rows, the owner's checks (like the developer portal's README). |
| `deploy/cloudflare/README.md` (new) | The zone file, the Tunnel file, the workflows, the token table. |

### Tests

T01 and T03 in the lint job; T02 and T04 in the `cloudflare` job; T05 by the pull request itself.

### Rollout

Merge: the deploy jobs skip until the owner extends the token (README §15.7). Then the first
push to main that touches each piece deploys it, and the smoke checks confirm it.

### Rollback

Revert the workflows; nothing on Cloudflare changes until a deploy runs. Live pieces roll back
as §5 says.

### Dependencies

50.01 (the names). 50.30, 50.31 and 50.32 add the files these workflows deploy.
