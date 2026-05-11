# Implementation Plan — Story 25.7 WSS relay tunnel: server side

> Companion to [story-25-07-relay-tunnel-server.md](story-25-07-relay-tunnel-server.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Where it lives | A **new** binary inside the existing local-server `api/` Go module: `api/cmd/maktaba-cloudlink/`. Lives under `api/go.mod` so it can `import "api/internal/auth/keys"` for sealing (plan-10-14) and `import "api/internal/auth/serverkeys"` for the Ed25519 identity (plan-10-18). Separate systemd unit / compose service so a panic doesn't take down the local API. **No top-level `go.mod`** exists in this repo; do not assume one. |
| WSS client lib | `github.com/coder/websocket` (modern fork of nhooyr/websocket; well-maintained; supports compression off). |
| Wire codec | Length-prefixed binary frames over WSS binary messages. One frame per WS message (canonical encoding). |
| Concurrency model | One owner goroutine for the WS conn (read loop). One writer goroutine drained by a buffered channel (256 frames). One goroutine per active stream proxying to the local HTTP listener. |
| Local upstream | Loopback HTTPS to local API (`https://127.0.0.1:8080`) using a bundled CA. Strictly `127.0.0.1`; reject 0.0.0.0 misconfiguration. |
| Reconnect | Exponential backoff 1s → 60s with full jitter; same `server_token` across reconnects. |
| Heartbeat | App-level PING/PONG every 25s; 10s deadline. |
| Out of scope | The cloud-side accept loop (25.8). Direct LAN probe / META_ENDPOINTS reporting (25.10 handles the LAN payload; cloudlink just sends it). |

## 1. Process layout

Paths are relative to the repository root. All files live under the
existing `api/` Go module (no new module).

```
api/cmd/maktaba-cloudlink/
  main.go                # flags, config, sd_notify, supervisor loop
api/internal/cloudlink/
  conn.go                # connect/handshake/reconnect
  frame.go               # frame codec (length, type, payload)
  multiplex.go           # stream_id → channel mapping
  proxy.go               # cloud→local-API bridge
  health.go              # admin endpoint exporter (HTTP on 127.0.0.1:8090)
  entitlement.go         # cache write on ENT_REFRESH (25.26)
  meta.go                # META_ENDPOINTS reporter (25.10 hook)
  storage.go             # encrypted-at-rest token + entitlement
  claim.go               # post {token_hash, server_pubkey} to /api/servers/claim/init (25.6)
```

The cloudlink binary imports:

- `api/internal/auth/keys` for `SealedBox` (plan-10-14 secret loader),
- `api/internal/auth/serverkeys` for the long-lived Ed25519 server
  identity (plan-10-18).

Because everything lives under one module, no `replace` directives or
shared module is needed; standard `internal/` visibility applies.

The local API exposes a Unix socket `/run/maktaba/cloudlink.sock` for inter-process calls (push event ingest, entitlement read). Cloudlink subscribes here, never the other way around.

## 2. Frame codec

Wire format (per architecture §13.4):

```
+----------------+--------+-----------+
| 4B len (BE)    | 1B type| payload   |
+----------------+--------+-----------+
len = bytes counted across type+payload; max 1 MiB per frame.
```

Types (constants in `frame.go`):

```go
const (
    FrameReqHead       FrameType = 0x01
    FrameReqBody                  = 0x02
    FrameReqEnd                   = 0x03
    FrameRespHead                 = 0x04
    FrameRespBody                 = 0x05
    FrameRespEnd                  = 0x06
    FramePing                     = 0x10
    FramePong                     = 0x11
    FrameWindowUpdate             = 0x12
    FrameRevoke                   = 0x20
    FrameEntRefresh               = 0x21
    FrameWSHead                   = 0x30   // client-side WS upgrade pass-through
    FrameMetaEndpoints            = 0x40   // server → cloud
)
```

REQ_HEAD payload (CBOR for compactness + bounded parsing):

```go
type ReqHead struct {
    StreamID  uint32
    Method    string
    Path      string                  // path+query
    Headers   [][2]string             // hop-by-hop already filtered
    ClientIP  string                  // assigned by cloud
    OriginalHost string
}
```

RESP_HEAD payload:

```go
type RespHead struct {
    StreamID uint32
    Status   int
    Headers  [][2]string
}
```

Bodies and ends carry `{stream_id, [chunk]}`. WINDOW_UPDATE: `{stream_id, bytes_added}`.

`frame.Encode(w io.Writer, t FrameType, payload []byte) error`. `Decode(r io.Reader) (FrameType, []byte, error)` reads exactly one frame; enforces `len ≤ 1 MiB`.

## 3. Connection lifecycle

```go
// internal/cloudlink/conn.go
type Conn struct {
    cfg          *Config
    token        string          // bearer; encrypted at rest via secrets.Sealed
    ws           *websocket.Conn
    writes       chan frame      // buffered 256
    mux          *Multiplexer
    state        atomic.Pointer[State]
    backoff      backoff.Strategy
    clock        clock.Clock
    health       *Health
}

func (c *Conn) Run(ctx context.Context) {
    for ctx.Err() == nil {
        if err := c.connectOnce(ctx); err != nil {
            c.state.Store(&State{Phase: "reconnecting", LastErr: err.Error()})
        }
        if revoked.Load() { return }                 // token revoked → stop trying
        time.Sleep(c.backoff.Next())                 // 1..60s full-jitter
    }
}

func (c *Conn) connectOnce(ctx context.Context) error {
    hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()
    opts := &websocket.DialOptions{
        HTTPHeader: http.Header{
            "Authorization": {"Bearer " + c.token},
            "User-Agent":    {fmt.Sprintf("maktaba-cloudlink/%s", version.Version)},
        },
        CompressionMode: websocket.CompressionDisabled,
    }
    ws, resp, err := websocket.Dial(hctx, c.cfg.CloudEndpoint, opts)
    if err != nil {
        if resp != nil && resp.StatusCode == 401 {
            c.attemptRevocation(resp)
        }
        return err
    }
    c.ws = ws
    c.health.OnHandshake(c.clock.Now())
    c.backoff.Reset()
    return c.loop(ctx)
}
```

After 3 consecutive 401s on reconnect, the cloudlink stops trying and surfaces "Cloud connection revoked — re-claim to reconnect" via the health endpoint and (in 25.16-side push) sends a local notification.

### 3.1 Read loop

```go
func (c *Conn) loop(ctx context.Context) error {
    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error { return c.readPump(gctx) })
    g.Go(func() error { return c.writePump(gctx) })
    g.Go(func() error { return c.pinger(gctx) })
    return g.Wait()
}

func (c *Conn) readPump(ctx context.Context) error {
    for {
        if ctx.Err() != nil { return ctx.Err() }
        _, payload, err := c.ws.Read(ctx)
        if err != nil { return err }
        t, body, err := frame.DecodePayload(payload)
        if err != nil {
            c.ws.Close(websocket.StatusCode(4001), "protocol_error")
            return err
        }
        c.lastFrame.Store(c.clock.Now().UnixNano())
        switch t {
        case FramePing:
            c.writes <- frame.Make(FramePong, nil)
        case FramePong:
            c.lastPong.Store(c.clock.Now().UnixNano())
        case FrameRevoke:
            revoked.Store(true)
            c.handleRevoke(ctx)
            return errRevoked
        case FrameEntRefresh:
            c.handleEntRefresh(ctx, body)
        case FrameReqHead, FrameReqBody, FrameReqEnd, FrameWindowUpdate, FrameWSHead:
            c.mux.Deliver(t, body)
        default:
            // unknown → ignore (forward-compat)
        }
    }
}
```

### 3.2 Writer & backpressure

```go
func (c *Conn) writePump(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done(): return ctx.Err()
        case f := <-c.writes:
            if err := c.ws.Write(ctx, websocket.MessageBinary, f.Bytes()); err != nil { return err }
        }
    }
}
```

Per-stream window: `Stream.SendWindow int64` (initial 64 KiB). Outbound goroutine `recv := <- localAPI.Body; if window < len(recv) { wait for WINDOW_UPDATE }; window -= n; emit REQ_BODY/RESP_BODY`. Window resets on stream open.

### 3.3 Heartbeat

```go
func (c *Conn) pinger(ctx context.Context) error {
    t := time.NewTicker(25 * time.Second); defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return ctx.Err()
        case <-t.C:
            c.writes <- frame.Make(FramePing, nil)
            // 10s dead-man's switch:
            time.AfterFunc(10*time.Second, func() {
                if time.Since(time.Unix(0, c.lastPong.Load())) > 10*time.Second {
                    c.ws.Close(websocket.StatusCode(4002), "pong_timeout")
                }
            })
        }
    }
}
```

Uses **monotonic clock** (via `clock.Now()` injection): timeouts must not be fooled by wall-clock jumps.

## 4. Stream multiplexer

```go
type Multiplexer struct {
    mu       sync.Mutex
    streams  map[uint32]*Stream
    proxy    *Proxy
}

type Stream struct {
    ID         uint32
    bodyIn     chan []byte    // REQ_BODY chunks
    endIn      chan struct{}
    sendWindow int64
    cancel     context.CancelFunc
}

func (m *Multiplexer) Deliver(t FrameType, body []byte) {
    switch t {
    case FrameReqHead:
        var h ReqHead; cbor.Unmarshal(body, &h)
        s := newStream(h.StreamID)
        m.add(s)
        go m.proxy.Run(s, &h)  // spawns a goroutine per request — bounded by total streams
    case FrameReqBody:
        s := m.get(streamID(body)); if s == nil { return }
        s.bodyIn <- bodyChunk(body)
    case FrameReqEnd:
        s := m.get(streamID(body)); if s == nil { return }
        close(s.bodyIn); close(s.endIn)
    case FrameWindowUpdate:
        s := m.get(streamID(body)); if s == nil { return }
        atomic.AddInt64(&s.sendWindow, windowDelta(body))
        s.notifyWindow()
    case FrameWSHead:
        // WebSocket pass-through: spawn pass-through goroutine
        m.proxy.RunWS(streamID(body), parseWSHead(body))
    }
}
```

Disconnect → `m.streams` iterated; every `cancel()` called; goroutines exit. New `tunnel_session_id` resets the map.

## 5. Proxy to local API

```go
func (p *Proxy) Run(s *Stream, h *ReqHead) {
    ctx, cancel := context.WithCancel(s.ctx)
    s.cancel = cancel
    req, _ := http.NewRequestWithContext(ctx, h.Method, p.localBaseURL+h.Path, nil)
    for _, kv := range h.Headers {
        if !isHopByHop(kv[0]) && !isFiltered(kv[0]) { req.Header.Add(kv[0], kv[1]) }
    }
    // Strip and re-add IP headers per story spec:
    req.Header.Del("X-Forwarded-For")
    req.Header.Del("Cf-Connecting-Ip")
    req.Header.Del("X-Real-Ip")
    req.Header.Set("X-Maktaba-Original-Ip", h.ClientIP)
    req.Header.Set("X-Maktaba-Original-Host", h.OriginalHost)
    // Service-account auth (Epic 10) — privileged trusted call:
    req.Header.Set("Authorization", "Bearer "+p.serviceToken)
    if cloudUser := extractCloudUser(h.Headers); cloudUser != "" {
        req.Header.Set("X-Maktaba-Cloud-User", cloudUser)
    }
    // Body: pipe from s.bodyIn into the request body.
    pr, pw := io.Pipe()
    req.Body = pr
    go func() {
        defer pw.Close()
        for chunk := range s.bodyIn { _, _ = pw.Write(chunk) }
    }()
    resp, err := p.http.Do(req)
    if err != nil {
        // 502 to cloud
        p.emit(s.ID, &RespHead{Status: 502}); p.emitEnd(s.ID); return
    }
    defer resp.Body.Close()
    p.emit(s.ID, &RespHead{Status: resp.StatusCode, Headers: filteredHeaders(resp.Header)})
    buf := make([]byte, 32*1024)
    for {
        n, rerr := resp.Body.Read(buf)
        if n > 0 {
            // Wait on send window
            for atomic.LoadInt64(&s.sendWindow) < int64(n) { s.waitWindow(ctx) }
            atomic.AddInt64(&s.sendWindow, -int64(n))
            p.emitBody(s.ID, buf[:n])
        }
        if rerr != nil { break }
    }
    p.emitEnd(s.ID)
}
```

The `http.Client` has `Timeout: 5 * time.Minute` for the headers wait; body streaming respects context cancellation.

Loopback CA: `p.http.Transport.TLSClientConfig.RootCAs = bundledLocalCA` so cloudlink only trusts the locally-signed Caddy cert; rejects any other certs (story EC).

## 6. Storage

`internal/cloudlink/storage.go`:

```go
type Storage struct{ dataKey []byte; path string }

func (s *Storage) SaveToken(token string) error {
    sealed := secrets.Seal(s.dataKey, []byte(token))
    return atomic.WriteFile(s.path+"/cloud-token.sealed", sealed, 0600)
}

func (s *Storage) LoadToken() (string, error) { ... }

func (s *Storage) SaveEntitlement(jws []byte) error { ... }
```

Uses `secrets.SealedBox` from Epic 10.14 (data key locally derived from machine key).

## 7. Health surface

`GET /admin/cloud-link` (served on local API, populated via Unix socket query):

```json
{
  "connected": true,
  "last_handshake_at": "...",
  "last_pong_at": "...",
  "tunnel_session_id": "...",
  "streams_in_flight": 0,
  "bytes_in_24h": 0,
  "bytes_out_24h": 0,
  "last_error": "",
  "reconnect_attempts_since_success": 0
}
```

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestFrameRoundTrip` | Random frame types + payloads round-trip byte-identical. |
| `TestFrameMaxSize1MiB` | 1 MiB + 1 byte → decode error. |
| `TestBackoffWithJitter` | 5 drops produce sequence within ±10% of 1/2/4/8/16s. |
| `TestPingerCancelsOnPongTimeout` | No PONG within 10s → conn closed 4002. |
| `TestRevokeAlwaysStops` | After REVOKE, no further reconnect attempts. |
| `TestLoopbackOnly127` | Configuring `localBaseURL=https://0.0.0.0:8080` → refused at init. |

### 8.2 Integration (mock cloud + httptest local API)

| Test | Pins |
|---|---|
| `TestProxy100GETs` | 100 successful round-trips; no drops. |
| `TestProxyError502` | Local API returns 500 → propagated cleanly. |
| `TestLocalAPIDown` | Local API closed → 502 frame; tunnel remains. |
| `TestProxy1GiBStreamRSSBound` | 1 GiB body; RSS sample < 64 MiB. |
| `TestDropWSMidStream` | WS closed mid-response → goroutines cleaned via context cancel. |
| `TestRestartCloudReconnectsWithin60s` | Mock cloud restart → reconnect 1..60s. |
| `TestServerTokenRevokedAfter3` | 3 consecutive 401s → cloudlink stops trying. |
| `TestMalformedFrame4001` | Cloud emits bad frame → conn closed 4001; metric inc. |
| `TestHeaderSanitization` | Inbound `X-Forwarded-For` stripped; `X-Maktaba-Original-Ip` re-added. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Captive-portal WSS | TLS handshake fails on `*.maktaba.app`; "captive portal detected" surface. | Doc. |
| Corp MITM proxy | Local trust store honored; banner shown. | Doc. |
| Frame fragmentation | Single frame per WS message; reader handles "partial message" only via WS lib. | Codec. |
| Goroutine leak on rapid reconnect | All stream goroutines hung off `parentCtx`; cancel fans out. | `TestDropWSMidStream`. |
| Local API unauthenticated loopback | Service-account header from Epic 10. | `TestServiceToken`. |
| WS-level PING vs our PING | We disable RFC 6455 ping; do app-level only. | Conn config. |
| Single-tunnel-per-server | Cloud handles dedup; this side is unaware (just reconnects on close). | Cross-test with 25.8. |
| Loopback non-127 | Reject at config-validate. | `TestLoopbackOnly127`. |
| Outbound HTTP_PROXY | Honored only for `CONNECT`; raw bytes after upgrade. | Doc. |
| Long-running scan + tunnel drop | Local job state survives; client retries. | Cross-test with Epic 06. |
| Concurrent REQ_HEAD with same stream_id | Reject second + emit 4001 protocol_error. | Defensive check in mux. |
| WindowUpdate when stream closed | Drop silently. | Mux defensive. |

## 10. Dependencies

- 25.1 (config patterns).
- 25.6 (server claim → bearer token + cloud_endpoint URL).
- 25.8 (the cloud-side; this story spec asserts what the cloud should answer, but the cloudlink should pass tests against a mock cloud while 25.8 is parallel-developed).
- 25.26 (entitlement signing — cloudlink consumes ENT_REFRESH frames).
- Epic 10 Story 10.14 (secrets sealing for token-at-rest).
- Epic 10 Story 10.18 (server identity Ed25519).

## 11. Acceptance checklist

- [ ] `cmd/maktaba-cloudlink` separate binary, separate systemd unit.
- [ ] Frame codec round-trips; max 1 MiB.
- [ ] WSS connect with bearer; reconnect with backoff.
- [ ] PING/PONG 25s/10s on monotonic clock.
- [ ] REVOKE stops reconnect, deletes token, surfaces admin notice.
- [ ] Concurrent streams handled in parallel; no HOL.
- [ ] 1 GiB stream uses <64 MiB RSS.
- [ ] Headers sanitized + re-added per spec.
- [ ] Health endpoint exposes connection telemetry.
- [ ] Tests in §8 pass.
