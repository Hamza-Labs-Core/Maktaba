# Story 28.2 — Update check service

> Epic 28 · Auto-Update · Phase 2 (detection)

## Description

A server-side service that periodically asks GitHub whether a newer
Maktaba release exists, on the channel the operator chose, and caches the
answer so the rest of the system (and the UI) can read it cheaply.

Behaviour:

- **Source.** `GET https://api.github.com/repos/Hamza-Labs-Core/Maktaba/releases`
  (paginated; the first page is enough for "is there something newer").
  Unauthenticated calls are fine for public repos (60 req/h/IP); an
  optional `MAKTABA_GITHUB_TOKEN` raises the limit and is used if set.
- **Compare.** Parse semver from each release `tag_name`; pick the
  highest release allowed by the channel:
  - `stable` — ignore any release where `prerelease == true` or whose tag
    has a `-beta`/`-rc` suffix;
  - `beta` — include prereleases.
  Then compare against the running version (28.1).
- **Cache.** Hold the last result in memory with a TTL (default 24 h, the
  `auto-check interval`). API reads return the cached value; the cache is
  refreshed by the background goroutine and by an explicit
  `?refresh=true`.
- **Settings.**
  - `auto-check interval` (default 24 h; `0` disables the background
    poller),
  - `update channel` (`stable` | `beta`),
  - `disable check` (kill switch — no network calls at all).
  Sourced from env (`MAKTABA_UPDATE_*`) with the runtime
  `app_settings` table as override where present.
- **API.** `GET /api/system/updates` returns:
  ```json
  {
    "available": true,
    "current_version": "v1.4.1",
    "latest_version": "v1.4.2",
    "channel": "stable",
    "release_url": "https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/v1.4.2",
    "release_notes": "…markdown…",
    "checked_at": "2026-06-17T12:00:00Z",
    "assets": [
      {"name": "maktaba-server-1.4.2-linux-amd64.tar.gz", "url": "…", "size": 18234234}
    ]
  }
  ```
  When checking is disabled it returns `available:false` with a
  `"disabled":true` marker and no network call is made.

## Acceptance criteria

- **Given** the server runs `v1.4.1` on `stable` and the repo's latest
  non-prerelease is `v1.4.2`,
  **when** `GET /api/system/updates` is called,
  **then** `available:true`, `latest_version:"v1.4.2"`, with the release
  URL, notes, and the asset list.

- **Given** the server is on `stable` and the only newer release is
  `v1.5.0-rc.1` (prerelease),
  **when** the check runs,
  **then** `available:false` — the prerelease is ignored.

- **Given** the same server switched to `beta`,
  **when** the check runs,
  **then** `available:true`, `latest_version:"v1.5.0-rc.1"`.

- **Given** a check already ran < TTL ago,
  **when** `GET /api/system/updates` is called again,
  **then** GitHub is **not** hit again (cache served), and `checked_at`
  is unchanged.

- **Given** `?refresh=true`,
  **when** the endpoint is called,
  **then** the cache is bypassed and `checked_at` advances.

- **Given** `disable check` is set,
  **when** the endpoint is called,
  **then** `available:false`, `disabled:true`, and no network request is
  made.

- **Given** GitHub is unreachable,
  **when** the background check runs,
  **then** the last good cached result is retained, the error is logged
  (rate-limited), and the endpoint never 500s on a transient failure.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | releases list, stable channel | pick latest | newest non-prerelease |
| T02 | unit        | releases list, beta channel | pick latest | newest incl. prerelease |
| T03 | unit        | current=`1.4.2`, latest=`1.4.2` | compare | `available:false` |
| T04 | unit        | current=`1.9.9`, latest=`1.10.0` | compare | `available:true` (numeric, not lexical) |
| T05 | unit        | cache within TTL | second call | no fetch; same `checked_at` |
| T06 | unit        | `?refresh=true` | call | fetch performed |
| T07 | unit        | disabled | call | no fetch; `disabled:true` |
| T08 | integration | stub GitHub 200 | `GET /api/system/updates` | full envelope |
| T09 | integration | stub GitHub 403 rate-limit | call | served stale cache, logged |
| T10 | unit        | malformed tag `nightly-xyz` | parse | skipped, not a crash |

## Edge cases

- **GitHub rate limit (403/429).** Honour `Retry-After`/reset; serve
  stale cache; back off; never hammer.
- **Empty releases list.** `available:false`, no error.
- **Draft releases.** Excluded (the API only returns drafts to
  authenticated authors, but filter defensively).
- **Tag that doesn't parse as semver.** Skipped, logged at debug.
- **Clock skew on `checked_at`.** Informational only; no security
  decision rides on it.
- **Channel changed at runtime.** Next refresh recomputes; the cache key
  includes the channel so a stale cross-channel answer is never served.
- **Self-hosted fork.** `MAKTABA_UPDATE_REPO` overrides the
  `owner/repo`; falls back to `Hamza-Labs-Core/Maktaba`.

## Files / packages

- `api/internal/system/updater.go` (new) — fetch, parse, compare, cache,
  background loop, `GET /api/system/updates` handler.
- `api/internal/system/updater_test.go` (new).
- `api/internal/router/p28.go` (new) — mount.
- `api/main.go` — construct the updater, start the background goroutine,
  mount the route.

## Open questions

- **Persist `checked_at` / last-seen version across restarts?** v1 keeps
  it in memory (a restart just re-checks). A future enhancement could
  persist "last dismissed version" so a notification doesn't re-nag.
