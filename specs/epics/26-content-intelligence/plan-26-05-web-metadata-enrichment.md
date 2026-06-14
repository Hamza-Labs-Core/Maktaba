# Plan 26.5 — Web metadata enrichment — implementation

> Implementation plan for [story-26-05-web-metadata-enrichment.md](story-26-05-web-metadata-enrichment.md).
> Self-contained. Cross-links: keyed off parsed titles
> ([Plan 26.1](plan-26-01-title-parser.md)) + classification
> ([Plan 26.2](plan-26-02-transcript-topic-extraction.md)); runs as the
> out-of-band `enrich` job
> ([Plan 26.7](plan-26-07-background-enrichment-pipeline.md)); promotion
> to `videos` and provenance live in
> [Plan 26.6](plan-26-06-enrichment-ui.md); API keys come from
> `api/internal/secret` (Epic 10). Writes slot 0077.

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **One shared `WebClient` core; thin per-provider adapters.** The core owns rate limiting (per-provider token bucket), on-disk response caching (TTL), retry/backoff, a circuit breaker, and an allow-list of hosts. Adapters are ~100-line field mappers. | Every provider shares the same failure modes (429/5xx/quota); solving them once keeps adapters trivial and uniform. |
| D2 | **Candidates are staging, never truth.** Enrichment writes `media_metadata_enrichment` only; it never touches `videos`. Promotion is 26.6's job. | Story AC: user edits are ground truth; matches are suggestions. |
| D3 | **Stable provider ids are first-class.** Every candidate stores `external_id` (`tmdb:movie:603`, `imdb:tt…`, `mbid:…`, `wikidata:Q…`, `youtube:…`). Re-enrich refreshes by id, not re-search. | Story AC: idempotent re-enrich; searches drift, ids don't. |
| D4 | **Provider selection by content type**, with YouTube as universal fallback. film→TMDb+OMDb; music_video→MusicBrainz; documentary/lecture→Wikidata; any→YouTube. | Story table; avoids wasting quota querying TMDb for a lecture. |
| D5 | **Raw responses cached in `web_metadata_cache` keyed by `(provider, request_hash)` with per-provider TTL.** Cache hit ⇒ free + offline. | Story AC: re-enrich offline; quota protection. |
| D6 | **Keys in the secret store; missing key ⇒ skip provider silently (log once).** No bundled keys. | Story AC + ToS: we can't ship keys; degrade gracefully. |
| D7 | **Poster/backdrop download is hardened:** allow-listed CDN host, size + dimension caps, content-type sniff, re-encode through the existing thumbnail path. Never store raw remote bytes as `poster_path`. | SSRF + image-bomb mitigations from the epic threat model. |
| D8 | **Confidence = title similarity × year match × (S/E alignment for TV).** Transparent, per-candidate. | Drives the 26.6 UI ranking and any auto-accept threshold. |

If D1 (shared core) is rejected for per-adapter HTTP: each adapter
re-implements rate limiting/caching/breakers inconsistently — the exact
class of bug that causes quota blowups. Rejected.

---

## 1. Package layout (Pipeline Service)

```
pipeline/src/maktaba_pipeline/enrich/
├── __init__.py
├── client.py            # WebClient: ratelimit + cache + retry + breaker + host allow-list (D1)
├── ratelimit.py         # per-provider token bucket + daily cap
├── cache.py             # web_metadata_cache read/write, TTL (D5)
├── breaker.py           # per-provider circuit breaker
├── posters.py           # hardened image fetch + re-encode (D7)
├── match.py             # confidence scoring (D8)
├── providers/
│   ├── base.py          # Adapter protocol: search(parsed, cls) -> list[Candidate]
│   ├── tmdb.py
│   ├── omdb.py
│   ├── youtube.py
│   ├── musicbrainz.py
│   └── wikidata.py
├── selector.py          # content_type → [providers] (D4)
├── service.py           # enrich_video(video_id): select → search → score → write candidates
├── repo.py              # media_metadata_enrichment + web_metadata_cache
└── tests/
    ├── test_client_ratelimit.py
    ├── test_client_cache.py
    ├── test_breaker.py
    ├── test_posters.py
    ├── test_match.py
    ├── test_providers_*.py   (recorded-fixture based)
    └── test_service.py
```

## 2. The shared client (`client.py`, D1)

```python
class WebClient:
    def __init__(self, *, provider, limiter, cache, breaker, allow_hosts):
        ...
    async def get_json(self, url, params, *, ttl) -> dict:
        host_allowed(url, self.allow_hosts)            # SSRF guard
        key = request_hash(self.provider, url, params)
        if (hit := self.cache.get(key)) is not None:   # D5
            return hit
        self.breaker.check()                            # raises ProviderPaused if open
        async with self.limiter.slot():                # D1 token bucket + daily cap
            resp = await self._retrying_get(url, params)  # backoff on 429/5xx
        self.cache.put(key, resp, ttl=ttl)
        return resp
```

The breaker trips after K consecutive failures/429s, pausing only that
provider; `selector` skips paused providers and the pause is surfaced in
Settings.

## 3. Adapter contract (`providers/base.py`)

```python
class Adapter(Protocol):
    name: str
    def configured(self, secrets) -> bool: ...                 # D6: has key?
    async def search(self, client, parsed, cls, *, locale) -> list[Candidate]: ...
    async def fetch_by_id(self, client, external_id, *, locale) -> Candidate: ...  # D3 re-enrich
```

`Candidate`: `provider`, `external_id`, `mapped` (normalised
title/overview/cast/genres/rating/poster_url/…), `confidence`,
`raw_ref`.

## 4. Service (`service.py`, D2/D3/D4)

```python
async def enrich_video(conn, secrets, video_id, *, force=False):
    parsed = await load_parsed(conn, video_id)         # 26.1
    cls    = await load_classification(conn, video_id) # 26.2
    accepted_id = await load_accepted_external_id(conn, video_id)
    if accepted_id and not force:                       # D3 idempotent refresh
        cand = await refresh_by_id(secrets, accepted_id, parsed, cls)
        await repo.upsert_candidate(conn, video_id, cand)
        return EnrichResult(match="refreshed")
    candidates = []
    for adapter in selector.for_content_type(cls.content_type):
        if not adapter.configured(secrets):             # D6
            log_once(f"{adapter.name}_no_key"); continue
        candidates += await adapter.search(client_for(adapter), parsed, cls, locale=locale)
    candidates = match.rank(candidates, parsed, cls)    # D8
    for c in candidates:
        if c.mapped.get("poster_url"):
            c.mapped["poster_local"] = await posters.fetch(c.mapped["poster_url"])  # D7
    await repo.replace_candidates(conn, video_id, candidates)
    return EnrichResult(match="none" if not candidates else "candidates")
```

## 5. Data model — migration slot 0077

```sql
-- Slot 0077 (Epic 26 / Story 26.5)
CREATE TABLE IF NOT EXISTS media_metadata_enrichment (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id     UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    provider     TEXT NOT NULL,                  -- tmdb|omdb|youtube|musicbrainz|wikidata
    external_id  TEXT NOT NULL,                  -- stable, namespaced (D3)
    mapped       JSONB NOT NULL DEFAULT '{}'::jsonb,
    confidence   REAL NOT NULL DEFAULT 0,
    is_accepted  BOOLEAN NOT NULL DEFAULT false, -- set by 26.6 accept
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (video_id, provider, external_id)
);

CREATE TABLE IF NOT EXISTS web_metadata_cache (
    provider     TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response     JSONB NOT NULL,
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (provider, request_hash)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS enrichment_video_idx
    ON media_metadata_enrichment (video_id, confidence DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS web_metadata_cache_expiry_idx
    ON web_metadata_cache (expires_at);
```

(The on-disk cache may alternatively live on the filesystem; this plan
uses a DB table so the cache is covered by the same backup/restore path
as the rest of the server. A janitor deletes `expires_at < now()` rows.)

## 6. Settings & secrets

- Provider keys: stored via `api/internal/secret`; entered in Settings
  UI (`web/src/pages/Settings`). Never returned by any API; never logged.
- Per-library: `settings.enrich.enabled`, `settings.enrich.providers`
  (per-provider toggle), `settings.enrich.locale`,
  `settings.enrich.auto_accept_threshold` (default off).
- A Settings panel shows per-provider status: configured?, breaker
  state, today's call count vs. cap.

## 7. Files to create / modify

**Create:** `pipeline/.../enrich/*` (client, providers, service, repo),
migration pair, recorded provider fixtures under `tests/fixtures/enrich/`.

**Modify:** Settings UI (provider keys + status), `api/internal/secret`
usage, `MANIFEST.md` (slot 0077). The enqueue/scheduling of `enrich`
jobs is [Plan 26.7](plan-26-07-background-enrichment-pipeline.md).

## 8. Dependencies

- **26.1**, **26.2** (inputs), **Epic 10** secret store, **Epic 08**
  thumbnail pipeline (poster re-encode). Runtime: an HTTP client
  (`httpx`, likely already present) — no provider SDKs (thin REST).

## 9. API contract (provider request shapes)

Documented per adapter in `providers/*.py` docstrings (endpoint, params,
auth header, the subset of response fields mapped). Recorded fixtures
pin the mapping so a provider response-shape change is caught in CI.

## 10. Test strategy

Adapters tested against **recorded** JSON fixtures (no live calls in CI).
`client` tests cover rate-limit throttling, cache hit/offline, breaker
isolation. `posters` tests cover host allow-list, size cap, wrong
content-type rejection. `service` test asserts candidates-only (no
`videos` mutation) and id-based idempotent re-enrich.

## 11. Security

SSRF: only hard-coded provider hosts + allow-listed poster CDNs are
reachable; no user-supplied URL is ever fetched. Keys never leave the
box, never go to the cloud (Epic 25 is uninvolved). Only parsed
title/year/topic strings egress — never transcripts, paths, or media.
