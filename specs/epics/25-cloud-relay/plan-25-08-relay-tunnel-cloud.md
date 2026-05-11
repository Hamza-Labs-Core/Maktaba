# Implementation Plan — Story 25.8 WSS relay tunnel: cloud side

> Companion to [story-25-08-relay-tunnel-cloud.md](story-25-08-relay-tunnel-cloud.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Process role | `maktaba-cloud --role=relay`. Binds `:8081` for tunnels (TLS-passthrough at Hetzner LB; relay process speaks `ws://`); binds `:8082` for `/healthz`+`/metrics`. |
| WSS server lib | `github.com/coder/websocket` (same as server side). |
| Registry | In-memory `sync.Map[uuid.UUID]*Tunnel`. Node-local; **no replication**. |
| Auth | `Authorization: Bearer <server_token>`; bcrypt-verified against `servers.token_hash_bcrypt`. Bearer caches `(server_id, user_id)` in pod-local LRU (10k entries, 5min TTL) — cleared on `superseded` close so a rotation propagates fast. |
| One-tunnel-per-server | Newer handshake wins; older closed `4003 superseded` within 1s. |
| Heartbeats | PING from server every 25s; cloud responds PONG via writer. 90s no-frame idle → close `4002 idle_timeout`. |
| Out of scope | The HTTP proxy from clients (25.9). Tunnel-to-pod stickiness via LB consistent hashing (operationally configured; not in code). |

## 1. Listener wiring

```go
// cloud/internal/relay/listener.go
type RelayServer struct {
    reg     *Registry
    repo    *Repo          // bcrypt verify
    metrics *Metrics
    clock   clock.Clock
    upgrader websocket.AcceptOptions
}

func (s *RelayServer) Handler() http.Handler {
    mux := http.NewServeMux()
    mux.HandleFunc("/tunnel/v1/connect", s.handleConnect)
    mux.HandleFunc("/tunnel/v1/heartbeat", s.handleHeartbeat)  // fallback for restricted nets (POST + long-poll)
    return mux
}

func (s *RelayServer) handleConnect(w http.ResponseWriter, r *http.Request) {
    bearer := stripBearer(r.Header.Get("Authorization"))
    if bearer == "" { http.Error(w, "missing token", 401); return }
    serverID, userID, ok := s.repo.VerifyBearer(r.Context(), bearer)
    if !ok {
        s.metrics.Handshakes.WithLabelValues("invalid_token").Inc()
        s.metrics.AbuseIP(r.RemoteAddr)
        http.Error(w, "invalid token", 401); return
    }
    if err := s.upgradeRate.Acquire(r.Context()); err != nil {
        http.Error(w, "throttled", 503); return
    }
    ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
        InsecureSkipVerify: false,
        CompressionMode:    websocket.CompressionDisabled,
        OriginPatterns:     []string{"*"},   // origin not meaningful here
    })
    if err != nil { return }
    s.metrics.Handshakes.WithLabelValues("ok").Inc()
    s.repo.MarkServerSeen(r.Context(), serverID)
    t := newTunnel(serverID, userID, ws, s.clock)
    s.reg.Insert(t)   // closes any superseded older tunnel inside.
    go t.run(r.Context())
}
```

`upgradeRate` is a token-bucket capped at 50 handshakes/s/pod (per story EC).

## 2. Tunnel struct

```go
type Tunnel struct {
    ID         uuid.UUID                 // tunnel_session_id; new each connect
    ServerID   uuid.UUID
    UserID     uuid.UUID
    ws         *websocket.Conn
    writes     chan frame                // buf 256
    streams    sync.Map                  // stream_id → *cloudStream
    streamSeq  atomic.Uint32             // monotonically odd
    lastFrame  atomic.Int64              // unix nano of last inbound frame
    closeOnce  sync.Once
    clock      clock.Clock
}

type cloudStream struct {
    id       uint32
    headCh   chan RespHead
    bodyCh   chan []byte
    endCh    chan struct{}
    sendWindow atomic.Int64
    cancel   context.CancelFunc
}
```

### 2.1 Run

```go
func (t *Tunnel) run(parent context.Context) {
    ctx, cancel := context.WithCancel(parent)
    defer cancel()
    defer t.close("normal_closure")
    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error { return t.readPump(gctx) })
    g.Go(func() error { return t.writePump(gctx) })
    g.Go(func() error { return t.idleWatch(gctx) })
    _ = g.Wait()
}

func (t *Tunnel) idleWatch(ctx context.Context) error {
    tick := time.NewTicker(15 * time.Second); defer tick.Stop()
    for {
        select {
        case <-ctx.Done(): return ctx.Err()
        case <-tick.C:
            last := time.Unix(0, t.lastFrame.Load())
            if t.clock.Since(last) > 90*time.Second {
                t.ws.Close(websocket.StatusCode(4002), "idle_timeout")
                return errIdle
            }
        }
    }
}
```

### 2.2 Per-stream allocation

```go
func (t *Tunnel) NewClientStream(ctx context.Context, head ReqHead, body io.Reader) (*cloudStream, error) {
    id := t.streamSeq.Add(2)        // ensures odd; wraps via Add semantics
    if id == 0 { id = 1 }           // skip 0 reserved
    s := &cloudStream{ id: id, headCh: make(chan RespHead, 1), bodyCh: make(chan []byte, 8), endCh: make(chan struct{}) }
    s.sendWindow.Store(64 * 1024)
    sc, scc := context.WithCancel(ctx); s.cancel = scc
    t.streams.Store(id, s)
    // Send REQ_HEAD
    payload, _ := cbor.Marshal(head)
    if err := t.send(FrameReqHead, payload); err != nil { return nil, err }
    if body != nil { go t.pumpBody(sc, s, body) }
    return s, nil
}
```

Stream-ID rollover: after the counter overflows it wraps; we skip even values and 0; we don't track collision risk explicitly because the 31-bit space + open-streams cap (200/tunnel default) makes collisions vanishingly unlikely. If we detect `t.streams.Load(id) != nil` on insert, retry once with next id; second collision logs `stream_id_collision` (should never happen).

### 2.3 Read pump

```go
func (t *Tunnel) readPump(ctx context.Context) error {
    for {
        _, payload, err := t.ws.Read(ctx)
        if err != nil { return err }
        t.lastFrame.Store(t.clock.Now().UnixNano())
        ft, body, err := frame.DecodePayload(payload)
        if err != nil {
            t.ws.Close(4001, "protocol_error")
            return err
        }
        if len(payload) > 1<<20 { t.ws.Close(4001, "frame_too_large"); return errOversized }
        switch ft {
        case FramePing:
            t.send(FramePong, nil)
        case FramePong:
            // observed; no-op (we don't initiate PINGs cloud-side in v1).
        case FrameRespHead:
            t.deliverHead(body)
        case FrameRespBody:
            t.deliverBody(body)
        case FrameRespEnd:
            t.deliverEnd(body)
        case FrameMetaEndpoints:
            t.handleMeta(ctx, body)        // hands to 25.10 module
        case FrameWindowUpdate:
            t.applyWindow(body)
        default:
            t.metrics.MalformedType.Inc()
        }
    }
}
```

## 3. Registry: supersede + lookup

```go
type Registry struct {
    inner sync.Map  // server_id → *Tunnel
}

func (r *Registry) Insert(t *Tunnel) {
    if prev, ok := r.inner.Swap(t.ServerID, t); ok {
        go prev.(*Tunnel).close("superseded")  // 4003
    }
    r.observers.NotifyConnect(t)
}

func (r *Registry) Get(id uuid.UUID) (*Tunnel, bool) {
    v, ok := r.inner.Load(id)
    if !ok { return nil, false }
    return v.(*Tunnel), true
}

func (r *Registry) Remove(id uuid.UUID, t *Tunnel) {
    r.inner.CompareAndDelete(id, t)
    r.observers.NotifyDisconnect(t)
}
```

`observers` is the bus that the WS `/ws/servers` (25.16) and admin `force-disconnect` (25.20) subscribe to.

## 4. Auth — bcrypt cost and LRU

```go
type BearerCache struct {
    inner *lru.Cache[string, bearerHit]   // key = sha256 of bearer
}

type bearerHit struct{ serverID, userID uuid.UUID; cachedAt time.Time }

func (r *Repo) VerifyBearer(ctx context.Context, bearer string) (uuid.UUID, uuid.UUID, bool) {
    keyHash := sha256.Sum256([]byte(bearer))
    if h, ok := r.cache.Get(hex.EncodeToString(keyHash[:])); ok && time.Since(h.cachedAt) < 5*time.Minute {
        return h.serverID, h.userID, true
    }
    rows, _ := r.db.Query(ctx, `
        SELECT cst.server_id, cs.user_id, cst.token_hash_bcrypt
        FROM servers cst
        JOIN servers cs ON cs.id = cst.server_id
        WHERE cst.revoked_at IS NULL AND cs.deleted_at IS NULL AND cs.suspended_at IS NULL
        LIMIT 5000`)
    defer rows.Close()
    for rows.Next() {
        var sid, uid uuid.UUID; var hash string
        rows.Scan(&sid, &uid, &hash)
        if bcrypt.CompareHashAndPassword([]byte(hash), []byte(bearer)) == nil {
            r.cache.Add(hex.EncodeToString(keyHash[:]), bearerHit{sid, uid, time.Now()})
            r.markUsed(ctx, sid)
            return sid, uid, true
        }
    }
    return uuid.Nil, uuid.Nil, false
}
```

Brute-force defense: 5000 bearer cap is a hard ceiling; well beyond expected count. We pre-filter by table-scan cost; eventually replace with a per-pod cache miss path using a deterministic salted hash index (v2).

Force-revoke: admin (25.20) `UPDATE servers SET revoked_at=now() WHERE server_id=$1`. Registry `Get(serverID)` returns the live tunnel; admin issues `0x20 REVOKE` frame, then closes the connection.

## 5. Heartbeat fallback

`/tunnel/v1/heartbeat` is a POST-based long-poll fallback for networks that block long-lived WSS. Body carries pending frames; server returns frames to send. Not the primary path; documented in 25.7 but not built in v1 (the WSS path covers > 99% of users). Stub the endpoint returning `501 Not Implemented`.

## 6. Metrics

```go
type Metrics struct {
    Open            prometheus.Gauge       // tunnels_open
    Handshakes      *prometheus.CounterVec // result
    Messages        *prometheus.CounterVec // direction, type
    BytesTotal      *prometheus.CounterVec // direction, server_id (separate scrape)
    Reconnects      *prometheus.CounterVec // reason
    StreamsInFlight prometheus.Gauge
    MalformedType   prometheus.Counter
}
```

`tunnel_bytes_total{server_id}` is high-cardinality; expose at a dedicated `/metrics/bytes` endpoint that the bandwidth scraper hits at low frequency.

## 7. Graceful shutdown

On SIGTERM:

1. Stop accepting new tunnels (`/tunnel/v1/connect` → 503).
2. For each tunnel: send `WS close 4000 normal_closure` via writer.
3. Wait `shutdown_grace = 5s` (story EC) for orderly close.
4. Force-close anything still open.

Servers reconnect to whichever pod the LB sends them to.

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestFrameWriterOverflow` | 1000 enqueued frames → drops with `tunnel_write_buffer_full` after 256. |
| `TestStreamIDOdd` | Allocator yields odd values; 0 skipped. |
| `TestStreamIDWrap` | Counter wraps; no collision detected (skipped second-place collision). |
| `TestBcryptCost10` | `bcrypt.GenerateFromPassword(cost=10)` < 50ms on CI hardware. |

### 8.2 Integration

| Test | Pins |
|---|---|
| `TestSecondTunnelSupersedes` | Two valid handshakes for same server → older closes 4003 within 1s. |
| `TestIdleTimeout90s` | No frames for 91s → `4002 idle_timeout`. |
| `TestRestartRebuildsRegistry` | Restart relay → servers reconnect, registry rebuilt. |
| `TestDispatchSpeedP99Under1ms` | After registry has 1k tunnels, dispatching a REQ_HEAD to specific tunnel < 1ms p99 steady state. |
| `TestServerOfflineReturns503` | Client request for offline `server_id` → 503 (depends on 25.9; cross-test). |
| `TestMalformedFrameClosesConn` | Bad type byte → `4001 protocol_error`, audit row. |
| `TestInvalidBearer401` | Bad bearer → 401, IP abuse counter inc. |
| `TestStreamIdRollover` | Force counter to 2^31; next yields 1 (odd). |
| `TestOversizedFrame` | 10 GiB single REQ_BODY → conn closed `4001`; we cap per-frame at 1 MiB. |
| `TestClientDisconnectsMidStream` | Cloud sends RST_STREAM-equivalent → server cancels goroutine (cross-tested with 25.7 fixture). |

### 8.3 Load / regression

| Test | Pins |
|---|---|
| `Load1kTunnelsHoldOpen` | Hold 1000 tunnels idle for 5min; RSS<1MiB/tunnel, CPU<5% steady. |
| `Load200ConcurrentStreams` | 200 streams across 10 tunnels → no HOL. |
| `GracefulShutdownClosesAllWithin5s` | SIGTERM → all tunnels closed 4000; new connects → 503. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Stale registry on graceful shutdown | New handshakes 503; close 4000 to all current. | `GracefulShutdownClosesAllWithin5s`. |
| Slowloris-style abuse | Idle 90s ≥ close 4002. | `TestIdleTimeout90s`. |
| Pod failure | Tunnels disconnect; servers reconnect server-side; no migration. | Doc. |
| LB consistent hashing | Sticky on bearer hash; same `server_id` tends to land same pod. | LB config, not code. |
| WSS upgrade rate | 50/s/pod token bucket; excess → 503. | `TestUpgradeRate`. |
| TLS only at LB | Relay process speaks `ws://`; LB terminates TLS. | Spec. |
| Frame > 1 MiB | Conn closed 4001. | `TestOversizedFrame`. |
| Stream ID collision | Retry once; log if collision repeats (defensive). | Mux defensive. |
| Bearer cache stale on revoke | `UPDATE … SET revoked_at` not enough; admin pathway (25.20) must `cache.Purge(serverID)` after revoke. | Cross-test 25.20. |
| Multi-pod registry sync | Out for v1; documented. | Spec. |
| Unknown frame type | Counted and dropped (forward-compat). | `TestUnknownFrameCounted`. |
| `Connection: close` in upgrade | Accept once, treat as no-op for WS. | Sanitizer. |

## 10. Dependencies

- 25.1 (foundation).
- 25.6 (`servers` table, bearer hash).
- 25.7 (server side; mock used for testing — and vice versa).
- 25.20 (force-disconnect via registry).
- 25.26 (signed ENT_REFRESH frames pushed at handshake).

## 11. Acceptance checklist

- [ ] `--role=relay` binds tunnel listener.
- [ ] Bearer auth via bcrypt; LRU cache with revocation purge.
- [ ] Older tunnel closed 4003 superseded on dup handshake.
- [ ] 90s idle close 4002.
- [ ] Registry exposes `Get(server_id)` to 25.9 proxy.
- [ ] Frame writes bounded; backpressure via WINDOW_UPDATE.
- [ ] Frames > 1 MiB closed 4001.
- [ ] Metrics exposed at `/metrics` (handshakes, open, messages).
- [ ] Tests in §8 pass.
