# Implementation Plan — Story 19.3 Streaming Horizontal Scale-Out

> Companion to [story-19-03-streaming-scale-out.md](story-19-03-streaming-scale-out.md).
> Sticky-session by `session_id` consistent hash; failover via clean reopen
> from server-side watch state; `EvictHashCache` fans out via gRPC.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| LB | Any consistent-hash L7 (Caddy with `lb_policy ip_hash`/cookie-based, nginx `hash $arg_session_id consistent`, HAProxy `balance hdr`). |
| Replica registry | `streaming_replicas(id, host, port, advertise_url, last_seen, drain)` table; replicas heartbeat. |
| Session ownership | `streaming_sessions.replica_id` column; `OpenSession` writes the local replica's id. |
| Failover | Client receives `session_invalidated`; reopens; new replica issues fresh manifest URL. |
| Cache eviction | gRPC `EvictHashCache` fans out via replica registry. |

## 1. Project layout

```
streaming/internal/
├── replicas/
│   ├── registry.go              # heartbeat + list
│   ├── grpc_client.go           # talk to peers
│   └── registry_test.go
├── session/
│   ├── pin.go                   # session→replica
│   ├── invalidate.go            # signal client on failover
│   └── pin_test.go
├── cache/
│   └── evict_fanout.go
shared/db/migrations/
└── 00xx_streaming_replicas.sql
```

## 2. Schema

> **Plan-introduced extensions to architecture §8.**
> Neither `streaming_replicas` nor `streaming_sessions.replica_id` is declared
> in architecture §8. They are owned by this plan and listed under the
> "plan-introduced schema" register in arch §8.7 (cross-link). The story
> reviewer should approve before merging.

```sql
-- 00xx_streaming_replicas.sql
-- Plan-introduced (Story 19.3); register in arch §8.7 plan-introduced schema list.
CREATE TABLE streaming_replicas (
    id            UUID PRIMARY KEY,
    host          TEXT NOT NULL,
    grpc_port     INT  NOT NULL,
    advertise_url TEXT NOT NULL,        -- "https://replica-a.maktaba.local:8443"
    drain         BOOLEAN NOT NULL DEFAULT false,
    last_seen     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Plan-introduced column (Story 19.3); see arch §8.7 plan-introduced schema list.
ALTER TABLE streaming_sessions ADD COLUMN replica_id UUID
    REFERENCES streaming_replicas(id) ON DELETE SET NULL;
CREATE INDEX streaming_sessions_replica_id_idx ON streaming_sessions (replica_id);
```

## 3. Replica registry

```go
// replicas/registry.go
type Registry struct{
    db    *sql.DB
    self  uuid.UUID
    advU  string
    grpc  string
}

func (r *Registry) Heartbeat(ctx context.Context) {
    t := time.NewTicker(5 * time.Second)
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            _, _ = r.db.ExecContext(ctx, `
                INSERT INTO streaming_replicas (id, host, grpc_port, advertise_url, last_seen)
                VALUES ($1, $2, $3, $4, now())
                ON CONFLICT (id) DO UPDATE SET last_seen = now(), advertise_url = EXCLUDED.advertise_url
            `, r.self, hostname(), r.grpc, r.advU)
        }
    }
}

func (r *Registry) Live(ctx context.Context) ([]Replica, error) {
    rows, err := r.db.QueryContext(ctx, `
      SELECT id, host, grpc_port, advertise_url FROM streaming_replicas
       WHERE last_seen > now() - interval '15 seconds' AND NOT drain`)
    // ...
}
```

## 4. OpenSession local pin

> **Routing choice.** This plan uses *exactly one* routing mechanism:
> hash-based load balancing at the LB on `?session_id=...` (Caddy
> `lb_policy hash {http.request.uri.query.session_id}`, see §12 and
> arch §10.3). The earlier draft also embedded `ReplicaUrl` in the
> OpenSession response so the client could bypass the LB; that is dropped
> as a routing mechanism. We retain the value as a *debug-only* hint
> surfaced via the `X-Maktaba-Replica` response header on segment requests
> (the replica that actually served), and we no longer return it in
> `OpenSessionResponse`. The client always loads `manifest_url` through the
> LB and trusts the LB's hash to keep the session pinned.

```go
// session/pin.go
func (s *Service) OpenSession(ctx context.Context, in *pb.OpenSessionRequest) (*pb.OpenSessionResponse, error) {
    sid := uuid.New()
    _, err := s.db.ExecContext(ctx, `
       INSERT INTO streaming_sessions (id, user_id, video_id, replica_id, opened_at)
       VALUES ($1, $2, $3, $4, now())`, sid, in.UserId, in.VideoId, s.replicaID)
    if err != nil { return nil, err }
    manifestURL := s.urls.Manifest(in.VideoHash, sid)        // includes session_id query param
    return &pb.OpenSessionResponse{
        SessionId:   sid.String(),
        ManifestUrl: manifestURL,
        // ReplicaUrl REMOVED from response — LB hashes on session_id; the
        // replica id is exposed via X-Maktaba-Replica response header for
        // debugging only.
    }, nil
}

// session/serve.go (segment handler) writes the debug header:
//
//   w.Header().Set("X-Maktaba-Replica", s.replicaID.String())
```

`ManifestUrl` is rewritten through the LB. The query param `?session_id=...` is what the LB consistent-hashes on; clients never address replicas directly.

## 5. Failover detection

```go
// session/invalidate.go
func (h *SegmentHandler) Serve(w http.ResponseWriter, r *http.Request) {
    sid := r.URL.Query().Get("session_id")
    sess, err := h.sessions.Get(r.Context(), sid)
    switch {
    case errors.Is(err, sql.ErrNoRows):
        http.Error(w, "session_invalidated", http.StatusGone); return
    case sess.ReplicaID != h.self:
        // We're not the owner — LB misrouted us due to mid-failover.
        // Tell client to reopen instead of pretending to serve.
        http.Error(w, "session_invalidated", http.StatusGone); return
    }
    h.serve(w, r, sess)
}
```

Client (web player) on `410 Gone` body `session_invalidated`:

```ts
// web/src/player/session.ts
async function onSegment410() {
    const last = video.currentTime;
    const fresh = await api.openSession({ videoId, resumeAt: last });
    player.load(fresh.manifestUrl);
    player.seek(last);
}
```

Watch position is stored server-side in `playback_state` (Epic 9; see arch §8.5 — canonical table is `playback_state`, NOT `watch_state`), so even a hard-killed replica preserves resume.

## 6. EvictHashCache fan-out

```go
// cache/evict_fanout.go
func (s *Service) EvictHashCache(ctx context.Context, in *pb.EvictHashCacheRequest) (*pb.EvictHashCacheResponse, error) {
    // local first
    local, _ := s.segCache.EvictByHash(in.ContentHash)

    if !in.NoFanout {                                        // avoid fan-out loop
        replicas, _ := s.registry.Live(ctx)
        for _, p := range replicas {
            if p.ID == s.replicaID { continue }
            cli := s.peers.For(p)
            _, _ = cli.EvictHashCache(ctx, &pb.EvictHashCacheRequest{
                ContentHash: in.ContentHash, NoFanout: true,
            })
        }
    }
    return &pb.EvictHashCacheResponse{Evicted: local}, nil
}
```

## 7. EC1 — duplicated session_id 409

`OpenSession` schema has `streaming_sessions(id PRIMARY KEY)`. A replay attempting to insert a duplicate fails on PK; service returns `409 Conflict` and the client mints a fresh `session_id`.

## 8. EC2 — replica disk full → drain

```go
// cache/disk_pressure.go
func (s *SegmentCache) Pressure() float64 { /* used / max */ }

// orchestrator
go func() {
    for range time.Tick(10 * time.Second) {
        if cache.Pressure() > 0.98 {
            s.registry.SetDrain(true)
            break
        }
    }
}()
```

LB observes `drain=true` via the registry-backed health endpoint:

```go
// healthz/handler.go
if reg.IsDrain(self) { w.WriteHeader(503); return }
```

Cold-transcode requests from the LB go to other replicas; existing pinned sessions continue (the disk-full replica still serves what it has).

## 9. EC3 — segment timestamp PTS

FFmpeg invocation always uses `-copyts` and segments inherit container PTS. No `-c:v libx264 -muxdelay 0` style wall-clock fudges; this is verified by a probe-test asserting `EXT-X-PROGRAM-DATE-TIME` is absent from media playlists.

## 10. Test cases

### TC1 — Pin
Open 100 distinct sessions across 2 replicas via the LB. For each session, fire 50 segment requests; for every session, every request must hit the same replica (verified via `X-Replica-ID` response header echoed by the replica).

### TC2 — Failover
2 replicas, 50 sessions on A. `kill -SIGKILL` replica A. Clients receive `410 session_invalidated` on next segment, reopen, hit replica B. Resume within 5 s.

> **AC3 strict reading.** AC3 forbids any duplicate cold transcode after
> replica migration. Replicas do **not** share a segment cache, so once A
> dies B will produce its own segments — this is a *controlled re-warm*,
> not a duplicate of work A had already published to B. The test asserts:
> on B, `transcode_started_total{(content_hash, rendition, segment)}` is
> incremented at most once per `(segment, session-on-B)` tuple — i.e. B
> never re-transcodes a segment it already produced. We do **not** attempt
> to dedupe against A's lost cache, and we mark this re-warm explicitly so
> the AC3 reviewer can sign off on it. Any double-increment on B (two
> transcodes for the same `(segment, session-on-B)`) fails the test.

### TC3 — Eviction fan-out
2 replicas, both have content `X` cached. Call `EvictHashCache(X)` against replica A. Within 1 s assert: A's cache loses X; B's cache loses X (queried via admin endpoint).

## 11. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 dup session_id | story | PK conflict → 409 → client mints new id. |
| EC2 disk full | story | Drain mode propagates; LB removes; existing sessions OK. |
| EC3 PTS time | story | `-copyts`, no wall-clock muxing. |
| Session row exists but owning replica is dead | impl | Segment handler 410s; client reopens. |
| Heartbeat missing for self | impl | Replica self-fences after 30 s of failed heartbeats; refuses new opens. |

## 12. Configuration

```yaml
streaming:
  replica_id: ${STREAMING_REPLICA_ID}    # uuid; persisted
  advertise_url: ${STREAMING_ADVERTISE_URL}
  heartbeat_interval: 5s
  registry_ttl: 15s
  drain_on_disk_pct: 98
```

Caddyfile excerpt (operator example):

```caddy
:8443 {
    handle_path /stream/* {
        reverse_proxy {
            to https://replica-a.maktaba.local:8443 https://replica-b.maktaba.local:8443
            lb_policy hash {http.request.uri.query.session_id}
            lb_try_duration 5s
            health_uri /healthz
            health_interval 5s
        }
    }
}
```

## 13. Dependencies

- Epic 7 API server (`OpenSession` gRPC).
- Epic 8 streaming epic (cache, manifest).
- Epic 9 watch state (resume).
- Epic 22 devops (LB config docs).
