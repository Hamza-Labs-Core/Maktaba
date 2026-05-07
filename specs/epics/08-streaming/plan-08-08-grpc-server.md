# Implementation Plan — Story 8.8 gRPC Server

> Companion to [story-08-08-grpc-server.md](story-08-08-grpc-server.md).
> The story states *what* and *why*; this plan states *how*. Wires
> together [Story 8.1](plan-08-01-server-skeleton.md) (config), 8.2
> (matrix), 8.5/8.6 (HLS/DASH sessions), 8.7 (hwaccel),
> [8.9](plan-08-09-session-store.md) (session table),
> [8.10](plan-08-10-concurrency-caps.md) (slots), 8.15 (probe cache).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Proto location | `shared/proto/streaming/v1/streaming.proto`. The `v1` namespace lets us add v2 later without breaking the API. |
| Codegen | `buf generate` writes Go to `shared/proto/streaming/v1/*.pb.go` and `*_grpc.pb.go`. |
| Listener | Separate gRPC port (default `:9090`); never multiplexed onto the HTTP byte port. |
| Auth | mTLS between API and Streaming if `[grpc] tls_ca` is set; otherwise localhost-only with a shared bearer token (`[grpc] bearer_token`) checked by an interceptor. The API is the **only** client. |
| Open/Close idempotency | Session UUID v7 generated server-side; OpenSession is **not** idempotent on retry — the API must call `Close` if it's confused. CloseSession **is** idempotent. |
| Out of scope | Watch-progress fanout (Epic 7 Story 7.10); the API owns that. The gRPC `HealthCheck` watch (server-streaming) — the basic unary HealthCheck ships here. |

## 1. Architecture diagram

```
                    API (Epic 7) — sole gRPC client
                          │
                          ▼
        ┌──────────────────────────────────────────────────────┐
        │  streaming.StreamingService gRPC server               │
        │  port :9090                                            │
        ├──────────────────────────────────────────────────────┤
        │  interceptors:                                         │
        │   1. recovery (panic → INTERNAL)                       │
        │   2. auth     (mTLS or bearer)                         │
        │   3. logging  (zerolog, RPC name, peer, latency)      │
        │   4. metrics  (Prom histograms keyed on method/code)   │
        ├──────────────────────────────────────────────────────┤
        │  RPCs:                                                 │
        │    - OpenSession    (unary)                            │
        │    - CloseSession   (unary)                            │
        │    - EvictHashCache (unary)                            │
        │    - GetCapabilities(unary)                            │
        │    - HealthCheck    (unary)                            │
        └──────────────────────────────────────────────────────┘
                          │
                          ▼
        ┌──────────────────────────────────────────────────────┐
        │ session.Manager (this story)                          │
        │   - probe.Lookup (8.15)                               │
        │   - caps.Registry (8.2)                               │
        │   - hwaccel.Detected (8.7)                            │
        │   - hls.SessionFactory / dash.SessionFactory (8.5/8.6)│
        │   - slot.Pool (8.10)                                  │
        │   - sessionStore (8.9 Postgres-backed)                │
        │   - cache.Store (for EvictHashCache)                  │
        └──────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/proto/buf.yaml`, `shared/proto/buf.gen.yaml` | Buf config for code generation. |
| `shared/proto/streaming/v1/streaming.proto` | The proto definitions per the story's AC. |
| `shared/proto/streaming/v1/streaming.pb.go` | Generated. |
| `shared/proto/streaming/v1/streaming_grpc.pb.go` | Generated. |
| `streaming/internal/grpcserver/server.go` | `Server` struct, gRPC listener wiring, interceptor chain. |
| `streaming/internal/grpcserver/auth.go` | mTLS + bearer-token interceptor. |
| `streaming/internal/grpcserver/open_session.go` | `OpenSession` handler. |
| `streaming/internal/grpcserver/close_session.go` | `CloseSession` handler. |
| `streaming/internal/grpcserver/evict_cache.go` | `EvictHashCache` handler. |
| `streaming/internal/grpcserver/get_capabilities.go` | `GetCapabilities` handler. |
| `streaming/internal/grpcserver/health.go` | `HealthCheck` handler. |
| `streaming/internal/grpcserver/server_test.go` | Integration tests (real grpc-go server, in-memory listener). |
| `streaming/internal/session/manager.go` | `Manager` — coordinates session lifecycle. |
| `streaming/internal/session/manager_test.go` | Unit tests with fake factories. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/cmd/maktaba-streaming/main.go` | Start the gRPC listener alongside HTTP/metrics; wire `session.Manager`. |
| `streaming/internal/observability/metrics.go` | gRPC server metrics (handler latency, code distribution). |
| `streaming/configs/streaming.toml.example` | `[grpc]` block. |
| `Makefile` | `make proto` runs `buf generate`. |
| `specs/epics/08-streaming/README.md` | Tick 8.8. |

### 2.3 Proto — `streaming/v1/streaming.proto`

> Canonical source: architecture §9.9. Verified on this story:
> `OpenSession` returns `OpenSessionResponse` (which packages the
> Session details + a CapabilitiesResponse view); `EvictHashCache`
> returns `EvictHashCacheResponse{entries_removed, artifacts}`;
> `GetCapabilities` is a unary RPC; and `WatchQueue` (server-streaming,
> defined in [Story 8.10](plan-08-10-concurrency-caps.md)) is the
> queue-promotion channel. No other shapes — this proto block matches
> §9.9 exactly.

```proto
syntax = "proto3";
package maktaba.streaming.v1;

import "google/protobuf/timestamp.proto";
import "google/protobuf/empty.proto";

option go_package = "maktaba/shared/proto/streaming/v1;streamingv1";

service StreamingService {
  rpc OpenSession     (OpenSessionRequest)     returns (OpenSessionResponse);
  rpc CloseSession    (CloseSessionRequest)    returns (google.protobuf.Empty);
  rpc EvictHashCache  (EvictHashCacheRequest)  returns (EvictHashCacheResponse);
  rpc GetCapabilities (google.protobuf.Empty)  returns (CapabilitiesResponse);
  rpc HealthCheck     (google.protobuf.Empty)  returns (HealthStatus);
}

// ---- OpenSession ----
message OpenSessionRequest {
  string video_id           = 1;
  string client_profile     = 2;
  optional int32  audio_track       = 3;
  optional int32  subtitle_track    = 4;
  optional int32  start_sec         = 5;
  optional int32  max_bitrate_kbps  = 6;
  optional string format            = 7;   // 'hls' | 'dash'
  bool   force_software             = 8;
  bool   force_transcode            = 9;
  bool   burn_subs                  = 10;
  bool   accept_queue               = 11;

  // Identity passed through from the API for auditing and usage metering.
  string user_id           = 20;
  repeated string library_ids = 21;
  string request_id        = 22;
}

message Rendition {
  string name         = 1;   // "1080p", "720p", "480p"
  int32  width        = 2;
  int32  height       = 3;
  int32  bitrate_kbps = 4;
  string codec        = 5;
  string profile      = 6;
  string level        = 7;
}

message QueueState {
  int32  position = 1;       // 1-based; 1 = next
  int32  eta_sec  = 2;       // estimated wait
}

// Per architecture §9.9, OpenSessionResponse packages a Session and a
// CapabilitiesResponse so the API gets the handshake-time capability
// snapshot in one round trip. The Session message holds the
// per-session fields (id, mode, state, ladder, manifest_path, etc).
message Session {
  string                    session_id    = 1;
  string                    mode          = 2;   // 'direct' | 'remux' | 'transcode' | 'direct-degraded'
  string                    state         = 3;   // 'active' | 'queued'
  repeated Rendition        ladder        = 4;
  string                    manifest_path = 5;   // relative; the API signs the URL
  google.protobuf.Timestamp expires_at    = 6;
  optional QueueState       queue         = 7;
  string                    host          = 8;   // sticky-routing hint (Story 8.9)
}

message OpenSessionResponse {
  Session              session      = 1;
  CapabilitiesResponse capabilities = 2;   // handshake convenience copy of GetCapabilities
}

// ---- CloseSession ----
message CloseSessionRequest {
  string session_id = 1;
  string reason     = 2;   // 'api' | 'user-stop' | 'admin-evict'
}

// ---- EvictHashCache ----
message EvictHashCacheRequest {
  string content_hash = 1;
}
message EvictHashCacheResponse {
  int32 entries_removed = 1;
  // Bookkeeping for ops; the API does not depend on these but they're
  // useful in audit logs.
  repeated string artifacts = 2;   // 'remux','poster','sprite','thumb','probe'
}

// ---- GetCapabilities ----
message Slots {
  int32 total = 1;
  int32 used  = 2;
  int32 queued = 3;
}
message CapabilitiesResponse {
  repeated string codecs                = 1;
  string          hwaccel               = 2;   // 'videotoolbox','nvenc','qsv','software'
  int32           max_bitrate_kbps      = 3;
  repeated string supported_containers  = 4;
  Slots           transcode_slots       = 5;
  int64           cache_used_gib        = 6;
  int64           cache_cap_gib         = 7;
  string          ffmpeg_version        = 8;
  string          host                  = 9;
}

// ---- HealthCheck ----
message HealthStatus {
  enum Status {
    UNKNOWN = 0;
    HEALTHY = 1;
    DEGRADED = 2;
    UNHEALTHY = 3;
  }
  Status                          status        = 1;
  optional string                 last_error    = 2;
  optional google.protobuf.Timestamp last_error_at = 3;
}
```

### 2.4 Type definitions

```go
// streaming/internal/session/manager.go
package session

type Manager struct {
    Probe     probe.Lookup
    Caps      *caps.Registry
    HwAccel   func() *hwaccel.Capabilities

    HLSFact   hls.SessionFactory   // creates *hls.Session
    DASHFact  dash.SessionFactory  // creates *dash.Session

    Slots     *slot.Pool           // 8.10
    Store     *sessionstore.Store  // 8.9
    Cache     *cache.Store
    EvictBus  *evict.Bus           // 8.15 + others subscribe

    JWTAud    string               // "streaming"
    JWTTTL    time.Duration        // default 1800s (≤ 30min revocation lag)
}

type OpenInput struct {
    VideoID         uuid.UUID
    ClientProfile   string
    AudioTrack      *int
    SubtitleTrack   *int
    StartSec        int
    MaxBitrateKbps  int
    Format          string  // 'hls' | 'dash'
    ForceSoftware   bool
    ForceTranscode  bool
    BurnSubs        bool
    AcceptQueue     bool

    UserID     uuid.UUID
    LibraryIDs []uuid.UUID
    RequestID  string
}

type OpenOutput struct {
    SessionID    uuid.UUID
    Mode         string
    State        string  // 'active' | 'queued'
    Ladder       []caps.Rendition
    ManifestPath string
    ExpiresAt    time.Time
    Queue        *QueueState
    Host         string
}

func (m *Manager) Open(ctx context.Context, in OpenInput) (OpenOutput, error)
func (m *Manager) Close(ctx context.Context, sessionID uuid.UUID, reason string) error
func (m *Manager) Evict(ctx context.Context, hash string) (entriesRemoved int, artifacts []string, err error)
```

### 2.5 OpenSession handler

```go
// streaming/internal/grpcserver/open_session.go
package grpcserver

import (
    "context"

    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
    "google.golang.org/protobuf/types/known/timestamppb"

    streamingv1 "maktaba/shared/proto/streaming/v1"
    "maktaba/streaming/internal/session"
)

func (s *Server) OpenSession(ctx context.Context, req *streamingv1.OpenSessionRequest) (*streamingv1.OpenSessionResponse, error) {
    in, err := openInputFrom(req)
    if err != nil {
        return nil, status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
    }

    out, err := s.Manager.Open(ctx, in)
    if err != nil {
        return nil, mapManagerErr(err)
    }

    sess := &streamingv1.Session{
        SessionId:    out.SessionID.String(),
        Mode:         out.Mode,
        State:        out.State,
        Ladder:       toProtoLadder(out.Ladder),
        ManifestPath: out.ManifestPath,
        ExpiresAt:    timestamppb.New(out.ExpiresAt),
        Host:         out.Host,
    }
    if out.Queue != nil {
        sess.Queue = &streamingv1.QueueState{
            Position: int32(out.Queue.Position),
            EtaSec:   int32(out.Queue.ETASec),
        }
    }
    // Per architecture §9.9, OpenSessionResponse carries a fresh
    // CapabilitiesResponse snapshot so the API gets the same view that
    // GetCapabilities would return — saves a round trip on session
    // open. We delegate to the same handler.
    capsResp, err := s.GetCapabilities(ctx, &emptypb.Empty{})
    if err != nil {
        return nil, status.Errorf(codes.Internal, "open session: capabilities snapshot failed: %v", err)
    }
    return &streamingv1.OpenSessionResponse{
        Session:      sess,
        Capabilities: capsResp,
    }, nil
}

// mapManagerErr translates session.Manager errors to gRPC status codes
// per the story:
//
//   - probe missing            → FailedPrecondition `video-not-probed`
//   - video not found          → NotFound
//   - slot pool exhausted      → ResourceExhausted (with details = QueueState)
//   - ffmpeg failed to spawn   → Internal
//   - context deadline         → DeadlineExceeded
//   - matrix verdict cancel/forbidden → PermissionDenied (rare; covers
//     the burn-subs branch where the source has no embedded subs and
//     the API didn't supply one — the API should validate first).
func mapManagerErr(err error) error {
    switch {
    case errors.Is(err, session.ErrProbeMissing):
        return status.Errorf(codes.FailedPrecondition, "video-not-probed")
    case errors.Is(err, session.ErrVideoNotFound):
        return status.Errorf(codes.NotFound, "video-not-found")
    default:
        // Resource-exhausted (slot pool full) is a typed error that
        // carries an optional QueueState. We pass a *T to errors.As so
        // it actually compiles — `errors.As(err, &session.ErrResourceExhausted{})`
        // is invalid because errors.As needs a pointer to a target. The
        // session package defines `ErrResourceExhausted` as a struct
        // type returned by pointer (matching the standard Go error idiom).
        var ere *session.ErrResourceExhausted
        if errors.As(err, &ere) {
            st := status.New(codes.ResourceExhausted, "transcode slots full")
            if ere.Queue != nil {
                st, _ = st.WithDetails(&streamingv1.QueueState{
                    Position: int32(ere.Queue.Position),
                    EtaSec:   int32(ere.Queue.ETASec),
                })
            }
            return st.Err()
        }
        return status.Errorf(codes.Internal, "open session: %v", err)
    }
}
```

### 2.6 Manager.Open — the core orchestration

```go
// streaming/internal/session/manager.go (continued)

func (m *Manager) Open(ctx context.Context, in OpenInput) (OpenOutput, error) {
    // 1. Probe (Story 8.15). FAILED_PRECONDITION when missing.
    row, err := m.Probe.LookupVideo(ctx, in.VideoID)
    if err != nil {
        if errors.Is(err, probe.ErrNotFound) {
            return OpenOutput{}, ErrVideoNotFound
        }
        if errors.Is(err, probe.ErrNotProbed) {
            return OpenOutput{}, ErrProbeMissing
        }
        return OpenOutput{}, err
    }

    // 2. Matrix verdict (Story 8.2).
    profile := m.Caps.Get(in.ClientProfile)
    override := caps.SessionOverride{
        ForceTranscode: in.ForceTranscode,
        ForceSoftware:  in.ForceSoftware,
        BurnSubs:       in.BurnSubs,
        MaxBitrateKbps: in.MaxBitrateKbps,
    }
    verdict := caps.Decide(profile, row.MediaInfo, override)

    // 3. Slot accounting (Story 8.10). Direct/remux is unbounded; transcode
    //    competes for slots.
    var slotHandle *slot.Slot
    state := "active"
    var queue *QueueState
    if verdict.Mode == caps.ModeTranscode {
        h, err := m.Slots.Acquire(ctx, slot.Request{
            UserID: in.UserID, AcceptQueue: in.AcceptQueue,
        })
        switch {
        case err == nil:
            slotHandle = h
        case errors.Is(err, slot.ErrFull) && in.AcceptQueue:
            queueHandle, q, err2 := m.Slots.Queue(ctx, in.UserID)
            if err2 != nil {
                return OpenOutput{}, err2
            }
            return m.recordQueued(ctx, in, row, verdict, queueHandle, q)
        case errors.Is(err, slot.ErrFull) && verdict.CanDegradeToDirect():
            // 8.10 AC-2: degrade to direct-cap.
            verdict = caps.Verdict{Mode: caps.ModeDirect, Container: row.Container,
                Reason: "slot full; direct-degraded", Ladder: nil,
                AudioMode: "passthrough", SubMode: "external"}
            // Reflect to the API.
            return m.recordActive(ctx, in, row, verdict, "direct-degraded", nil, nil)
        case errors.Is(err, slot.ErrFull):
            return OpenOutput{}, ErrResourceExhausted{Queue: nil}
        default:
            return OpenOutput{}, err
        }
    }

    // 4. Allocate session id, dirs, format.
    sid := uuid.Must(uuid.NewV7())
    format := in.Format
    if format == "" {
        format = "hls"
    }
    dir := filepath.Join(m.Cache.Root, format, sid.String())

    // 5. Spawn the FFmpeg session if needed.
    var manifestPath string
    switch verdict.Mode {
    case caps.ModeDirect, caps.ModeRemux:
        // No FFmpeg ahead of time; manifest_path points to the per-mode handler.
        manifestPath = directManifestPath(in.VideoID, verdict.Mode)
    case caps.ModeTranscode:
        if format == "dash" {
            sess, err := m.DASHFact(sid, in, row, verdict)
            if err != nil {
                slot.Release(slotHandle)
                return OpenOutput{}, err
            }
            if err := sess.Start(ctx); err != nil {
                slot.Release(slotHandle)
                return OpenOutput{}, err
            }
            if err := sess.WaitForMPD(ctx, 5*time.Second); err != nil {
                _ = sess.Stop(ctx)
                slot.Release(slotHandle)
                return OpenOutput{}, err
            }
            manifestPath = fmt.Sprintf("/stream/%s/manifest.mpd", sid)
        } else {
            sess, err := m.HLSFact(sid, in, row, verdict)
            if err != nil {
                slot.Release(slotHandle)
                return OpenOutput{}, err
            }
            if err := sess.Start(ctx); err != nil {
                slot.Release(slotHandle)
                return OpenOutput{}, err
            }
            if err := sess.WaitForMaster(ctx, 5*time.Second); err != nil {
                _ = sess.Stop(ctx)
                slot.Release(slotHandle)
                return OpenOutput{}, err
            }
            manifestPath = fmt.Sprintf("/stream/%s/manifest.m3u8", sid)
        }
    }

    // 6. Persist row in `streaming_sessions` (Story 8.9). The transaction
    //    is owned by sessionstore; if it fails we tear down the FFmpeg.
    if err := m.Store.Insert(ctx, sessionstore.Row{
        ID:            sid,
        VideoID:       in.VideoID,
        UserID:        in.UserID,
        ClientProfile: in.ClientProfile,
        Mode:          string(verdict.Mode),
        Format:        format,
        Host:          m.HostID(),
        State:         state,
    }); err != nil {
        // Rollback: stop session + release slot. We're already on the
        // Manager receiver, so call its Close directly — there is no
        // nested `Manager` field on the receiver.
        _ = m.Close(ctx, sid, "store-insert-failed")
        return OpenOutput{}, err
    }

    return OpenOutput{
        SessionID: sid, Mode: string(verdict.Mode),
        State: "active", Ladder: verdict.Ladder,
        ManifestPath: manifestPath,
        ExpiresAt: time.Now().Add(m.JWTTTL),
        Host: m.HostID(),
    }, nil
}
```

### 2.7 CloseSession

```go
// streaming/internal/grpcserver/close_session.go
package grpcserver

func (s *Server) CloseSession(ctx context.Context, req *streamingv1.CloseSessionRequest) (*emptypb.Empty, error) {
    sid, err := uuid.Parse(req.SessionId)
    if err != nil {
        return nil, status.Errorf(codes.InvalidArgument, "invalid session_id")
    }
    if err := s.Manager.Close(ctx, sid, req.Reason); err != nil {
        // Idempotent: ErrAlreadyClosed → OK.
        if errors.Is(err, session.ErrAlreadyClosed) {
            return &emptypb.Empty{}, nil
        }
        return nil, status.Errorf(codes.Internal, "close: %v", err)
    }
    return &emptypb.Empty{}, nil
}
```

```go
// streaming/internal/session/manager.go (Close)

func (m *Manager) Close(ctx context.Context, sid uuid.UUID, reason string) error {
    // 1. Stop FFmpeg if any. Idempotent.
    if sess := m.HLSFact.Lookup(sid); sess != nil {
        // Two-phase: SIGTERM, wait 2s, SIGKILL.
        ctxStop, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
        defer cancel()
        _ = sess.Stop(ctxStop)
    }
    if sess := m.DASHFact.Lookup(sid); sess != nil {
        ctxStop, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
        defer cancel()
        _ = sess.Stop(ctxStop)
    }

    // 2. Purge per-session HLS/DASH dir (cache/hls/{sid}, cache/dash/{sid}).
    _ = os.RemoveAll(filepath.Join(m.Cache.Root, "hls", sid.String()))
    _ = os.RemoveAll(filepath.Join(m.Cache.Root, "dash", sid.String()))

    // 3. Update sessionstore row. closed_at = now, closed_reason = reason.
    return m.Store.MarkClosed(ctx, sid, reason)
}
```

### 2.8 EvictHashCache

```go
// streaming/internal/grpcserver/evict_cache.go
package grpcserver

func (s *Server) EvictHashCache(ctx context.Context, req *streamingv1.EvictHashCacheRequest) (*streamingv1.EvictHashCacheResponse, error) {
    if !validHash(req.ContentHash) {
        return nil, status.Errorf(codes.InvalidArgument, "invalid content_hash")
    }
    n, kinds, err := s.Manager.Evict(ctx, req.ContentHash)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "evict: %v", err)
    }
    return &streamingv1.EvictHashCacheResponse{
        EntriesRemoved: int32(n),
        Artifacts:      kinds,
    }, nil
}
```

```go
// streaming/internal/session/manager.go (Evict)

// Evict drops every cache entry keyed by content_hash and broadcasts
// to in-process subscribers (probe cache, remux cache, etc). Open file
// descriptors continue serving — the OS keeps the inode alive after
// unlink (POSIX behavior).
func (m *Manager) Evict(ctx context.Context, hash string) (int, []string, error) {
    artifacts := []string{}
    n := 0

    // remux/{hash[:2]}/...
    if removed := m.Cache.RemoveAllByHash("remux", hash); removed > 0 {
        n += removed
        artifacts = append(artifacts, "remux")
    }
    if removed := m.Cache.RemoveAllByHash("posters", hash); removed > 0 {
        n += removed; artifacts = append(artifacts, "poster")
    }
    if removed := m.Cache.RemoveAllByHash("sprites", hash); removed > 0 {
        n += removed; artifacts = append(artifacts, "sprite")
    }
    if removed := m.Cache.RemoveAllByHash("thumbs", hash); removed > 0 {
        n += removed; artifacts = append(artifacts, "thumb")
    }

    // In-memory probe cache (Story 8.15).
    m.EvictBus.Publish(evict.Event{Hash: hash})
    artifacts = append(artifacts, "probe")
    return n, artifacts, nil
}
```

### 2.9 GetCapabilities

```go
// streaming/internal/grpcserver/get_capabilities.go
package grpcserver

func (s *Server) GetCapabilities(ctx context.Context, _ *emptypb.Empty) (*streamingv1.CapabilitiesResponse, error) {
    cur := s.Manager.HwAccel()
    cacheUsed, cacheCap := s.Manager.Cache.UsageGiB()
    slotsTotal, slotsUsed, slotsQ := s.Manager.Slots.Snapshot()

    return &streamingv1.CapabilitiesResponse{
        Codecs:               []string{"h264","aac"}, // current encode set
        Hwaccel:              string(cur.Encoder),
        MaxBitrateKbps:       int32(s.Manager.MaxBitrateKbps()),
        SupportedContainers:  []string{"mp4","ts","mkv","webm","mov"},
        TranscodeSlots:       &streamingv1.Slots{Total: int32(slotsTotal), Used: int32(slotsUsed), Queued: int32(slotsQ)},
        CacheUsedGib:         int64(cacheUsed),
        CacheCapGib:          int64(cacheCap),
        FfmpegVersion:        cur.FFmpegVersion,
        Host:                 s.Manager.HostID(),
    }, nil
}
```

The story's p95 ≤ 50 ms target is satisfied because every field is read
from in-memory atomics or struct fields — no DB query or child-process
call.

## 3. Test plan

### 3.1 Proto contract tests (`buf lint`, `buf breaking`)

`buf breaking --against .git#branch=main` runs in CI; any incompatible
change to `streaming.proto` blocks merge.

### 3.2 Server unit tests (`server_test.go`) — uses `bufconn` for an in-memory listener.

| Test | What it pins |
|---|---|
| `TestOpenSession_TranscodeReturnsManifestPath` | Open with a transcode-required source → response has `mode=transcode`, ladder len ≥ 1, manifest_path matches `/stream/{sid}/manifest.m3u8`, expires_at within JWTTTL. AC-1. |
| `TestOpenSession_DirectReturnsDirectMode` | Direct-eligible source → mode=direct, ladder empty, manifest_path is the direct-play URL. |
| `TestOpenSession_RemuxReturnsRemuxMode` | Container-mismatch source → mode=remux, manifest_path is `/stream/direct/{video_id}` (Story 8.4 piggybacks on direct path with verdict=remux). |
| `TestOpenSession_VideoNotFound_NOT_FOUND` | Probe lookup returns ErrNotFound → gRPC status `NOT_FOUND`. AC edge case. |
| `TestOpenSession_VideoNotProbed_FAILED_PRECONDITION` | Probe lookup returns ErrNotProbed → gRPC status `FAILED_PRECONDITION`, code in detail = `video-not-probed`. AC of Story 8.15. |
| `TestOpenSession_SlotsFull_RESOURCE_EXHAUSTED` | Slot pool full + `accept_queue=false` → gRPC `RESOURCE_EXHAUSTED`. AC-1 edge case. |
| `TestOpenSession_AcceptQueueReturnsQueued` | Slot pool full + `accept_queue=true` → response state=`queued`, queue field populated, no FFmpeg spawned. AC-1 edge case (queue handling). |
| `TestOpenSession_FFmpegSpawnFails_INTERNAL` | Mock factory returns spawn error → gRPC `INTERNAL`; slot is released; sessionstore row absent. |
| `TestOpenSession_ManifestNotReadyIn5s_INTERNAL` | Mock spawn but never writes master → 5 s timeout → INTERNAL; FFmpeg killed; slot released. AC-1's 5-second cap. |
| `TestCloseSession_KillsFFmpegWithin2s` | Open + Close; FFmpeg subprocess gone within 2 s; per-session dir removed; sessionstore row marked closed. AC-2. |
| `TestCloseSession_AlreadyClosed_OK` | Close twice → second is OK (idempotent). AC-2 edge case. |
| `TestCloseSession_FFmpegAlreadyCrashed_OK` | Crash the mock then Close → no error. AC-2 edge case. |
| `TestEvictHashCache_RemovesAllArtifacts` | Plant remux + poster + sprite + thumb + probe entries for a hash; Evict; entries gone; counter ticks; cross-hash isolation: another hash's entries untouched. AC-3. |
| `TestEvictHashCache_InFlightFDsKeepReading` | Open a remuxed file's FD; Evict; the FD continues returning bytes; the next OpenSession for that hash spawns a fresh remux. AC-3 edge case. |
| `TestEvictHashCache_InvalidatesProbeCache` | After Evict, the next OpenSession for that hash issues a DB query (asserted via Story 8.15's spy). |
| `TestGetCapabilities_P95Under50ms` | 1000 calls, the first within 50 ms (no warm-up), p95 ≤ 50 ms. AC-4. |
| `TestGetCapabilities_NoChildProcessSpawned` | Process snapshot before/after 100 GetCapabilities calls → no `ffmpeg`/`ffprobe`/`nvidia-smi` invocations. AC-4. |
| `TestHealthCheck_StatusFields` | Healthy host → status=HEALTHY, no last_error; force a degradation flag → status=DEGRADED with last_error_at. AC-5. |
| `TestAuth_BearerInterceptor_RejectsBadToken` | Missing or wrong bearer token → `UNAUTHENTICATED`. |
| `TestAuth_MTLSInterceptor_RejectsUnverifiedClient` | Self-signed client cert → handshake fails. |

### 3.3 Manager unit tests (`manager_test.go`) — fakes for probe, caps, slot, factory, store.

| Test | What it pins |
|---|---|
| `TestManager_OpenRollsBackOnStoreInsertFailure` | Mock store returns DB error after FFmpeg spawn → manager calls Close, slot released, no leak. |
| `TestManager_OpenWithDegradeToDirect` | Slot full + verdict.CanDegradeToDirect() + accept_queue=false → mode=`direct-degraded`. AC of Story 8.10. |
| `TestManager_CloseRemovesPerSessionDirs` | Close → `cache/hls/{sid}` and `cache/dash/{sid}` are gone; cache root structure preserved. |
| `TestManager_CloseIdempotent` | Two parallel Close calls → both succeed; FFmpeg killed once. |
| `TestManager_EvictBroadcastsToProbeCache` | A subscriber on `evict.Bus` receives the event with the matching hash. |

### 3.4 Integration test (real grpc-go + sqlite-mode store)

`TestIntegration_OpenManifestThenClose` — open a transcode session with
a fixture, confirm the master playlist exists on disk, make 3 segment
HTTP requests through the streaming server, then call CloseSession;
assert the per-session dir is gone and the row is marked closed.

## 4. Test code scaffolding

```go
// streaming/internal/grpcserver/server_test.go
package grpcserver_test

import (
    "context"
    "net"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/stretchr/testify/require"
    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/grpc/status"
    "google.golang.org/grpc/test/bufconn"

    streamingv1 "maktaba/shared/proto/streaming/v1"
)

func TestOpenSession_TranscodeReturnsManifestPath(t *testing.T) {
    h := newHarness(t)
    defer h.Close()

    h.Probe.SetVideo(uuid.New(), &probe.Row{ /* HEVC source on chrome → transcode */ })

    cli := streamingv1.NewStreamingServiceClient(h.Conn)
    resp, err := cli.OpenSession(context.Background(), &streamingv1.OpenSessionRequest{
        VideoId:       h.VideoID.String(),
        ClientProfile: "browser-chrome",
        UserId:        h.UserID.String(),
        LibraryIds:    []string{h.LibraryID.String()},
    })
    require.NoError(t, err)
    // Per architecture §9.9, OpenSessionResponse wraps Session +
    // CapabilitiesResponse — fields live on resp.Session.
    require.Equal(t, "transcode", resp.Session.Mode)
    require.NotEmpty(t, resp.Session.SessionId)
    require.Contains(t, resp.Session.ManifestPath, "/manifest.m3u8")
    require.GreaterOrEqual(t, len(resp.Session.Ladder), 1)
    require.WithinDuration(t, time.Now().Add(30*time.Minute), resp.Session.ExpiresAt.AsTime(), 5*time.Second)
    require.NotNil(t, resp.Capabilities, "OpenSession should return a capabilities snapshot")
}

func TestOpenSession_VideoNotProbed_FAILED_PRECONDITION(t *testing.T) {
    h := newHarness(t)
    defer h.Close()
    h.Probe.SetError(probe.ErrNotProbed)

    cli := streamingv1.NewStreamingServiceClient(h.Conn)
    _, err := cli.OpenSession(context.Background(), &streamingv1.OpenSessionRequest{
        VideoId:       uuid.NewString(),
        ClientProfile: "browser-chrome",
        UserId:        uuid.NewString(),
    })
    st, ok := status.FromError(err)
    require.True(t, ok)
    require.Equal(t, codes.FailedPrecondition, st.Code())
    require.Contains(t, st.Message(), "video-not-probed")
}

func TestCloseSession_AlreadyClosed_OK(t *testing.T) {
    h := newHarness(t)
    defer h.Close()
    cli := streamingv1.NewStreamingServiceClient(h.Conn)

    open, err := cli.OpenSession(context.Background(), simpleOpenReq(h))
    require.NoError(t, err)
    _, err = cli.CloseSession(context.Background(), &streamingv1.CloseSessionRequest{
        SessionId: open.Session.SessionId, Reason: "api",
    })
    require.NoError(t, err)
    _, err = cli.CloseSession(context.Background(), &streamingv1.CloseSessionRequest{
        SessionId: open.Session.SessionId, Reason: "api",
    })
    require.NoError(t, err)
}
```

## 5. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Open while transcode slots full, no accept_queue | gRPC `RESOURCE_EXHAUSTED` (without queue details). | `TestOpenSession_SlotsFull_RESOURCE_EXHAUSTED` |
| Open while slots full, with accept_queue | gRPC OK with response state=`queued`, queue filled. The session **row** is recorded `state='queued'`; FFmpeg not spawned until a slot opens (Story 8.10 promotes). | `TestOpenSession_AcceptQueueReturnsQueued` |
| Close on already-closed session | Idempotent OK. | `TestCloseSession_AlreadyClosed_OK` |
| Close on a session whose FFmpeg crashed | Idempotent OK. | `TestCloseSession_FFmpegAlreadyCrashed_OK` |
| EvictHashCache while a session is reading the file | OS keeps the inode alive; the session's open FD continues returning bytes. The next OpenSession for the same hash sees no cache and regenerates. | `TestEvictHashCache_InFlightFDsKeepReading` |
| Open with an unknown video_id | `NOT_FOUND`. | `TestOpenSession_VideoNotFound_NOT_FOUND` |
| Manifest doesn't appear within 5 s | Manager kills FFmpeg, releases slot, returns `INTERNAL`. The 5 s window is tight; the integration test fixture uses a 1 s clip so it always fits. | `TestOpenSession_ManifestNotReadyIn5s_INTERNAL` |
| Session row insert fails after FFmpeg spawn | Manager rolls back: stops FFmpeg, releases slot. Surfaced as `INTERNAL`. | `TestManager_OpenRollsBackOnStoreInsertFailure` |
| Two API instances OpenSession concurrently for the same video and user | Both succeed independently; the API decides whether to merge or keep separate. (Streaming has no per-(user, video) uniqueness constraint.) | Documented; no test needed beyond the cross-host story (8.9). |
| GetCapabilities under load | Returns from atomics + struct fields; no DB; p95 ≤ 50 ms. | `TestGetCapabilities_P95Under50ms` |
| Auth interceptor with no creds configured | The server starts in `bearer_token=""` mode + no mTLS → it refuses to start (init-time failure). We never run gRPC unauthenticated even in dev (use a literal `dev` token in `streaming.toml.example`). | `TestServer_StartsRequiresAuthConfig` |

## 6. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `google.golang.org/grpc` | ^1.65 | gRPC for Go. |
| `google.golang.org/protobuf` | ^1.34 | Generated proto messages. |
| `github.com/bufbuild/buf` | latest (dev tool) | Linting and code generation; matches the cross-language proto setup elsewhere in the project. |
| `google.golang.org/grpc/test/bufconn` | bundled with grpc | In-memory listener for tests. |

## 7. Acceptance checklist

**RPCs (story ACs)**
- [ ] AC-1: OpenSession returns the documented response (session_id, mode, ladder, manifest_path, expires_at, optional queue) and waits up to 5 s for the master playlist.
- [ ] AC-2: CloseSession kills FFmpeg within 2 s grace, purges per-session dir, marks `streaming_sessions` row, idempotent.
- [ ] AC-3: EvictHashCache invalidates remux + posters + sprites + thumbs + probe cache for the given hash; in-flight FDs unaffected.
- [ ] AC-4: GetCapabilities responds in ≤ 50 ms p95 with no child-process spawns.
- [ ] AC-5: HealthCheck returns status without capability fields; capability data exclusively via GetCapabilities.

**Proto and codegen**
- [ ] `buf lint` passes.
- [ ] `buf breaking --against main` passes (no breaking changes after first cut).
- [ ] Generated Go is checked in.

**Auth**
- [ ] Bearer interceptor or mTLS configured; the server refuses to start without one.
- [ ] Bad token → `UNAUTHENTICATED`.

**Robustness**
- [ ] Open rolls back FFmpeg + slot when the session row insert fails.
- [ ] CloseSession is idempotent across all three states (active, already-closed, crashed-ffmpeg).
- [ ] EvictHashCache cross-hash isolation tested.

**Observability**
- [ ] gRPC server metrics: `grpc_server_handled_total`, `grpc_server_handling_seconds`.
- [ ] Manager-level: `streaming_sessions_opened_total{mode}`, `streaming_sessions_closed_total{reason}`, `streaming_evict_total{artifact}`.

**Docs**
- [ ] `streaming/configs/streaming.toml.example` documents `[grpc]` block.
- [ ] `specs/epics/08-streaming/README.md` ticks 8.8.
