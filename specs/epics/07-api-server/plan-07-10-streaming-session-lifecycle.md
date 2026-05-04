# Implementation Plan — Story 7.10 Streaming Session Lifecycle

> Companion to [story-07-10-streaming-session-lifecycle.md](story-07-10-streaming-session-lifecycle.md).
> The API mints sessions and signed URLs; Streaming serves bytes (Epic 8).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `POST /api/stream/sessions`, `GET /api/stream/sessions/{id}`, `DELETE /api/stream/sessions/{id}`, `GET /api/stream/capabilities`. |
| Authority | The Streaming Service decides `mode` (direct/remux/transcode) — the API just forwards request, persists the response, mints the URL. |
| URL signer | Epic 10 Story 10.8's `auth.Signer`. JWT claims: `aud=streaming`, `sub=session_id`, `usr=user_id`, `lib=[library_id]`, `exp=iat+ttl`, `iss=api`. |
| Capabilities cache | 60 s in-process; refreshed on `LISTEN profiles_changed` from Streaming. |
| Out of scope | Streaming's transcoder pipeline (Epic 8), the bitrate ladder logic (Epic 8 Story 8.4), client device-profile registry (Streaming-side). |

## 1. Architecture diagram

```
   POST /api/stream/sessions { video_id, client_profile, ... }
        │
        ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ 1. Resolve user → set of authorised library_ids             │
   │ 2. Verify video.library_id ∈ authorised (else 403)          │
   │ 3. streaming.OpenSession(ctx, OpenSessionRequest{           │
   │       video_id, user_id, library_id, client_profile, ...    │
   │    })                                                        │
   │     timeout = config.streaming_open_session_timeout_sec     │
   │ 4. session_id, mode, ladder, current_rendition := response  │
   │ 5. Mint signed URL:                                         │
   │       claims = {aud:"streaming", sub:session_id,            │
   │                 usr:user_id, lib:[library_id],              │
   │                 exp:now+session_url_ttl_sec, iss:"api"}     │
   │       signed = signer.Sign(claims, "/stream/" + session_id  │
   │                                    + "/manifest.m3u8")      │
   │ 6. INSERT INTO streaming_sessions(...)                      │
   │ 7. Return { session_id, mode, manifest_url|direct_url,      │
   │             expires_at, ladder, current_rendition }         │
   └──────────────────────────────────────────────────────────────┘

   GET /api/stream/sessions/{id}
        │
        ▼ DB read; no Streaming round-trip on hot path

   DELETE /api/stream/sessions/{id}
        │
        ▼ streaming.CloseSession(); UPDATE closed_at = now()

   GET /api/stream/capabilities
        │
        ▼ cache.GetOr(60s, streaming.GetCapabilities)
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/streaming/handler.go` | All four routes. |
| `api/internal/streaming/sign.go` | JWT minter (delegates to `auth.Signer`). |
| `api/internal/streaming/cache.go` | 60 s capabilities cache + `LISTEN profiles_changed`. |
| `api/internal/streaming/types.go` | DTOs. |
| `api/internal/streaming/handler_test.go` | Integration. |
| `api/internal/streaming/sign_test.go` | Unit. |
| `api/internal/streaming/cache_test.go` | Unit. |
| `shared/db/queries/streaming_sessions.sql` | sqlc inputs. |
| `shared/db/migrations/0016_streaming_sessions.sql` | Schema. |

## 3. SQL — schema

`shared/db/migrations/0016_streaming_sessions.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE streaming_sessions (
    id                       UUID PRIMARY KEY,
    user_id                  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id                 UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    library_id               UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    client_profile           TEXT,
    mode                     TEXT NOT NULL CHECK (mode IN ('direct','remux','transcode')),
    audio_track              TEXT,
    subtitle_track           TEXT,
    start_sec                REAL NOT NULL DEFAULT 0,
    max_bitrate_kbps         INT,
    ladder                   JSONB NOT NULL,
    current_rendition        TEXT,
    last_segment_fetched_at  TIMESTAMPTZ,
    expires_at               TIMESTAMPTZ NOT NULL,
    opened_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at                TIMESTAMPTZ
);
CREATE INDEX streaming_sessions_user_idx
    ON streaming_sessions (user_id, opened_at DESC)
    WHERE closed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS streaming_sessions;
-- +goose StatementEnd
```

## 4. Type definitions

```go
// api/internal/streaming/types.go
package streaming

import (
    "time"
    "github.com/google/uuid"
)

type Mode string

const (
    ModeDirect    Mode = "direct"
    ModeRemux     Mode = "remux"
    ModeTranscode Mode = "transcode"
)

type OpenRequest struct {
    VideoID         uuid.UUID `json:"video_id"          validate:"required"`
    ClientProfile   string    `json:"client_profile,omitempty"`
    AudioTrack      string    `json:"audio_track,omitempty"`
    SubtitleTrack   string    `json:"subtitle_track,omitempty"`
    StartSec        float64   `json:"start_sec,omitempty"`
    MaxBitrateKbps  int       `json:"max_bitrate_kbps,omitempty"`
    Format          string    `json:"format,omitempty"`
    ForceSoftware   bool      `json:"force_software,omitempty"`
    ForceTranscode  bool      `json:"force_transcode,omitempty"`
    BurnSubs        bool      `json:"burn_subs,omitempty"`
    AcceptQueue     bool      `json:"accept_queue,omitempty"`
}

type OpenResponse struct {
    SessionID        uuid.UUID  `json:"session_id"`
    Mode             Mode       `json:"mode"`
    ManifestURL      string     `json:"manifest_url,omitempty"`
    DirectURL        string     `json:"direct_url,omitempty"`
    ExpiresAt        time.Time  `json:"expires_at"`
    Ladder           []Rung     `json:"ladder"`
    CurrentRendition string     `json:"current_rendition"`
    Warnings         []string   `json:"warnings,omitempty"`
}

type Rung struct {
    Name      string `json:"name"`
    KbpsCap   int    `json:"kbps_cap"`
    Width     int    `json:"width"`
    Height    int    `json:"height"`
    Codec     string `json:"codec"`
}

type SessionDetail struct {
    OpenResponse
    LastSegmentFetchedAt *time.Time `json:"last_segment_fetched_at"`
    ClosedAt             *time.Time `json:"closed_at"`
}

type Capabilities struct {
    Codecs              []string `json:"codecs"`
    HWAccel             string   `json:"hwaccel"`        // "videotoolbox" | "vaapi" | "nvenc" | "none"
    MaxBitrateKbps      int      `json:"max_bitrate_kbps"`
    SupportedContainers []string `json:"supported_containers"`
    TranscodeSlots      *Slots   `json:"transcode_slots,omitempty"`
}

type Slots struct {
    Used     int `json:"used"`
    Capacity int `json:"capacity"`
}
```

## 5. Handler scaffolding

```go
// api/internal/streaming/handler.go
package streaming

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/api/internal/httperror"
)

func (h *handler) open(w http.ResponseWriter, r *http.Request) {
    var in OpenRequest
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    if err := validate(in); err != nil { httperror.Write(w, r, err); return }

    user := userFromCtx(r.Context())
    libID, perr := h.svc.authorize(r.Context(), user, in.VideoID)
    if perr != nil { httperror.Write(w, r, perr); return }

    out, perr := h.svc.openSession(r.Context(), user, libID, in)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(out)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }
    user := userFromCtx(r.Context())
    out, perr := h.svc.getSession(r.Context(), user, id)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(out)
}

func (h *handler) close(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }
    user := userFromCtx(r.Context())
    if perr := h.svc.closeSession(r.Context(), user, id); perr != nil {
        httperror.Write(w, r, perr); return
    }
    w.WriteHeader(http.StatusNoContent)
}

func (h *handler) capabilities(w http.ResponseWriter, r *http.Request) {
    out, err := h.svc.capabilities(r.Context())
    if err != nil {
        httperror.Write(w, r, httperror.Unavailable(5).WithType(TypeStreamingUnavailable))
        return
    }
    json.NewEncoder(w).Encode(out)
}
```

## 6. Service layer

```go
func (s *service) openSession(ctx context.Context, user User, libID uuid.UUID, in OpenRequest) (*OpenResponse, *httperror.Error) {
    // Clamp start_sec to (duration - 5s); add warning if clamped.
    var warnings []string
    if dur, ok := s.lookupDuration(ctx, in.VideoID); ok && in.StartSec > dur-5 {
        in.StartSec = dur - 5
        warnings = append(warnings, "start-sec-clamped")
    }

    grpcCtx, cancel := context.WithTimeout(ctx, s.cfg.OpenTimeout)
    defer cancel()

    // Streaming.OpenSession is canonical (architecture §9.9). It returns
    // *pb.OpenSessionResponse which contains a Session message and the
    // server's current Capabilities snapshot. We only read Session here;
    // the snapshot is forwarded to /capabilities readers via the cache.
    resp, err := s.streaming.OpenSession(grpcCtx, &pb.OpenSessionRequest{
        VideoId: in.VideoID.String(), UserId: user.ID.String(),
        LibraryId: libID.String(), ClientProfile: in.ClientProfile,
        AudioTrack: in.AudioTrack, SubtitleTrack: in.SubtitleTrack,
        StartSec: in.StartSec, MaxBitrateKbps: int32(in.MaxBitrateKbps),
        Format: in.Format, ForceSoftware: in.ForceSoftware,
        ForceTranscode: in.ForceTranscode, BurnSubs: in.BurnSubs,
        AcceptQueue: in.AcceptQueue,
    })
    if err != nil { return nil, mapStreamingErr(err) }
    sess := resp.Session

    sid := uuid.MustParse(sess.SessionId)
    expires := s.clock().Add(s.cfg.SessionURLTTL)
    mode := Mode(sess.Mode)

    var manifestURL, directURL string
    switch mode {
    case ModeDirect:
        directURL, _ = s.signer.Sign(auth.Claims{
            Aud: "streaming", Sub: sid.String(),
            UserID: user.ID.String(), Libraries: []string{libID.String()},
            ExpiresAt: expires, Issuer: "api",
        }, "/stream/direct/"+in.VideoID.String())
    default:
        manifestURL, _ = s.signer.Sign(auth.Claims{
            Aud: "streaming", Sub: sid.String(),
            UserID: user.ID.String(), Libraries: []string{libID.String()},
            ExpiresAt: expires, Issuer: "api",
        }, "/stream/"+sid.String()+"/manifest.m3u8")
    }

    if err := s.db.InsertSession(ctx, /* ... fields ... */); err != nil {
        return nil, httperror.Internal("session persist failed")
    }

    return &OpenResponse{
        SessionID: sid, Mode: mode,
        ManifestURL: manifestURL, DirectURL: directURL,
        ExpiresAt: expires,
        Ladder: toRungs(sess.Ladder), CurrentRendition: sess.CurrentRendition,
        Warnings: warnings,
    }, nil
}

func mapStreamingErr(err error) *httperror.Error {
    st, ok := status.FromError(err)
    if !ok { return httperror.Internal("streaming") }
    switch st.Code() {
    case codes.Unavailable, codes.DeadlineExceeded:
        return httperror.Unavailable(5).WithType(TypeStreamingUnavailable)
    case codes.PermissionDenied:
        return httperror.Forbidden(TypeAccessDenied, st.Message())
    case codes.ResourceExhausted:
        return httperror.Unavailable(5).WithType(TypeTranscoderBusy)
    default:
        return httperror.Internal("streaming")
    }
}
```

## 7. Capabilities cache

```go
// api/internal/streaming/cache.go
package streaming

import (
    "context"
    "sync"
    "time"
)

type capCache struct {
    mu     sync.RWMutex
    val    *Capabilities
    expire time.Time
}

func (s *service) capabilities(ctx context.Context) (*Capabilities, error) {
    s.cap.mu.RLock()
    if s.cap.val != nil && time.Now().Before(s.cap.expire) {
        out := *s.cap.val
        s.cap.mu.RUnlock()
        return &out, nil
    }
    s.cap.mu.RUnlock()

    s.cap.mu.Lock()
    defer s.cap.mu.Unlock()
    // Double-check.
    if s.cap.val != nil && time.Now().Before(s.cap.expire) { return s.cap.val, nil }

    grpcCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()
    r, err := s.streaming.GetCapabilities(grpcCtx, &pb.GetCapabilitiesRequest{})
    if err != nil { return nil, err }

    s.cap.val = &Capabilities{
        Codecs: r.Codecs, HWAccel: r.Hwaccel,
        MaxBitrateKbps: int(r.MaxBitrateKbps),
        SupportedContainers: r.SupportedContainers,
        TranscodeSlots: &Slots{Used: int(r.Slots.Used), Capacity: int(r.Slots.Capacity)},
    }
    s.cap.expire = time.Now().Add(60 * time.Second)
    return s.cap.val, nil
}

// startProfilesListener subscribes to LISTEN profiles_changed and
// invalidates the cap cache on any notification.
func (s *service) startProfilesListener(ctx context.Context) {
    go func() {
        for evt := range s.notify.Subscribe(ctx, "profiles_changed") {
            _ = evt
            s.cap.mu.Lock()
            s.cap.val = nil
            s.cap.expire = time.Time{}
            s.cap.mu.Unlock()
        }
    }()
}
```

## 8. SQL — sqlc inputs

`shared/db/queries/streaming_sessions.sql`:

```sql
-- name: InsertStreamingSession :one
INSERT INTO streaming_sessions
    (id, user_id, video_id, library_id, client_profile, mode,
     audio_track, subtitle_track, start_sec, max_bitrate_kbps,
     ladder, current_rendition, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetStreamingSession :one
SELECT * FROM streaming_sessions
 WHERE id = $1 AND user_id = $2;

-- name: CloseStreamingSession :exec
UPDATE streaming_sessions
   SET closed_at = now()
 WHERE id = $1 AND user_id = $2 AND closed_at IS NULL;
```

## 9. Test plan

### 9.1 Unit (`sign_test.go`)

| Test | What it pins |
|---|---|
| `TestSignContainsClaims` | Decoded JWT carries `aud=streaming`, `sub=session_id`, `usr=user_id`, `lib=[library_id]`, `iss=api`, `exp=iat+ttl`. |
| `TestSignExpiresFromClock` | Frozen clock → `exp` is exactly `clock.Now()+ttl`. |
| `TestSignDirectVsManifest` | Direct play path uses `/stream/direct/{video_id}`; transcode path uses `/stream/{session_id}/manifest.m3u8`. |

### 9.2 Unit (`cache_test.go`)

| Test | What it pins |
|---|---|
| `TestCapabilitiesCacheHit` | 100 calls in 1 s → exactly one gRPC call. |
| `TestCapabilitiesCacheExpire` | Advance clock past 60 s → next call refetches. |
| `TestCapabilitiesProfilesNotifyInvalidates` | Publish `profiles_changed` event → next call refetches. |

### 9.3 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestOpenAndClose` | POST → 200 with `manifest_url`; DELETE → 204; `streaming_sessions.closed_at` set. |
| `TestOpenStartSecPropagated` | POST `{start_sec: 600}` → fake Streaming receives `start_sec=600`. |
| `TestOpenForbiddenVideo` | User without library access → 403 `access-denied`; Streaming gRPC never called. |
| `TestCapabilitiesEndpoint` | GET → 200 with codecs/hwaccel populated. |
| `TestOpenDirect` | Streaming returns `mode=direct` → response has `direct_url` populated, `manifest_url` empty. |
| `TestStreamingDown` | gRPC `UNAVAILABLE` → 503 `streaming-unavailable` with `Retry-After: 5`. |
| `TestStartSecClamp` | `start_sec=600` on a 500 s video → `start_sec` stored as 495; `Maktaba-Warning: start-sec-clamped` header set. |
| `TestUnknownClientProfile` | `client_profile="banana"` → falls back to generic; warn log emitted; 200 returned. |
| `TestConcurrentOpensSucceed` | Two POSTs for same `(user, video)` → both succeed; rate limit (Story 7.19) governs throttling. |
| `TestIdempotencyKeyReplay` | Same `Idempotency-Key` on POST → cached response replayed; only one Streaming gRPC call recorded. |
| `TestExpiredManifestNotMintedAgain` | Once a session's `expires_at` passes, GET returns the stale row but with `expires_at` in the past — clients are expected to re-open. |

## 10. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Streaming gRPC down | 503 `streaming-unavailable`, `Retry-After: 5`. | `TestStreamingDown` |
| `start_sec > duration_sec` | Clamped to `duration - 5`; warning header set. | `TestStartSecClamp` |
| Unknown `client_profile` | Generic fallback, warn log, 200. | `TestUnknownClientProfile` |
| Two concurrent opens for same `(user, video)` | Both succeed (legitimate two-device watch). | `TestConcurrentOpensSucceed` |
| Manifest URL expired before client fetch | Streaming returns 401 to the player; client must re-POST `/sessions`. | Documented |
| Idempotency-Key replay | Story 7.1 middleware caches the OpenResponse; second POST returns the same `session_id`. | `TestIdempotencyKeyReplay` |
| User access revoked between open and DELETE | DELETE still authorised by `user_id` match (the user still owns their session). Streaming is informed; subsequent fetches by the player return 401. | Documented |
| `force_software=true` on a system without HW accel | Streaming returns mode=transcode regardless; the API forwards. | Streaming-side concern |
| `accept_queue=true` and slots full | Streaming returns mode=transcode with a `queued: true` flag in the gRPC response; the API forwards as a `Maktaba-Warning: queued` header. | Streaming-side; documented |
| Capabilities cache stale during a Streaming version bump | Mitigated by `LISTEN profiles_changed`; eventual fallback is the 60 s TTL. | `TestCapabilitiesProfilesNotifyInvalidates` |

## 11. Acceptance checklist

- [ ] `POST /sessions` validates auth, calls Streaming gRPC, mints signed URL, persists row.
- [ ] `GET /sessions/{id}` returns the row + ladder/rendition.
- [ ] `DELETE /sessions/{id}` calls Streaming `CloseSession`, sets `closed_at`.
- [ ] `GET /capabilities` cached 60 s; invalidated by NOTIFY.
- [ ] Signed URL contains all six claims; TTL respected.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.10.
