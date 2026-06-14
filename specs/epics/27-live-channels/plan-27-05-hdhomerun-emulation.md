# Plan 27.5 — HDHomeRun emulation — implementation

> Implementation plan for [story-27-05-hdhomerun-emulation.md](story-27-05-hdhomerun-emulation.md).
> Self-contained. Cross-links: serves the MPEG-TS output of the channel
> engine ([Plan 27.3](plan-27-03-live-stream-engine.md), `mpegts.go`,
> D9); lineup ids == channel slug/number, paired with the XMLTV guide
> ([Plan 27.4](plan-27-04-epg-generation.md)); reuses the scoped
> stream-token + lazy-activation/reaper (Epic 8 / 27.3). Lives in the
> Streaming Service (Go). Writes slot 0084 (`hdhr_device`,
> `hdhr_tuner_leases`).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **HDHomeRun emulation lives in streaming**, beside the channel engine. | It serves video (MPEG-TS), must be LAN-reachable for SSDP, and reuses the engine's schedule/look-ahead — all streaming concerns. |
| D2 | **`/auto/v{ch}` is the channel engine's MPEG-TS output** (`-f mpegts`), joined at wall-clock offset, one FFmpeg per tuner connection. | Plex/Jellyfin pull continuous TS, not HLS; reuse the engine's join + look-ahead, swap the mux. |
| D3 | **`DeviceID` is generated once and persisted** in `hdhr_device`. | Story EC1 — Plex binds its DVR to the id; it must be stable across restarts. |
| D4 | **`BaseURL` is derived from the inbound request, not static config.** | Story EC2 — works over LAN IP and the relay host alike. |
| D5 | **Tuner connections are leased, capped at `TunerCount`**; a lease maps to one engine MPEG-TS consumer; release on disconnect → reaper. | Story AC6/AC7 — finite tuners; idle costs nothing; matches real HDHomeRun semantics. |
| D6 | **`/auto/v{ch}` requires the scoped stream token; discovery is harmless metadata.** | Story AC8 — the token (embedded in advertised URLs) scopes lineup + stream; SSDP doesn't bypass auth. |
| D7 | **Feature is off by default (`hdhr_device.enabled=false`); SSDP silent + endpoints 404 when off.** | Story AC9 — opt-in; no surprise LAN advertisement. |
| D8 | **`TunerCount` validated ≤ the per-host concurrent-channel cap** (27.3 D6). | Story EC4 — never advertise more tuners than the box can transcode. |

---

## 1. Package layout (Streaming Service, Go)

```
streaming/internal/hdhr/
├── ssdp.go         # UPnP M-SEARCH responder on udp/1900 (D1, AC1)
├── device.go       # /device.xml + /discover.json (D3/D4)
├── lineup.go       # /lineup.json + /lineup_status.json + /lineup.post (AC3/AC4)
├── tuner.go        # /auto/v{ch}: lease → engine MPEG-TS → response (D2/D5)
├── leases.go       # tuner-lease registry (slot 0084) (D5)
├── repo.go         # hdhr_device (singleton) + hdhr_tuner_leases
└── hdhr_test.go
```

Mounted in the streaming HTTP server; the SSDP responder is a goroutine
started with the server (only when `enabled`).

## 2. Discovery (`ssdp.go` + `device.go`, D3/D4)

- **SSDP:** listen on udp/1900 multicast; on `M-SEARCH` with a matching
  `ST`, reply unicast with `LOCATION: {BaseURL}/device.xml`. `BaseURL`
  derived from the local interface the request arrived on (D4).
- **`/discover.json`:**

```json
{
  "FriendlyName": "Maktaba",
  "Manufacturer": "Maktaba",
  "ModelNumber": "HDTC-2US",
  "FirmwareName": "maktaba_atsc",
  "TunerCount": 4,
  "DeviceID": "{stable persisted id}",
  "DeviceAuth": "{scoped-token}",
  "BaseURL": "{derived}",
  "LineupURL": "{derived}/lineup.json"
}
```

`ModelNumber`/`FirmwareName` use values Plex/Jellyfin/Emby accept as a
clear-QAM/ATSC tuner (documented per-app quirks, Story EC7).

## 3. Lineup (`lineup.go`, AC3)

```json
[ { "GuideNumber": "5", "GuideName": "Kids", "URL": "{BaseURL}/auto/v5" }, ... ]
```

Only enabled, token-permitted channels (D6). `GuideNumber` == channel
`number`; `GuideName` == channel `name`; the same `number`/slug joins to
the XMLTV guide ([27.4](plan-27-04-epg-generation.md)).
`lineup_status.json` returns `{"ScanInProgress":0,"ScanPossible":1,...}`;
`lineup.post?scan=start` 200s immediately (D nothing to scan).

## 4. Tuner stream (`tuner.go` + `leases.go`, D2/D5)

```go
func (h *HDHR) Auto(w http.ResponseWriter, r *http.Request) {
    ch := resolveChannel(chiURLParam(r, "channel"))      // by number
    if !h.token.Allows(r, ch) { http.Error(w, "", 403); return }   // D6
    lease, err := h.leases.Acquire(ch, r.RemoteAddr)     // D5 — cap = TunerCount
    if err == ErrAllTunersInUse {
        writeHDHRBusy(w); return                         // Story AC6
    }
    defer lease.Release()                                 // → engine consumer stop → reaper
    w.Header().Set("Content-Type", "video/mp2t")
    h.engine.ServeMPEGTS(r.Context(), ch, time.Now(), w) // D2 — join + look-ahead, -f mpegts
}
```

`engine.ServeMPEGTS` reuses `join.Locate` + `concat` from
[27.3](plan-27-03-live-stream-engine.md), but muxes `-f mpegts` straight
to the `http.ResponseWriter`, maintaining PID/PCR continuity across
concat boundaries (Story EC5). Plex's probe-then-reconnect pattern
(Story EC3) is absorbed by the engine's warm grace window.

## 5. Data model — migration slot 0084

`shared/db/migrations/0084_hdhomerun.sql` (+ `.sqlite.sql`):

```sql
-- +goose Up
-- +goose StatementBegin
-- Slot 0084 (Epic 27 / Story 27.5) — emulated HDHomeRun device + tuner leases.
CREATE TABLE IF NOT EXISTS hdhr_device (
    id            INTEGER     PRIMARY KEY DEFAULT 1 CHECK (id = 1),  -- singleton
    device_id     TEXT        NOT NULL,                  -- stable, generated once (D3)
    device_uuid   UUID        NOT NULL DEFAULT gen_random_uuid(),    -- UPnP UDN
    friendly_name TEXT        NOT NULL DEFAULT 'Maktaba',
    tuner_count   INTEGER     NOT NULL DEFAULT 4,        -- validated ≤ host cap (D8)
    enabled       BOOLEAN     NOT NULL DEFAULT false,    -- opt-in (D7)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hdhr_tuner_leases (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id  UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    client_addr TEXT        NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS hdhr_tuner_leases_active_idx ON hdhr_tuner_leases (last_seen);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS hdhr_tuner_leases;
DROP TABLE IF EXISTS hdhr_device;
-- +goose StatementEnd
```

`.sqlite.sql` per convention. Register slot 0084 in `MANIFEST.md`.

## 6. HTTP contract (streaming binary, not /api)

```
udp/1900            SSDP M-SEARCH responder (when enabled)
GET  /device.xml            UPnP description
GET  /discover.json         device info (D3/D4)
GET  /lineup.json           enabled, token-scoped channels (D6)
GET  /lineup_status.json    idle status
POST /lineup.post?scan=start  no-op ack
GET  /auto/v{channel}       continuous MPEG-TS (leased, D2/D5)
```

## 7. Files to create / modify

**Create:** everything under `streaming/internal/hdhr/`, the migration
pair.

**Modify:**
- `streaming/main.go` / server wiring — mount HDHR routes + start SSDP
  goroutine when `hdhr_device.enabled`.
- `streaming/internal/channel/mpegts.go` — `ServeMPEGTS` (the TS mux of
  the engine, [27.3](plan-27-03-live-stream-engine.md) D9).
- Config (`streaming.toml`) — `TunerCount`, `FriendlyName`, enable flag;
  validate `TunerCount ≤ cap` (D8).
- `shared/db/migrations/MANIFEST.md` — register slot 0084.

## 8. Dependencies

- **27.1** (`channels`), **27.3** (channel engine + MPEG-TS output),
  **27.4** (XMLTV guide pairing — soft; the tuner works without it but
  Plex wants both), Epic 8/25 scoped token.

## 9. Test strategy

`test_ssdp_responds_to_msearch` (simulated multicast), `discover.json`
shape + stable id across restart, lineup scoping by token, `/auto`
content-type + wall-clock join (fake clock + fake ffmpeg.Runner), lease
cap + release, disabled-feature silence. Per-app compatibility documented
and smoke-tested against Plex/Jellyfin/Emby in QA (Story EC7).

## 10. Notes on deployment

SSDP multicast does not cross Docker bridge networks — document the
host-network or SSDP-reflector requirement (Story EC6) in `deploy/`.
