# Implementation Plan — Story 10.8 Signed-URL minter

> Companion to [story-10-08-signed-url-minter.md](story-10-08-signed-url-minter.md).
> Keys come from [Story 10.6](plan-10-06-rs256-keys-jwks.md). The Claims
> struct and `Mint` helper come from [Story 10.3](plan-10-03-native-login.md).
> The library-ACL resolver is shared with [Story 10.13](story-10-13-permission-model.md).
> Streaming-side enforcement is [Story 10.7](plan-10-07-streaming-jwt-verify.md);
> wire format is owned by Epic 8 Story 8.1.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `api/internal/auth/signedurl/` — `Minter` interface + concrete `RSASignedURLMinter`. |
| Caller surfaces | API session-open handler (Epic 7 Story 7.10), direct-play (Epic 7 Story 7.7), static-asset (Epic 7 Story 7.13). Each calls `minter.MintXxx(...)`. |
| TTL clamp | `clampTTL(want, max)` returns the smaller value and increments a counter. |
| Pre-mint ACL check | `LibrariesForUser(ctx, user) → []library_id`; mint refuses (with `403 access-denied`) if the resource library is not in the set. |
| Out of scope | Server-side verify (Story 10.7), DB ACL row schema (owned by 10.13's plan). |

## 1. Architecture diagram

```
caller (session-open / direct-play / static-asset handler)
   │
   ▼
┌────────────────────────────────────────────────────────────────┐
│ signedurl.Minter                                                 │
│   MintManifestURL(ctx, sessionID, userID, libID, ttl)            │
│   MintDirectURL(ctx, videoID, userID, libID, ttl)                │
│   MintStaticURL(ctx, artifactPath, videoID, userID, libID, ttl)  │
│                                                                   │
│   pipeline (per call):                                            │
│     1. resolveAccess(ctx, userID, libID)                          │
│         - libs := libACL.LibrariesForUser(userID)                 │
│         - if libID ∉ libs && !user.IsAdmin → ErrAccessDenied      │
│     2. ttl := clampTTL(ttl, cfg.MaxSignedURLTTLSec)               │
│     3. claims := build(aud, sub, usr, libs, ttl)                   │
│     4. tok := auth.Mint(claims, signer)                            │
│         - bubble KeyUnavailable from signer up                     │
│     5. return URL.with("?sig=" + tok)                              │
└────────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/auth/signedurl/minter.go` | `Minter` interface + struct. |
| `api/internal/auth/signedurl/clamp.go` | `clampTTL` + Prometheus counter `maktaba_signedurl_ttl_clamped_total`. |
| `api/internal/auth/signedurl/minter_test.go` | Unit tests. |
| `api/internal/auth/signedurl/integration_test.go` | End-to-end against a real Streaming verify (uses Story 10.7's middleware). |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Add `Auth.SessionURLTTLSec` (default 1800), `Auth.DirectURLTTLSec` (default 1800), `Auth.StaticURLTTLSec` (default 3600), `Auth.MaxSignedURLTTLSec` (default 86400), `Auth.StreamingOrigin` (e.g., `https://stream.maktaba.local`). |
| `api/internal/http/sessions.go` | Story 7.10's session-open handler calls `MintManifestURL`. |
| `api/internal/http/direct.go` | Story 7.7's direct-play handler calls `MintDirectURL`. |
| `api/internal/http/static.go` | Story 7.13's static-asset handler calls `MintStaticURL`. |

### 2.3 Type definitions

```go
// api/internal/auth/signedurl/minter.go
package signedurl

import (
    "context"
    "time"

    "github.com/google/uuid"

    "maktaba/api/internal/auth"
)

type Minter interface {
    MintManifestURL(ctx context.Context, p ManifestParams) (string, error)
    MintDirectURL  (ctx context.Context, p DirectParams)   (string, error)
    MintStaticURL  (ctx context.Context, p StaticParams)   (string, error)
}

type ManifestParams struct {
    SessionID  uuid.UUID
    UserID     uuid.UUID
    LibraryID  uuid.UUID
    TTL        time.Duration
}

type DirectParams struct {
    VideoID    uuid.UUID
    UserID     uuid.UUID
    LibraryID  uuid.UUID
    TTL        time.Duration
}

type StaticParams struct {
    ArtifactPath string         // e.g., "/cache/posters/<video>.jpg"
    VideoID      uuid.UUID      // for usr/lib/audit attribution
    UserID       uuid.UUID
    LibraryID    uuid.UUID
    TTL          time.Duration
}

var (
    ErrAccessDenied   = errors.New("signedurl: user does not have access to library")
    ErrKeyUnavailable = errors.New("signedurl: signer unavailable")
)
```

### 2.4 Function signatures

```go
func New(signer auth.Signer, libACL auth.LibACL, users auth.Store, cfg Config, origin string) Minter

func clampTTL(want time.Duration, max time.Duration) time.Duration
```

## 3. Mint logic

```go
// api/internal/auth/signedurl/minter.go
type minter struct {
    signer auth.Signer
    libACL auth.LibACL
    users  auth.Store
    cfg    Config
    origin string  // e.g. "https://stream.maktaba.local"
}

func (m *minter) MintManifestURL(ctx context.Context, p ManifestParams) (string, error) {
    libs, err := m.resolveAccess(ctx, p.UserID, p.LibraryID)
    if err != nil { return "", err }

    ttl := clampTTL(p.TTL, time.Duration(m.cfg.MaxSignedURLTTLSec)*time.Second)
    now := time.Now().Unix()
    claims := auth.Claims{
        Iss: "maktaba", Aud: "streaming", Sub: p.SessionID.String(),
        Iat: now, Exp: now + int64(ttl.Seconds()),
        Usr: p.UserID.String(),
        Lib: libsToStrings(libs),       // see §5 — admin gets every library
    }
    tok, err := auth.Mint(claims, m.signer)
    if err != nil { return "", fmt.Errorf("%w: %v", ErrKeyUnavailable, err) }

    u := fmt.Sprintf("%s/stream/%s/manifest.m3u8?sig=%s",
        m.origin, p.SessionID, tok)
    return u, nil
}

func (m *minter) MintDirectURL(ctx context.Context, p DirectParams) (string, error) {
    libs, err := m.resolveAccess(ctx, p.UserID, p.LibraryID)
    if err != nil { return "", err }

    ttl := clampTTL(p.TTL, time.Duration(m.cfg.MaxSignedURLTTLSec)*time.Second)
    now := time.Now().Unix()
    claims := auth.Claims{
        Iss: "maktaba", Aud: "streaming-direct", Sub: p.VideoID.String(),
        Iat: now, Exp: now + int64(ttl.Seconds()),
        Usr: p.UserID.String(),
        Lib: libsToStrings(libs),
    }
    tok, err := auth.Mint(claims, m.signer)
    if err != nil { return "", fmt.Errorf("%w: %v", ErrKeyUnavailable, err) }
    return fmt.Sprintf("%s/stream/direct/%s?sig=%s", m.origin, p.VideoID, tok), nil
}

func (m *minter) MintStaticURL(ctx context.Context, p StaticParams) (string, error) {
    libs, err := m.resolveAccess(ctx, p.UserID, p.LibraryID)
    if err != nil { return "", err }

    ttl := clampTTL(p.TTL, time.Duration(m.cfg.MaxSignedURLTTLSec)*time.Second)
    sub := sha256Hex(p.ArtifactPath)   // 64 hex chars
    now := time.Now().Unix()
    claims := auth.Claims{
        Iss: "maktaba", Aud: "streaming-static", Sub: sub,
        Iat: now, Exp: now + int64(ttl.Seconds()),
        Usr: p.UserID.String(),
        Lib: libsToStrings(libs),
    }
    tok, err := auth.Mint(claims, m.signer)
    if err != nil { return "", fmt.Errorf("%w: %v", ErrKeyUnavailable, err) }

    return fmt.Sprintf("%s/stream/static/%s?sig=%s", m.origin, url.PathEscape(p.ArtifactPath), tok), nil
}

func (m *minter) resolveAccess(ctx context.Context, userID, libID uuid.UUID) ([]uuid.UUID, error) {
    user, err := m.users.GetByID(ctx, userID)
    if err != nil { return nil, ErrAccessDenied }

    if user.IsAdmin {
        // Story 10.9 AC-5 + Story 10.13 AC-1: admin/sentinel → every library.
        return m.libACL.AllLibraryIDs(ctx)
    }

    libs, err := m.libACL.LibrariesForUser(ctx, userID)
    if err != nil { return nil, err }
    for _, l := range libs {
        if l == libID { return libs, nil }
    }
    return nil, ErrAccessDenied
}
```

## 4. TTL clamping

```go
// api/internal/auth/signedurl/clamp.go
var ttlClampedCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
    Name: "maktaba_signedurl_ttl_clamped_total",
    Help: "Number of mint calls whose requested TTL exceeded MaxSignedURLTTLSec",
}, nil)

func clampTTL(want time.Duration, max time.Duration) time.Duration {
    if want <= 0 {
        // A zero or negative ttl is a programming bug; surface as max so a
        // bad call still produces a working URL until the operator notices.
        ttlClampedCounter.WithLabelValues().Inc()
        return max
    }
    if want > max {
        ttlClampedCounter.WithLabelValues().Inc()
        return max
    }
    return want
}
```

The clamp is silent (no error) per AC-4. The metric increment is the
operator-visible signal.

## 5. `lib[]` resolution

`libsToStrings([]uuid.UUID) []string` is a thin helper. The substantive
logic is in `resolveAccess`:

1. Admin / sentinel → every library id (`AllLibraryIDs` from the ACL).
2. Non-admin → the user's `library_acl` rows.
3. Mint refuses (with `ErrAccessDenied`) when the resource's `libID`
   is not in the user's set.

This guarantees the streaming verify (Story 10.7) sees only tokens
whose `lib[]` is honest.

## 6. HTTP error envelope from callers

The session-open / direct / static handlers translate minter errors:

```go
url, err := minter.MintManifestURL(ctx, params)
if err != nil {
    if errors.Is(err, signedurl.ErrAccessDenied) {
        problem(w, http.StatusForbidden, "access-denied", "")
        return
    }
    if errors.Is(err, signedurl.ErrKeyUnavailable) {
        problem(w, http.StatusServiceUnavailable, "signing-unavailable", "")
        return
    }
    problem(w, http.StatusInternalServerError, "internal", "")
    return
}
```

## 7. Crypto details

| Concern | Decision |
|---|---|
| Signature alg | RS256 via `auth.Mint` (Story 10.3 §4 / Story 10.6 signer). `WithValidMethods` on the verify side closes alg-confusion. |
| Static-asset `sub` | `sha256(artifact_path)` hex-encoded (64 chars). Stable across requests for the same artifact; opaque enough that the `sub` doesn't leak the on-disk path. |
| Manifest segment URLs | The segment URLs inside the manifest are NOT individually signed; the segment requests carry the same `sig` as the manifest because Streaming attaches it via internal redirect (Epic 8 Story 8.x). The `sub=session_id` claim ensures any segment under that session is allowed. |
| URL escaping | `url.PathEscape(artifact_path)` before insertion; the verify side uses the un-escaped form when computing the sha256 (the sha256 is over the canonical relative path, NOT the URL-encoded form). The minter passes the canonical path to sha256; the URL-encoded form is purely transport. |
| TTL bound | Hard cap 86400s (24h). Even an admin cannot exceed it; the cap exists to bound the blast radius of a leaked URL. |
| Token in `?sig=` vs Authorization header | The minter places it in `?sig=` because manifests, browsers, and `<video src>` cannot set headers. Story 10.7's middleware accepts both. |

## 8. Test plan

### 8.1 Mint round-trips (`minter_test.go`)

| Test | What it pins |
|---|---|
| `TestMintManifestClaimsMatchAC1` | Decode the resulting JWT: `aud="streaming"`, `sub=session_id`, `usr=user_id`, `lib=[library_id]`, `exp - iat == ttl`. |
| `TestMintDirectClaimsMatchAC2` | `aud="streaming-direct"`, `sub=video_id`, `usr=user_id`, `lib=[library_id]`. |
| `TestMintStaticClaimsMatchAC3` | `aud="streaming-static"`, `sub=sha256(artifact_path)`, `usr=user_id`, `lib=[library_id]`. |
| `TestMintStaticTTLDefaults` | Without an explicit ttl, defaults: poster 1h, sprite 1h, subtitle 1h, chapters 1h. |
| `TestMintIncludesKID` | Header `kid` matches signer.KID(); payload `kid` matches. |
| `TestMintFailsWhenSignerKeyUnavailable` | Stub signer returns error → minter returns wrapped `ErrKeyUnavailable`. |
| `TestMintCallerHandlerReturns503` | Full handler test: signer down → 503 `signing-unavailable`. |
| `TestStaticSubIsSha256OfPath` | `MintStaticURL("/foo/bar.png")` → JWT.Sub == `hex(sha256("/foo/bar.png"))`. |

### 8.2 Access checks

| Test | What it pins |
|---|---|
| `TestMintRefusesWhenLibNotInACL` | User U has libs [L1]; mint for L2 → `ErrAccessDenied`; no JWT issued. |
| `TestHandlerReturns403WhenAccessDenied` | The integration test for the session-open handler returns 403 `access-denied`; never any token in the response body. |
| `TestAdminGetsEveryLibInLibClaim` | User with `is_admin=true`; `lib[]` in the issued token contains every library id from `AllLibraryIDs`. |
| `TestSentinelGetsEveryLib` | Same as above but specifically for the sentinel UUID (Story 10.9 AC-5). |
| `TestMintIncludesUserLibsNotJustResourceLib` | User U has libs [L1, L2]; mint for L1 → `lib[]` = [L1, L2] (the full snapshot, not a singleton). This means a single signed URL can be reused by Streaming to validate multiple resources, but Streaming still enforces per-resource lib match (Story 10.7). |

### 8.3 TTL clamp

| Test | What it pins |
|---|---|
| `TestClampReturnsMaxOnOverflow` | `want=48h, max=24h` → returns 24h; counter += 1. |
| `TestClampReturnsWantWhenInRange` | `want=1h` → returns 1h; counter unchanged. |
| `TestClampHandlesZeroTTL` | `want=0` → returns max; counter += 1. |
| `TestClampHandlesNegativeTTL` | `want=-1m` → returns max; counter += 1. |
| `TestMintEnforcesTTLClampInJWT` | A 48h request with 24h max → token's `exp-iat == 86400`. |

### 8.4 End-to-end with Streaming verify

`integration_test.go` runs the API minter and the Streaming middleware
in-process and asserts:

| Test | What it pins |
|---|---|
| `TestMintedManifestVerifiedByStreaming` | API mint → Streaming middleware accepts → next handler runs. |
| `TestExpiredMintedURLRejected` | TTL=1s; sleep 2s; request → 401 `expired`. |
| `TestWrongAudCrossUseRejected` | A `aud=streaming` token sent to `/stream/direct/{vid}` → 401 `wrong-aud`. |
| `TestAttackerForgedTokenRejected` | An attacker signs a token with their own RSA key; even with the correct claims shape → 401 (`unknown-kid` because their kid isn't in the JWKS). |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| API has no private key configured | `auth.Mint` returns an error (Story 10.6's keyring would have refused boot, so this is theoretical for the env-key case but possible during a brief reload window) → `ErrKeyUnavailable` → 503 `signing-unavailable`. | `TestMintFailsWhenSignerKeyUnavailable` |
| URL minted with `aud=streaming` then sent to `/stream/direct/...` | Rejected by Streaming with `wrong-aud`. | `TestWrongAudCrossUseRejected` |
| User downgraded between mint and use | The token's `lib[]` is a snapshot; up to TTL of staleness. For instant revocation, operator runs `keys rotate --immediate` (Story 10.6). | n/a (covered in Story 10.5 plan) |
| Static-asset path with spaces / unicode | `url.PathEscape` for transport; the sha256 is over the canonical *unescaped* path. The minter normalizes the input via `path.Clean` first to defend against `..` traversal. | `TestStaticPathNormalizationConsistent` |
| Caller passes the wrong `LibraryID` for the resource | The minter trusts the caller for "what library this resource is in" because the caller already had to look it up; we do *not* re-check the resource→library binding here. The Streaming side re-checks (Story 10.7) — defense in depth. | n/a |
| Caller passes a UUID that isn't a real video | The minter doesn't load the video; it just signs. The verify side will fail at the resource-lookup step (`ErrUnknownVideo` → 401). | Story 10.7 plan |
| `MaxSignedURLTTLSec` set to 0 in config | The clamp would always return 0 (every URL immediately expired). The config validator (Epic 7 Story 7.15) rejects 0 with a startup error. | Config validation |
| Token URL exceeds 8 KB browser query-string limit | An RSA-4096 JWT is ~700 bytes base64; well under any limit. The `lib[]` claim with 1000 entries (Story 10.13's cap) brings the token to ~30 KB — beyond browser query-string limits in some configurations. The mitigation is the lib cap; the v2 plan calls for a "lib_all" sentinel. Documented in Story 10.13. | Story 10.13 plan |
| Two callers mint for the same `(user, lib, video)` | Both succeed; both URLs are independently valid until expiry. No deduplication. | n/a |

## 10. Dependencies

No new dependencies beyond Stories 10.3 and 10.6.

## 11. Acceptance checklist

**Mint surface**
- [ ] AC-1: `MintManifestURL` returns `https://<origin>/stream/<sid>/manifest.m3u8?sig=<jwt>` with the documented claims.
- [ ] AC-2: `MintDirectURL` returns the documented shape; `usr` claim populated for audit.
- [ ] AC-3: `MintStaticURL` returns the documented shape; `sub` is `sha256(artifact_path)`.

**Pre-mint ACL**
- [ ] AC-5: a user without ACL access to the requested library → `ErrAccessDenied`; handler returns 403; no JWT is ever issued.
- [ ] Admin / sentinel → every library id appears in `lib[]`.

**TTL clamp**
- [ ] AC-4: requested TTL above `MaxSignedURLTTLSec` is silently capped; metric `maktaba_signedurl_ttl_clamped_total` increments.

**Tests**
- [ ] All §8 tests pass.
- [ ] End-to-end test (mint → Streaming verify) green in CI.

**Docs**
- [ ] README.md ticks story 10.8.
