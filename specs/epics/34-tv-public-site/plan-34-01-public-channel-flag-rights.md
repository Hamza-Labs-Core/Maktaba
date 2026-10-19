# Plan 34.1 — Public channel flag and rights — implementation

> Implementation plan for [story-34-01](story-34-01-public-channel-flag-rights.md).
> Migration slot 0122 (provisional, `0122_tv_public.sql`). Design: [design.md](design.md) §2.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Public state in its own table `tv_public_channels`, not columns on `channels`. | Keeps Epic 27's table untouched; audit fields and input ids live together; nodes get SELECT only. |
| D2 | One `Availability` function shared by publisher and station. | The guide and the feed can never disagree. |
| D3 | Rights confirmation is a recorded statement, not a check. | Maktaba cannot verify rights; it records who confirmed and when. |
| D4 | `public_id` = HMAC(site_salt, channel_id). | Stable, opaque, not reversible to internal ids. |

## 1. Files

**Create**
- `shared/db/migrations/0122_tv_public.sql` — the five tables of the README data model, grants (SELECT on
  `tv_public_channels`, `tv_public_exclusions` for `maktaba_worker`; everything else revoked)
- `api/internal/tvpublic/model.go` — types, repo (pg)
- `api/internal/tvpublic/availability.go` — `Availability(block, policy, exclusions, membership)`
- `api/internal/tvpublic/ids.go` — slug validation, `PublicID`
- `api/internal/handlers/tvpublic/channels.go`, `exclusions.go`, `offair.go` — routes of the README API
- `web/src/pages/LiveTV/PublicSite/*` — page, rights dialog, exclusions editor, "new on public" list
- tests next to each

**Modify**
- `api/internal/router` — mount `/api/tv/public/*` (admin only)
- `api/internal/handlers/rules/schema.json`, `schema.go` — section `tv_public` (all keys of the README
  settings table, ranges, en + ar descriptions)
- `streaming/internal/channel/repo_pg.go` — read `tv_public_channels` + exclusions for the station (34.3)
- `web/src/i18n/{en,ar}.json`

## 2. Steps

1. Migration + repo + `site_salt` creation in `home_secrets` on first use.
2. `Availability` with membership lookups (series id on the block, collection membership query, tags).
3. Handlers with validation (422 `rights_confirmation_required`, slug rules), audit columns.
4. "New on a public channel": query blocks of public channels in the horizon whose series id is not in
   `tv_public_reviews` (small table in 0122: channel_id, series_id, reviewed_at, by).
5. Capacity warning: call the Epic 32 cluster status (`GET /api/tv/cluster`) and compute design §5.4.
6. Admin page (en/ar), wired to the endpoints; panic switch with confirmation.

## 3. Tests

Story TCs; plus `TestGrantsNodeRoleSelectOnly` (node role cannot write).

## 4. Rollout

Ships dark (`tv_public.enabled = false`, all channels private). No behaviour change for existing users.
