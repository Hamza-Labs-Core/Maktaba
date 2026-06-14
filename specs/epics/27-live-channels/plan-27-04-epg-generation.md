# Plan 27.4 — EPG generation & exports — implementation

> Implementation plan for [story-27-04-epg-generation.md](story-27-04-epg-generation.md).
> Self-contained. Cross-links: pure read path over `channel_programs`
> (slot 0082, [Plan 27.2](plan-27-02-program-scheduler.md)) + `channels`
> (slot 0081); the channel slug (27.1 D4) is the XMLTV/M3U id that joins
> to the lineup in [Plan 27.5](plan-27-05-hdhomerun-emulation.md). Reuses
> `libraryacl` (slot 0072) + the scoped stream-token model (Epic 8/25).
> **Adds no migration.**

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Guide is a read path, not a store.** All outputs are time-range queries over `channel_programs`; only export *responses* are cached (short TTL). | Story AC7/AC8 — the schedule is the single truth; a parallel store is a consistency hazard. |
| D2 | **`title_snapshot` powers the payload**, so a guide read doesn't join the whole library. | Story payload note — cheap, stable reads. |
| D3 | **XMLTV/M3U `tvg-id` == channel `slug`.** | Story AC6 — external players join playlist↔guide by this id; the slug is the stable cross-format key. |
| D4 | **External exports use a scoped access token, not the interactive session.** | Story AC9/EC7 — a token exposes only its channels; a minter's broader access must not leak. |
| D5 | **Filler/bumper blocks collapse by default** in human/external guide output. | Story AC10 — don't shred the EPG with 15-s rows. |
| D6 | **XMLTV is streamed, not buffered**; capped to the generated horizon. | Story EC3 — large lineups/horizons must not balloon memory. |

---

## 1. Package layout (API Service, Go)

```
api/internal/handlers/guide/
├── guide.go        # GET /api/channels/guide, /{id}/guide, /now (D1/D2)
├── xmltv.go        # GET /api/channels/xmltv — streamed XMLTV writer (D3/D6)
├── m3u.go          # GET /api/channels/playlist.m3u (D3)
├── collapse.go     # filler/bumper collapsing for human/external output (D5)
├── cache.go        # short-TTL response cache keyed by range + visible-channel set (D1)
├── token.go        # scoped export-token validation (D4)
├── repo.go         # time-range reads over channel_programs
└── guide_test.go
```

## 2. Core read (`guide.go`, D1/D2)

```go
// blocksInRange returns guide payloads for the visible channels overlapping [start,end).
func (h *Guide) blocksInRange(ctx, channels []uuid.UUID, start, end time.Time) []GuideBlock {
    rows := h.repo.ProgramsOverlapping(ctx, channels, start, end) // index channel_programs(channel_id,start_at,end_at)
    return mapToPayload(rows)                                     // from title_snapshot (D2)
}
```

`/now` is `blocksInRange(channels, now, now)` for the current block +
the immediately following block per channel, annotated with
`progress = (now - start_at) / (end_at - start_at)` and `is_live=true`.
This is the payload [27.9](plan-27-09-home-widget.md) and the
[27.1](plan-27-01-channel-definition.md) `now_playing` summary consume.

## 3. XMLTV (`xmltv.go`, D3/D6)

Streamed writer:

```xml
<tv generator-info-name="Maktaba">
  <channel id="{slug}">
    <display-name>{name}</display-name>
    <icon src="{logo_url}"/>
  </channel>
  ...
  <programme start="{start +offset}" stop="{stop +offset}" channel="{slug}">
    <title>{title}</title>
    <sub-title>{episode_title}</sub-title>
    <desc>{desc}</desc>
    <category>{category}</category>
    <icon src="{poster_url}"/>
    <episode-num system="xmltv_ns">{s-1}.{e-1}.</episode-num>
    <episode-num system="onscreen">S{s}E{e}</episode-num>
  </programme>
</tv>
```

Validates against the XMLTV DTD (a CI fixture test). Collapsed filler
(D5) is omitted or rendered as a generic "Up Next" programme.

## 4. M3U (`m3u.go`, D3)

```
#EXTM3U url-tvg="{base}/api/channels/xmltv"
#EXTINF:-1 tvg-id="{slug}" tvg-name="{name}" tvg-logo="{logo}" tvg-chno="{number}" group-title="{category}",{name}
{base}/stream/channel/{id}/manifest.m3u8
...
```

`tvg-id` == slug == XMLTV `<channel id>` (D3), so VLC/Plex join the two.

## 5. API contract

```
GET /api/channels/guide?start=&end=&category=    → grid payload (ACL-scoped)
GET /api/channels/{id}/guide?start=&end=         → one channel (+ horizon_until marker)
GET /api/channels/now                            → current+next per channel (+progress)
GET /api/channels/xmltv[?token=]                 → XMLTV (token-scoped for external, D4)
GET /api/channels/playlist.m3u[?token=]          → M3U (token-scoped for external, D4)
```

## 6. Files to create / modify

**Create:** everything under `api/internal/handlers/guide/`.

**Modify:**
- `api/internal/router` — mount guide routes (interactive behind
  `libraryacl`; export endpoints behind the scoped token, D4).
- Reuse the existing scoped-token minting (the same model `/auto/v{ch}`
  uses, [27.5](plan-27-05-hdhomerun-emulation.md)).

## 7. Dependencies

- **27.1** (`channels` + slug), **27.2** (`channel_programs` +
  `title_snapshot`), slot 0072 (`libraryacl`), Epic 8/25 scoped token.
  No migration.

## 8. Test strategy

`test_xmltv_validates` (DTD fixture), `test_m3u_tvg_id_matches_xmltv`,
`test_guide_matches_stream` (guide current block == engine's reported
block), cache hit/invalidation, ACL/token scoping (a token-scoped export
omits forbidden channels), filler collapse, horizon marker.

## 9. Performance

Every output is O(blocks-in-range) on the `(channel_id,start_at,end_at)`
index; never a library scan. Short-TTL caching absorbs the bursty
external poll pattern (Plex/Jellyfin re-fetch guide periodically); XMLTV
streams so a 7-day × 50-channel export is constant-memory.
