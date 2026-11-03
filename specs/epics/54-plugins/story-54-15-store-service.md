# Story 54.15 — The store service and developer accounts

> Epic 54 · Phase 2 · Depends on: 54.14, Epic 50 (50.07) · Needed by: 54.16, 54.17, 54.19

## Why

Someone has to hold the catalog, the packages, the developer accounts and the review queue.
Maktaba Cloud already has accounts, a licence issuer, an admin console and (after Epic
50) two machines that cover for each other. The store is a new part of it, not a new service to
run.

## Description

### 1. Where it runs

- Package `cloud/internal/store/` in the cloud API binary, its own tables in the cloud database
  (migrations in `cloud/migrations/`, next free slot), its own routes under `/store/v1/`.
- Packages, icons and screenshots in the cloud's object storage (Epic 50.09's bucket, prefix
  `store/`), served through the CDN at `store.maktaba.tv` (immutable, content-addressed URLs
  `/{sha256}.mkplugin`).
- Epic 50's two nodes and zero-downtime deploys apply. The catalog and revocation list are
  static signed files regenerated on every change and on a timer, so homes keep working from
  the CDN if the API is down.

### 2. Tables

| Table | Holds |
|---|---|
| `store_developers` | account id (Epic 25 user), display name, verified prefixes, agreement version accepted, payout account (Phase 3), status |
| `store_developer_keys` | developer, key id, public key, added, revoked |
| `store_plugins` | id, developer, names and summaries (en/ar), category, tags, status (`draft`, `listed`, `delisted`, `removed`) |
| `store_versions` | plugin, version, sha256, size, manifest JSON, capabilities, maktaba range, plugin API, review level, status (`submitted`, `checking`, `in_review`, `approved`, `rejected`, `withdrawn`), release notes (en/ar) |
| `store_submissions` | version, uploaded by, source archive object key, checks report, reviewer, decision, reasons |
| `store_revocations` | plugin, versions, key id, action, reason code, by, at |
| `store_installs` | home id, plugin, version, first/last seen (from catalog fetches with a signed home assertion; used for counts and rating eligibility) |
| `store_ratings`, `store_reports` | 54.18 |
| `store_products`, `store_purchases` | 54.19 |

### 3. Developer accounts

- Sign up with an existing Maktaba Cloud account at **maktaba.tv/developers**; verify email;
  **two-factor sign-in required** to submit or change anything (the cloud's existing auth adds
  TOTP if not already there).
- Accept the **developer agreement** (versioned; a new version must be accepted before the next
  submission).
- **Id prefix:** prove a domain with a DNS TXT record `maktaba-developer=<token>` (checked by
  the store, re-checked monthly), or take a community prefix `tv.maktaba.community.<handle>`.
- **Keys:** add public keys from `mkp keys export-public`; revoke; at least one active key to
  submit.
- **Roles:** developer (own plugins only); `store_reviewer` and `store_admin` for the Maktaba
  service admin (Epic 42), every action audited in the cloud's `audit` table. Role tests for
  every route as no one, a plain cloud user, a developer of another plugin, the developer, a
  reviewer and a store admin.

### 4. The console

maktaba.tv/developers (the cloud web app): plugins, versions and their check reports and review
status, keys, the agreement, stats (installs by version, ratings, reports summary), and (Phase
3) payouts. English and Arabic.

### 5. Store API

| Route | Who | What |
|---|---|---|
| `POST /store/v1/submissions` | developer (2FA) | upload package + source archive + notes; starts checks (54.16) |
| `GET /store/v1/submissions/{id}` | developer, reviewer | status and report |
| `POST /store/v1/plugins/{id}/withdraw` | developer | delist own plugin |
| `GET /store/v1/catalog/{api-major}.json` | public (CDN) | signed catalog |
| `GET /store/v1/revoked.json`, `/store/v1/store-keys.json` | public (CDN) | signed lists |
| `GET /store/v1/packages/{sha256}` | public (CDN), paid ones need an entitlement (54.19) | the file |
| `POST /store/v1/homes/{id}/seen` | a home over the cloud link (signed home assertion) | installed plugin ids and versions, for counts and rating eligibility |
| `/store/v1/admin/*` | reviewer, store admin | review queue, decisions, revocations |

The catalog: `{version, issued, expires (+7 d), api_major, plugins: [{id, developer, names,
summaries, icon, category, tags, rating avg/count, installs (rounded), price, versions:
[{version, sha256, size, url, maktaba, plugin_api, platforms, capabilities, review_level,
released}]}]}`, signed by S (54.14). Only `approved` versions of `listed` plugins appear; one
catalog per plugin API major.

### 6. Privacy

The store keeps the home id (for linked homes), plugin ids and versions installed, and server
version and platform. It never receives libraries, titles, plugin data or logs. `seen` reports
are skipped when the owner turned telemetry off; then counts and rating eligibility use only
downloads made through the cloud link.

## Acceptance criteria

- **Given** a cloud user without 2FA,
  **when** they try to submit,
  **then** `403 two_factor_required`.
- **Given** developer A,
  **when** they call any route on developer B's plugin,
  **then** 403, and the attempt is audited.
- **Given** an approved version,
  **when** the catalog regenerates,
  **then** it appears with its sha256 and review level, and the catalog verifies with 54.14.
- **Given** the cloud API down,
  **when** a home fetches the catalog and a package,
  **then** both come from the CDN and verify.
- **Given** a prefix claimed by DNS and the TXT record removed,
  **when** the monthly check runs,
  **then** new submissions under it are blocked and the developer is emailed; listed plugins
  stay.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | role test (go) | every store route × 6 roles | call | per §3 |
| T02 | integration (PG, go) | submit → approve (54.16 stubbed) | regenerate | catalog contains it; signature valid |
| T03 | integration (go) | API down, CDN fixture | home fetch | works |
| T04 | unit (go) | DNS TXT check (fake resolver) | verify / lapse | per AC |
| T05 | integration (PG, go) | `seen` with bad assertion | POST | 401 |
| T06 | unit (web) | console pages en/ar | render | RTL, strings |
| T07 | CI (`store`) | full flow with test keys, no live calls | e2e | pass |

## Edge cases

- **Plugin id squatting** (`dev.example.plex`): ids containing trademarks of others are flagged
  for review (a reviewer-maintained list); the agreement covers removal.
- **Developer deletes their account**: plugins are delisted; installed copies keep working;
  paid entitlements stay valid until their end (54.19).
- **Large uploads**: resumable upload to object storage with a signed URL; 50 MB package + 200
  MB source limit.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/store/` (new) | Store logic, catalog generation, signer (uses 54.14). |
| `cloud/internal/handlers/store/` (new) | Routes. |
| `cloud/migrations/NNNN_store.sql` (new) | Tables. |
| `cloud/internal/auth/` | Require TOTP for developer actions. |
| `cloud/internal/stores/` | Developer and key stores. |
| Cloud web app (developer console) | Pages, en + ar. |
| `deploy/cloud-relay/` | `store.maktaba.tv` CDN and bucket prefix (Epic 50 owns infra; this adds config only). |
| `.github/workflows/` `store` job | T07. |

### Tests

- Role T01; integration T02, T03, T05; unit T04, T06; CI T07. No live payment, DNS or object
  storage calls: fakes only.

### Rollout

Deployed dark (no listed plugins) on Epic 50's nodes. Opening the store to developers is an
owner decision after the root keys exist (54.14) and the reviewers are named.

### Rollback

Store routes and tables are additive to the cloud; delisting everything empties the catalog;
homes keep their installed plugins.

### Dependencies

54.14; Epic 50 50.07 (two nodes) and 50.09 (object storage); Epic 25 accounts.
