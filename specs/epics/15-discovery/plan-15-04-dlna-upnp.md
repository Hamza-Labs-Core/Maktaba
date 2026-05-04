# Implementation Plan — Story 15.4 UPnP / DLNA compatibility

> Companion to [story-15-04-dlna-upnp.md](story-15-04-dlna-upnp.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| New service | `services/dlna/` (Go) — a separate process, run-by-default-disabled. Implements `MediaServer:1` with `ContentDirectory:1` + `ConnectionManager:1`. |
| Library | `github.com/anacrolix/dms` is the closest pure-Go DLNA media server; we vendor the parts we need (SOAP + SSDP) to avoid pulling in unrelated transcode logic. Justification: DLNA is a small, stable surface; vendoring keeps it self-contained. |
| Toggle | `[dlna] enabled = false` (default); admin toggles via Settings → Compatibility. |
| Browse tree | `Library` → (`Genre` | `Speaker` | `Recently Added`) → flat video list. |
| Direct-play only | DLNA clients can only play files we'd serve as direct-play (no transcoded HLS). The candidate list is `videos WHERE direct_play = true`. |
| Out of scope | DLNA controllers (rendering on a TV from a phone — that's CastV2/AirPlay, not in this epic); search through DLNA (out of v1). |

## 1. Architecture diagram

```
   Smart TV / VLC                        ┌──────────────────────────┐
   (DLNA control point)  ─── SSDP ─────► │ services/dlna            │
                                         │  - SSDP advertiser       │
                                         │  - SOAP /ContentDirectory│
                                         │  - HTTP byte server      │
                                         └─────────┬────────────────┘
                                                   │ reads videos table
                                                   ▼
                                            Postgres (read-only)
                                                   │
                                                   ▼
                                            file://... (sendfile)
```

## 2. Database additions

`shared/db/migrations/0052_dlna.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE dlna_settings (
    id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    enabled         BOOLEAN NOT NULL DEFAULT false,
    bind_iface      TEXT NOT NULL DEFAULT '',     -- '' = auto
    advertise_uuid  UUID NOT NULL                 -- derived from mdns_id
);
INSERT INTO dlna_settings (id, advertise_uuid)
VALUES (1, gen_random_uuid())
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS dlna_settings;
-- +goose StatementEnd
```

The `advertise_uuid` is derived deterministically from `mdns_id` in code, but persisting it lets ops change it without touching identity. EC: "DLNA UUID conflicts with another product on the LAN: pick a deterministic UUID derived from `mdns_id`." If conflict, ops can rotate via `UPDATE dlna_settings SET advertise_uuid = gen_random_uuid()`.

## 3. SSDP advertiser

`services/dlna/internal/ssdp/server.go`:

```go
type Server struct {
    cfg     Config
    iface   *net.Interface
    conn    *net.UDPConn
    closeCh chan struct{}
}

func (s *Server) Start(ctx context.Context) error {
    addr := &net.UDPAddr{IP: net.ParseIP("239.255.255.250"), Port: 1900}
    conn, err := net.ListenMulticastUDP("udp4", s.iface, addr)
    if err != nil { return err }
    s.conn = conn
    go s.respondToSearches(ctx)
    go s.advertisePeriodic(ctx)
    return nil
}
```

Refuses to advertise on non-private addresses (story EC: "Cellular network mistakenly being treated as LAN by UPnP IGD: we refuse to advertise on non-private addresses"):

```go
func validateAddr(ip net.IP) error {
    if !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLoopback() {
        return errors.New("dlna: refuse to advertise on non-private address")
    }
    return nil
}
```

## 4. Content directory

`services/dlna/internal/contentdir/handler.go` implements:

- `Browse(ObjectID, BrowseFlag, Filter, StartingIndex, RequestedCount, SortCriteria)`.

Object IDs encode the path: `0` (root), `0/1` (Genre), `0/1/<genre_id>` (a genre's videos), etc.

```go
func (c *ContentDirectory) Browse(ctx context.Context, in BrowseRequest) (BrowseResponse, error) {
    switch in.ObjectID {
    case "0":      return c.browseRoot()
    case "0/1":    return c.browseGenres(in.StartingIndex, in.RequestedCount)
    default:
        if strings.HasPrefix(in.ObjectID, "0/1/") {
            return c.browseGenre(strings.TrimPrefix(in.ObjectID, "0/1/"), in)
        }
        if strings.HasPrefix(in.ObjectID, "0/4/") {  // recently added
            return c.browseRecentlyAdded(in)
        }
    }
    return BrowseResponse{}, errBadObject
}
```

Each video item DIDL-Lite excerpt:

```xml
<item id="0/1/abc/v123" parentID="0/1/abc" restricted="1">
  <dc:title>...</dc:title>
  <upnp:class>object.item.videoItem</upnp:class>
  <res protocolInfo="http-get:*:video/mp4:*"
       size="123456789"
       duration="01:23:45.000"
       resolution="1920x1080">
       http://10.0.0.5:8200/dlna/file/v123
  </res>
  <upnp:subtitle>http://10.0.0.5:8200/dlna/sub/v123/en.srt</upnp:subtitle>
</item>
```

Subtitles: sidecar SRT exposed as `<upnp:subtitle>`; clients that don't support that field show no subtitles (graceful degradation).

## 5. Codec compatibility filter

The story EC: "DLNA-incompatible codec (HEVC on a 2014 TV): we don't advertise that file". Implementation:

- `videos.dlna_compatible` is a derived view: `WHERE codec IN ('h264','aac','mp3','jpeg','mpeg4')`. Anything HEVC, AV1, or transcoded HLS is excluded.
- Filter applied in every `Browse` query.

If we ever want per-client codec negotiation, we'd parse the User-Agent / `getProtocolInfo` response, but that's out of v1.

## 6. Byte server

`services/dlna/internal/byteserver/server.go`:

```go
func serveFile(w http.ResponseWriter, r *http.Request, path string) {
    f, err := os.Open(path)
    if err != nil { http.Error(w, "404", 404); return }
    defer f.Close()
    info, _ := f.Stat()
    http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)  // honors Range
}
```

`http.ServeContent` honors `Range: bytes=` headers, which DLNA clients depend on for seeking. On Linux, Go's `http.ServeContent` uses sendfile when there's no compression layer.

## 7. Test plan

### 7.1 SSDP

| Test | What it pins |
|---|---|
| `TestSSDPRespondsToMSearch` | Send `M-SEARCH * HTTP/1.1` → 200 OK with `LOCATION` header pointing at description.xml. |
| `TestSSDPRefusesPublicIP` | Bind to a 1.2.3.4 interface → start error; test asserts. |
| `TestSSDPStopsWithin30s` | Disable in config; advertiser sends `byebye`; new SSDP query within 30 s gets nothing. |

### 7.2 Content directory

| Test | What it pins |
|---|---|
| `TestBrowseRootContainsAllSections` | `Browse("0", BrowseDirectChildren)` returns 4 containers: Library, Genre, Speaker, Recently Added. |
| `TestBrowseFiltersIncompatibleCodecs` | A library with one HEVC + one H.264 file → only the H.264 listed. |
| `TestBrowseExcludesNonDirectPlay` | A video flagged `direct_play=false` → excluded. |
| `TestBrowsePagination` | 1000 items; `RequestedCount=100` → 100 items per page; `StartingIndex` advances correctly. |
| `TestSubtitleSidecarExposed` | A video with `en.srt` sidecar exposes `<upnp:subtitle>` URL. |

### 7.3 Byte server

| Test | What it pins |
|---|---|
| `TestRangeRequestsHonored` | `Range: bytes=1024-2047` → 206 Partial Content with the correct slice. |
| `TestUnknownVideoIDIs404` | Request a nonexistent path → 404, no path traversal. |
| `TestPathTraversalRefused` | URL-encoded `../etc/passwd` → 404, never escapes. |

### 7.4 Integration

| Test | What it pins |
|---|---|
| `e2e_VLCBrowsesAndPlays` | Spin up a VLC headless test client; browse → play → assert frame extracted matches expected. |
| `e2e_DisableStopsWithin30s` | Toggle off; SSDP `byebye` observed; subsequent M-SEARCH gets nothing. |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| HEVC on a 2014 TV | Excluded by codec filter; never advertised. | `TestBrowseFiltersIncompatibleCodecs` |
| Cellular network treated as LAN by UPnP IGD | SSDP refuses non-private addresses. | `TestSSDPRefusesPublicIP` |
| DLNA UUID conflicts | Operator regenerates `advertise_uuid`; deterministic from `mdns_id` by default. | `TestDLNAUUIDDeterministic` |
| Sidecar SRT missing | `<upnp:subtitle>` omitted; client shows no subs. | `TestNoSidecarOmitsField` |
| Path traversal attempt | 404; no fs escape. | `TestPathTraversalRefused` |
| Range request beyond EOF | 416 Range Not Satisfiable per HTTP spec. | `TestRangeBeyondEOF` |
| Multiple library roots | Each video's `path` is absolute; the byte server checks the absolute path is rooted in one of `library.root_path` allowlist before opening. | `TestPathRootedInLibrary` |
| Video deleted while client browsing | DIDL listing snapshot at request time; byte server returns 404 for missing files — DLNA clients show "cannot play". | `TestDeletedFileMidBrowse` |
| Disable when client mid-stream | In-flight HTTP byte streams are not killed; SSDP byebye sent so new browses don't appear. | `TestDisableMidStream` |
| 2018 Bravia parses subset | Common DIDL fields used; verified in e2e. | `e2e_BraviaSpecific` (manual or device-lab) |

## 9. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/anacrolix/dms` (vendored partial) | latest | SSDP + SOAP boilerplate. |
| stdlib `net/http` | go 1.22 | Byte server with Range. |

## 10. Acceptance checklist

**Service**
- [ ] `services/dlna` built and run (gated behind config).
- [ ] SSDP refuses non-private addresses.
- [ ] `Browse` paginates and filters incompatible codecs.

**Schema**
- [ ] `dlna_settings` exists; `advertise_uuid` deterministic from `mdns_id`.

**Tests**
- [ ] All §7 tests pass; e2e against VLC works.

**Docs**
- [ ] `docs/operations/dlna.md` covers TV-specific quirks.
- [ ] `specs/epics/15-discovery/README.md` ticks story 15.4.
