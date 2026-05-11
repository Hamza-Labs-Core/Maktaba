# Implementation Plan — Story 25.10 Direct-connection probe & LAN fallback

> Companion to [story-25-10-direct-connection-fallback.md](story-25-10-direct-connection-fallback.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Cloud surface | `GET /api/servers/{server_id}/endpoints` — read-only; lists LAN candidates + relay URL + `preferred`. |
| Server → cloud reporting | `0x40 META_ENDPOINTS` frame: server pushes LAN candidates at tunnel handshake and on local network-change. Cloud stores them encrypted in `cloud_server_endpoints`. |
| Client probe | Each platform implements the same algorithm: race 1s probes; pin winner 5min; invalidate on connectivity change. |
| Storage | LAN IPs sealed at rest (AES-GCM with cloud data key); never returned to anyone but the owning user. |
| Out of scope | WebRTC / STUN hole punching (v2). Custom user-supplied direct hostnames beyond manual override (v1.1). |

## 1. Migration `00020002_server_endpoints.sql`

```sql
-- +goose Up
CREATE TABLE cloud_server_endpoints (
    server_id     UUID PRIMARY KEY REFERENCES cloud_servers(id) ON DELETE CASCADE,
    candidates_sealed BYTEA NOT NULL,           -- AES-GCM-sealed JSON
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    sources_count INT NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE IF EXISTS cloud_server_endpoints;
```

JSON shape inside `candidates_sealed`:

```json
[
  {"url":"http://192.168.1.42:8080","source":"mdns","reported_at":"..."},
  {"url":"http://10.0.0.5:8080","source":"user-set","reported_at":"..."}
]
```

## 2. Server → cloud (cloudlink)

In `internal/cloudlink/meta.go`:

```go
func (c *Conn) PostMeta(ctx context.Context, endpoints []Endpoint) {
    payload, _ := cbor.Marshal(struct{ Endpoints []Endpoint }{endpoints})
    c.writes <- frame.Make(FrameMetaEndpoints, payload)
}

// Watcher: subscribes to mDNS publisher (Epic 15.2) + config file for user-set
// IPs; debounced 2s; emits on every change.
```

On handshake, the cloudlink emits an initial META_ENDPOINTS with whatever it knows. On change events, re-emit.

## 3. Cloud-side ingest

In `cloud/internal/relay/tunnel.go` (extends 25.8):

```go
func (t *Tunnel) handleMeta(ctx context.Context, body []byte) {
    var meta struct{ Endpoints []Endpoint }
    if err := cbor.Unmarshal(body, &meta); err != nil { return }
    // Filter: keep only RFC1918 + link-local IPv6 + localhost.
    var keep []Endpoint
    for _, e := range meta.Endpoints {
        if validLANCandidate(e.URL) {
            keep = append(keep, e)
        }
    }
    sealed, _ := seal.Marshal(t.dataKey, keep)
    _, _ = t.repo.UpsertServerEndpoints(ctx, t.ServerID, sealed, len(keep))
}

func validLANCandidate(rawURL string) bool {
    u, err := url.Parse(rawURL)
    if err != nil { return false }
    host, _, _ := net.SplitHostPort(u.Host)
    ip := net.ParseIP(host)
    if ip == nil { return false }
    if ip.IsLoopback() { return true }
    if isRFC1918(ip) { return true }
    if isLinkLocalV6(ip) { return true }
    return false
}
```

We **never** store globally-routable IPs we receive in META — those are either misconfiguration or a hostile server trying to make the cloud announce arbitrary endpoints.

## 4. Endpoints API

```go
// GET /api/servers/{server_id}/endpoints
func endpoints(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        sid, _ := uuid.Parse(chi.URLParam(r, "server_id"))
        userID := currentUserID(r)
        srv, err := s.repo.GetServer(r.Context(), sid)
        if err != nil { problem(w, 404, "not_found", ""); return }
        if srv.UserID != userID { problem(w, 403, "not_your_server", ""); return }
        sealed, _ := s.repo.GetEndpoints(r.Context(), sid)
        var cands []Endpoint
        if sealed != nil { seal.Unmarshal(s.dataKey, sealed, &cands) }
        relay := fmt.Sprintf("https://%s.maktaba.app", srv.Subdomain)
        preferred := "lan"
        if len(cands) == 0 { preferred = "relay" }
        writeJSON(w, 200, map[string]any{
            "lan":      cands,
            "relay":    relay,
            "preferred": preferred,
        })
    }
}
```

Cross-user access → `403 not_your_server`; in story EC the abuse log records `lan_probe_id_mismatch` for client-side mismatches (see §6).

## 5. Client probe algorithm (shared across all platforms)

```ts
// web/src/lib/server-endpoint.ts
type Endpoints = { lan: Candidate[]; relay: string; preferred: "lan"|"relay" };

export async function resolveBaseURL(serverID: string): Promise<string> {
    const cached = pinCache.get(serverID);
    if (cached && Date.now() - cached.at < 5*60*1000) return cached.url;
    const eps: Endpoints = await fetch(`${CLOUD}/api/servers/${serverID}/endpoints`).then(r=>r.json());
    if (eps.preferred === "relay" || eps.lan.length === 0) {
        pinCache.set(serverID, {url: eps.relay, at: Date.now()});
        return eps.relay;
    }
    const winner = await raceProbes(eps.lan, serverID);
    if (winner) {
        pinCache.set(serverID, {url: winner, at: Date.now()});
        return winner;
    }
    pinCache.set(serverID, {url: eps.relay, at: Date.now()});
    return eps.relay;
}

async function raceProbes(cands: Candidate[], expectedServerID: string): Promise<string|null> {
    const controller = new AbortController();
    const probes = cands.map(c => probe(c.url, expectedServerID, controller.signal));
    const winner = await Promise.race([
        Promise.any(probes),
        sleep(1000).then(() => null),
    ]);
    controller.abort();
    return winner;
}

async function probe(base: string, expectID: string, sig: AbortSignal): Promise<string> {
    const resp = await fetch(`${base}/api/health`, { signal: sig, mode: "cors", redirect: "manual" });
    if (resp.status !== 200) throw new Error("not ok");
    const id = resp.headers.get("X-Maktaba-Server-Id");
    if (id && id !== expectID) {
        navigator.sendBeacon(`${CLOUD}/api/abuse/report`, JSON.stringify({kind:"lan_probe_id_mismatch", expect: expectID, got: id, url: base}));
        throw new Error("id mismatch");
    }
    return base;
}
```

The cache is invalidated on `online` / `offline` events:

```ts
window.addEventListener("online", () => pinCache.clear());
window.addEventListener("offline", () => pinCache.clear());
```

Stream affinity: once a session is established with a `base`, the player wrapper stores the base on the playback context; it does not switch mid-stream.

### 5.1 Per-platform plugins

- iOS / Android (Capacitor): `mobile/plugins/maktaba-net/` — uses `Reachability` / `ConnectivityManager` for the equivalent of `online`/`offline`.
- Desktop (Tauri): `apps/desktop/src-tauri/src/net.rs` — `tokio::net::lookup_host` for candidate validation; `notify-rust` for state.
- TV iOS: `apps/tv/tvos/Sources/Maktaba/Services/NetService.swift`; Android TV: equivalent Kotlin.

All four implement the same JSON contract and call the cloud `/endpoints` API.

## 6. Abuse signal: ID mismatch

When a LAN probe returns 200 but its `X-Maktaba-Server-Id` differs from the expected one (e.g., DNS rebinding to attacker), the client fire-and-forgets `POST /api/abuse/report` (lightweight; rate-limited per IP+user) so the cloud can append `cloud_abuse_events kind=lan_probe_id_mismatch`. This story stubs the report endpoint; 25.25 owns full abuse pipeline.

## 7. Test plan

### 7.1 Unit (cloud side)

| Test | Pins |
|---|---|
| `TestValidLANCandidate` | RFC1918, loopback, IPv6 link-local accepted; public IPs rejected. |
| `TestEndpointsResponseSealed` | Stored bytes don't contain plain IP string. |
| `TestEndpointsForbidsOtherUser` | 403 not_your_server. |
| `TestPreferredRelayIfNoLAN` | Empty LAN list → `preferred=relay`. |

### 7.2 Integration

| Test | Pins |
|---|---|
| `TestMetaEndpointsRoundTrip` | Server posts META; GET returns the same. |
| `TestUpdateOnNetworkChange` | Server posts new META; old gone, new returned. |

### 7.3 Client (web only, JSDOM)

| Test | Pins |
|---|---|
| `TestProbeRaceLanWinner` | First responder wins; relay not used. |
| `TestProbeRaceTimeoutFallsBackToRelay` | All LAN candidates time out at 1s; relay used. |
| `TestPinTTL5min` | Advance 6 min; re-probe triggered. |
| `TestIDMismatchAbuseReport` | Probe responds with wrong `X-Maktaba-Server-Id` → abuse report posted, candidate rejected. |
| `TestSessionAffinityHLS` | Switching networks mid-stream keeps original base for the session. |

## 8. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Tailscale / VPN | Manual override IP works as a "user-set" candidate. | Doc. |
| CGNAT mobile | Probes fail; relay path used. | `TestProbeRaceTimeoutFallsBackToRelay`. |
| Mixed-content browser block | Doc: HTTPS web client requires Caddy local CA or `localhost` only. | Doc. |
| Network fingerprint (iOS) | Without location permission, always probe. | Doc. |
| Server moves IP | mDNS → new META → next probe picks up. | Cross-story 15.2. |
| Probe blocks app startup | Decide before 1.5s; fallback to relay if undecided. | Client logic. |
| Logging | Probe outcomes log locally only; cloud never sees them. | Spec. |
| Privacy of another user's LAN IPs | 403 from cloud; client cannot enumerate. | `TestEndpointsForbidsOtherUser`. |
| Hostname-based candidate | Require explicit user opt-in (defeats SSRF-via-LAN-hostname). | Mobile plugin doc. |
| Server posts public IP via META | Rejected by `validLANCandidate`; not stored. | `TestValidLANCandidate`. |
| User changes Wi-Fi mid-stream | Session affinity holds; status visible in UI. | `TestSessionAffinityHLS`. |
| TLS LAN cert with local CA | Probe accepts only if OS trusts local CA. | Doc. |

## 9. Dependencies

- 25.1 (foundation).
- 25.7/25.8 (META frame; tunnel handshake).
- 25.5 (`PATCH /api/me` for manual override IP — actually owned by Settings page in 25.16; the API is fine).
- Epic 15 Story 15.2 (LAN mDNS source).

## 10. Acceptance checklist

- [ ] Migration 00020002 applies.
- [ ] `META_ENDPOINTS` ingestion stores sealed bytes; only LAN-class IPs persisted.
- [ ] `GET /api/servers/{id}/endpoints` enforces ownership.
- [ ] `preferred` toggles based on candidate availability.
- [ ] Web client implements race-probe + pin + invalidate.
- [ ] Mobile / Desktop / TV plugins share the same contract.
- [ ] ID-mismatch abuse report flow stubbed.
- [ ] Tests in §7 pass.
