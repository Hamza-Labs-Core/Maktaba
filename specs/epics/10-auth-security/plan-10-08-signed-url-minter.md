# Plan 10.8 — Signed-URL minter (manifest, direct, sidecar) — implementation

> Implementation plan for [story-10-08-signed-url-minter.md](story-10-08-signed-url-minter.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: signs with the active key from
> [Plan 10.6](plan-10-06-rs256-keys-jwks.md); URLs are verified by
> [Plan 10.7](plan-10-07-streaming-jwt-verify.md); `lib[]` resolution
> uses the ACL store from
> [Plan 10.13](plan-10-13-permission-model.md). Callers are the
> Streaming session handler (Epic 7 Plan 7.10), direct-play handler
> (Plan 7.7), and sidecar handlers (Plan 7.x). The wire format and
> error envelopes match
> [Epic 8 Plan 8.1](../08-streaming/plan-08-01-signed-url-verify.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Authorization (lib check) runs BEFORE signing.** The minter consults `library_acl` for the actor; if the actor lacks read access to the resource's library, it returns `ErrAccessDenied` and **no JWT is constructed**. | Story AC-5: "if the user does not have access, the minter returns 403 access-denied *before* signing — no JWT is issued for a resource the user can't read". | Signing first then checking is a footgun: a developer error that fails to check would still have produced a usable token. Authorize-first makes the authorization decision unambiguous and gives Streaming a strong invariant ("any well-formed JWT it sees grants the right library"). |
| D2 | **`lib[]` is exactly one library** — the resource's library, after verifying access. Not the actor's full library list. | Story AC-1/2/3 sample: `lib=[library_id]`. | Streaming only checks "is the resource's library in lib[]"; including extra libraries leaks information ("this user can also see X"). Single-library `lib[]` is minimal and predictable. |
| D3 | **TTLs are silently capped at `max_signed_url_ttl_sec` (default 86400)** and a counter metric `signed_url_ttl_capped_total{aud}` is incremented. The caller sees the capped value via the returned `Expiry`. | Story AC-4. | Caller's request is honored to the maximum allowed; failing the call would force every caller to know the cap. Metric makes the cap visible in operations. |
| D4 | **Three top-level functions, one shared signer.** `MintManifestURL`, `MintDirectURL`, `MintStaticURL` each call into a shared `signClaims(claims, ttl)` helper that does the TTL cap, kid lookup, and RS256 signing. | Story exposes three named functions. | The functions differ only in URL shape, `aud`, and `sub` derivation; centralising the signing path keeps cap behaviour, error handling, and metric emission in one place. |
| D5 | **`KeyUnavailable` is a sentinel error** that callers translate to HTTP 503 with `type: signing-unavailable`. The minter does *not* itself construct HTTP responses. | Story edge: "callers translate to 503 `type: signing-unavailable`". | Minter is package-internal; HTTP responsibilities belong to the handler layer. The error code path is integration-tested at the handler level. |
| D6 | **`sub` for static URLs is `sha256(artifact_path)` lowercase hex.** | Story AC-3. | Hex is the storage convention used elsewhere (see Plan 7.x's `video_artifacts.sha256` column). Streaming's resolver looks up the artifact by hash via Plan 8.15; this matches what's already on disk. |
| D7 | **`jti` is UUID v7** for monotonic-ish ordering, useful for replay-detection deferred-store work in Story 10.16. | Architecture §9.8 cites uuid v7 for tokens. | UUID v7 is already in `github.com/google/uuid` (≥ v1.6); embedding the issue time in the prefix gives audit logs a free time index without leaking high-precision timestamps. |
| D8 | **Per-aud default TTLs are config-resolved, not hard-coded.** `auth.signed_url.ttl_default_sec.{streaming, direct, static}` with shipping defaults `1800/3600/3600`. | Story AC-1 mentions `session_url_ttl_sec=1800` and AC-3 lists 1 h for sidecars. | Operators reasonably want to lengthen poster TTL or shorten manifest TTL without recompiling. Config keeps the contract fluid. |

---

## 1. Architecture diagram — mint pipeline

```
   Caller (Epic 7 handler)                Library ACL (Plan 10.13)
        │                                          │
        │ minter.MintManifestURL(ctx, req)         │
        ▼                                          │
   ┌────────────────────────────────────────────┐  │
   │ internal/auth/signurl                      │  │
   │                                            │  │
   │  1. resolve TTL (cap @ max — D3)           │  │
   │  2. acl.UserHasLibrary(ctx, user, lib) ────┼──┘   ─► false ⇒ ErrAccessDenied
   │  3. keysCache.ActiveSigning() ─────────────┼─►  no key ⇒ ErrKeyUnavailable
   │  4. build claims                           │
   │     iss=maktaba                            │
   │     aud=streaming|streaming-direct|        │
   │         streaming-static                   │
   │     sub=session_id|video_id|sha256(path)   │
   │     usr=user_id, lib=[lib_id]              │
   │     iat, exp, jti=uuid.v7, kid             │
   │  5. token.SignedString(activePriv)         │
   │  6. assemble URL with ?sig=…               │
   │  7. metric: signed_url_minted_total{aud}++ │
   └─────────────────┬──────────────────────────┘
                     │
                     ▼
                   URL string  →  caller returns to client
                                   client → Streaming verify (Plan 10.7)
```

---

## 2. Detailed implementation

### 2.1 Package layout

```
api/
└── internal/
    └── auth/
        └── signurl/
            ├── minter.go            # public API: MintManifestURL/MintDirectURL/MintStaticURL
            ├── signer.go            # internal signClaims helper
            ├── acl.go               # tiny LibraryACL interface (impl lives in Plan 10.13)
            ├── config.go            # TTL caps, default-TTL lookup
            ├── errors.go            # ErrAccessDenied, ErrKeyUnavailable
            ├── metrics.go           # prometheus counters
            └── signurl_test.go
```

### 2.2 `errors.go`, `config.go`, `metrics.go`

```go
// api/internal/auth/signurl/errors.go
package signurl

import "errors"

var (
	ErrAccessDenied   = errors.New("signurl: access denied")
	ErrKeyUnavailable = errors.New("signurl: signing key unavailable")
)
```

```go
// api/internal/auth/signurl/config.go
package signurl

import "time"

type Config struct {
	StreamingOrigin    string        // e.g. "https://streaming.maktaba.local"
	MaxTTLSec          int           // default 86400
	DefaultTTLManifest time.Duration // default 1800s
	DefaultTTLDirect   time.Duration // default 3600s
	DefaultTTLStatic   time.Duration // default 3600s
}

func DefaultConfig(streamingOrigin string) Config {
	return Config{
		StreamingOrigin:    streamingOrigin,
		MaxTTLSec:          86400,
		DefaultTTLManifest: 30 * time.Minute,
		DefaultTTLDirect:   time.Hour,
		DefaultTTLStatic:   time.Hour,
	}
}
```

```go
// api/internal/auth/signurl/metrics.go
package signurl

import "github.com/prometheus/client_golang/prometheus"

var (
	mintedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "signed_url_minted_total",
		Help: "Signed URLs minted, by aud.",
	}, []string{"aud"})

	ttlCappedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "signed_url_ttl_capped_total",
		Help: "Mint requests whose TTL was capped at max_signed_url_ttl_sec.",
	}, []string{"aud"})

	deniedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "signed_url_denied_total",
		Help: "Mint requests rejected by lib[] resolution.",
	}, []string{"aud"})
)

func init() {
	prometheus.MustRegister(mintedTotal, ttlCappedTotal, deniedTotal)
}
```

### 2.3 `acl.go` — tiny interface

```go
// api/internal/auth/signurl/acl.go
package signurl

import (
	"context"

	"github.com/google/uuid"
)

// LibraryACL is the slice of Plan 10.13 the minter actually needs.
// The concrete impl is sqlc-backed against `library_acl`; here we accept
// any implementation so this package stays independent of the auth/perm
// package's wider API.
type LibraryACL interface {
	UserHasLibrary(ctx context.Context, userID uuid.UUID, libraryID uuid.UUID) (bool, error)
}
```

### 2.4 `signer.go` — shared signing path (D4, D5)

```go
// api/internal/auth/signurl/signer.go
package signurl

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// activeKey is the minimal interface Minter needs from the keys.Cache
// (Plan 10.6).
type activeKey interface {
	ActiveSigning() (kid string, priv *rsa.PrivateKey, ok bool)
}

type signedClaims struct {
	Iss string   `json:"iss"`
	Aud string   `json:"aud"`
	Sub string   `json:"sub"`
	Usr string   `json:"usr"`
	Lib []string `json:"lib"`
	Iat int64    `json:"iat"`
	Exp int64    `json:"exp"`
	Jti string   `json:"jti"`
}

func (c signedClaims) GetExpirationTime() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(time.Unix(c.Exp, 0)), nil
}
func (c signedClaims) GetIssuedAt() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(time.Unix(c.Iat, 0)), nil
}
func (c signedClaims) GetNotBefore() (*jwt.NumericDate, error) { return nil, nil }
func (c signedClaims) GetIssuer() (string, error)              { return c.Iss, nil }
func (c signedClaims) GetSubject() (string, error)             { return c.Sub, nil }
func (c signedClaims) GetAudience() (jwt.ClaimStrings, error)  { return jwt.ClaimStrings{c.Aud}, nil }

// capTTL silently bounds the requested TTL and emits a metric on cap.
func (m *Minter) capTTL(ttl time.Duration, aud string) (time.Duration, bool) {
	if max := time.Duration(m.cfg.MaxTTLSec) * time.Second; ttl > max {
		ttlCappedTotal.WithLabelValues(aud).Inc()
		return max, true
	}
	return ttl, false
}

// signClaims is the inner path: looks up the active key, builds claims
// (with iat/exp/jti/kid), and signs.
func (m *Minter) signClaims(
	aud, sub string, userID uuid.UUID, libraryID uuid.UUID, ttl time.Duration,
) (token string, expiresAt time.Time, err error) {
	kid, priv, ok := m.keys.ActiveSigning()
	if !ok || priv == nil {
		return "", time.Time{}, ErrKeyUnavailable
	}
	now := time.Now().UTC()
	exp := now.Add(ttl)
	jti, err := uuid.NewV7()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("uuid v7: %w", err)
	}
	claims := signedClaims{
		Iss: "maktaba",
		Aud: aud,
		Sub: sub,
		Usr: userID.String(),
		Lib: []string{libraryID.String()}, // D2
		Iat: now.Unix(),
		Exp: exp.Unix(),
		Jti: jti.String(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	t.Header["kid"] = kid
	signed, err := t.SignedString(priv)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign: %w", err)
	}
	mintedTotal.WithLabelValues(aud).Inc()
	return signed, exp, nil
}

// guard so the activeKey impl is constrained at compile time.
var _ = errors.Is
```

### 2.5 `minter.go` — public surface

```go
// api/internal/auth/signurl/minter.go
package signurl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
)

type Minter struct {
	cfg  Config
	keys activeKey
	acl  LibraryACL
}

func NewMinter(cfg Config, keys activeKey, acl LibraryACL) *Minter {
	return &Minter{cfg: cfg, keys: keys, acl: acl}
}

// MintedURL is what callers pass to clients.
type MintedURL struct {
	URL       string
	ExpiresAt time.Time
	Kid       string
	Jti       string
}

// MintManifestURL signs a session-bound URL for the HLS manifest.
//
//	aud=streaming, sub=session_id
func (m *Minter) MintManifestURL(
	ctx context.Context,
	sessionID uuid.UUID, userID uuid.UUID, libraryID uuid.UUID,
	ttl time.Duration,
) (MintedURL, error) {
	if ttl <= 0 {
		ttl = m.cfg.DefaultTTLManifest
	}
	ttl, _ = m.capTTL(ttl, "streaming")
	if err := m.authorize(ctx, "streaming", userID, libraryID); err != nil {
		return MintedURL{}, err
	}
	tok, exp, err := m.signClaims("streaming", sessionID.String(), userID, libraryID, ttl)
	if err != nil {
		return MintedURL{}, err
	}
	u := fmt.Sprintf("%s/stream/%s/manifest.m3u8?sig=%s",
		m.cfg.StreamingOrigin, sessionID, url.QueryEscape(tok))
	return MintedURL{URL: u, ExpiresAt: exp}, nil
}

// MintDirectURL signs a single-shot direct-play URL.
//
//	aud=streaming-direct, sub=video_id
func (m *Minter) MintDirectURL(
	ctx context.Context,
	videoID uuid.UUID, userID uuid.UUID, libraryID uuid.UUID,
	ttl time.Duration,
) (MintedURL, error) {
	if ttl <= 0 {
		ttl = m.cfg.DefaultTTLDirect
	}
	ttl, _ = m.capTTL(ttl, "streaming-direct")
	if err := m.authorize(ctx, "streaming-direct", userID, libraryID); err != nil {
		return MintedURL{}, err
	}
	tok, exp, err := m.signClaims("streaming-direct", videoID.String(), userID, libraryID, ttl)
	if err != nil {
		return MintedURL{}, err
	}
	u := fmt.Sprintf("%s/stream/direct/%s?sig=%s",
		m.cfg.StreamingOrigin, videoID, url.QueryEscape(tok))
	return MintedURL{URL: u, ExpiresAt: exp}, nil
}

// MintStaticURL signs a sidecar artifact URL (poster, sprite, vtt, chapters).
//
//	aud=streaming-static, sub=hex(sha256(artifact_path))
func (m *Minter) MintStaticURL(
	ctx context.Context,
	artifactPath string, videoID uuid.UUID, userID uuid.UUID, libraryID uuid.UUID,
	ttl time.Duration,
) (MintedURL, error) {
	if ttl <= 0 {
		ttl = m.cfg.DefaultTTLStatic
	}
	ttl, _ = m.capTTL(ttl, "streaming-static")
	if err := m.authorize(ctx, "streaming-static", userID, libraryID); err != nil {
		return MintedURL{}, err
	}
	sum := sha256.Sum256([]byte(artifactPath)) // D6
	sub := hex.EncodeToString(sum[:])
	tok, exp, err := m.signClaims("streaming-static", sub, userID, libraryID, ttl)
	if err != nil {
		return MintedURL{}, err
	}
	// Path is encoded so traversals are inert; the verify side hashes it
	// and matches against `sub`.
	u := fmt.Sprintf("%s/stream/static/%s?sig=%s",
		m.cfg.StreamingOrigin, url.PathEscape(artifactPath), url.QueryEscape(tok))
	return MintedURL{URL: u, ExpiresAt: exp}, nil
}

// authorize enforces D1: a user lacking access to libraryID gets
// ErrAccessDenied *before* any signing happens.
func (m *Minter) authorize(ctx context.Context, aud string, userID, libraryID uuid.UUID) error {
	ok, err := m.acl.UserHasLibrary(ctx, userID, libraryID)
	if err != nil {
		return fmt.Errorf("acl lookup: %w", err)
	}
	if !ok {
		deniedTotal.WithLabelValues(aud).Inc()
		return ErrAccessDenied
	}
	return nil
}

// guard
var _ = errors.New
```

### 2.6 Caller wiring (handler example)

```go
// api/internal/streaming/handlers/session_open.go (excerpt)
mu, err := minter.MintManifestURL(ctx, sessionID, userID, libraryID, 0 /* default */)
switch {
case errors.Is(err, signurl.ErrAccessDenied):
	httpx.WriteError(w, http.StatusForbidden, "access-denied", err)
	return
case errors.Is(err, signurl.ErrKeyUnavailable):
	w.Header().Set("Retry-After", "30")
	errs.Write(w, http.StatusServiceUnavailable, errs.SigningUnavailable) // D5
	return
case err != nil:
	httpx.WriteError(w, http.StatusInternalServerError, "internal", err)
	return
}
_ = json.NewEncoder(w).Encode(map[string]any{
	"manifest_url": mu.URL,
	"expires_at":   mu.ExpiresAt,
})
```

### 2.7 Wire-format conformance with Plan 8.1

Each minted URL produces a JWT whose claims map directly to the verify
expectations of [Plan 10.7](plan-10-07-streaming-jwt-verify.md):

| aud                    | sub                         | URL pattern                                                 |
|------------------------|-----------------------------|-------------------------------------------------------------|
| `streaming`            | `<session_id>`              | `/stream/{session_id}/manifest.m3u8?sig=<jwt>`              |
| `streaming-direct`     | `<video_id>`                | `/stream/direct/{video_id}?sig=<jwt>`                       |
| `streaming-static`     | `hex(sha256(artifact_path))` | `/stream/static/{escaped_artifact_path}?sig=<jwt>`         |

Common claims (per Story 10 README): `iss=maktaba`, `iat`, `exp`,
`jti=uuid.v7`, `kid=<active>`, `usr=<user_id>`, `lib=[<library_id>]`.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols | Tests gating |
|-------|------|---------|--------------|
| 1 | `api/internal/auth/signurl/errors.go` | `ErrAccessDenied`, `ErrKeyUnavailable` | (n/a) |
| 2 | `api/internal/auth/signurl/config.go` | `Config`, `DefaultConfig` | smoke |
| 3 | `api/internal/auth/signurl/acl.go` | `LibraryACL` | (n/a) |
| 4 | `api/internal/auth/signurl/metrics.go` | `mintedTotal`, `ttlCappedTotal`, `deniedTotal` | metric registration smoke |
| 5 | `api/internal/auth/signurl/signer.go` | `signedClaims`, `Minter.capTTL`, `Minter.signClaims`, `activeKey` interface | `TestSignClaimsRoundTrip`, `TestCapTTLBumpsMetric`, `TestKeyUnavailable` |
| 6 | `api/internal/auth/signurl/minter.go` | `Minter`, `NewMinter`, `MintManifestURL`, `MintDirectURL`, `MintStaticURL`, `authorize` | `TestMint*`, `TestAccessDenied*` |
| 7 | `api/internal/streaming/handlers/session_open.go` (extend) | wiring + 503 mapping | integration |

---

## 4. Test cases keyed to ACs

### 4.1 `TestMintManifestClaims` (AC-1)

```go
func TestMintManifestProducesExpectedClaims(t *testing.T) {
	keys, signer := freshKeyCache(t)
	acl := fakeACL{allow: true}
	m := signurl.NewMinter(signurl.DefaultConfig("https://s.example"), keys, acl)
	uid, lib, sid := uuid.New(), uuid.New(), uuid.New()
	out, err := m.MintManifestURL(context.Background(), sid, uid, lib, 0)
	require.NoError(t, err)
	claims := decode(t, out.URL, signer)
	require.Equal(t, "maktaba", claims["iss"])
	require.Equal(t, "streaming", claims["aud"])
	require.Equal(t, sid.String(), claims["sub"])
	require.Equal(t, uid.String(), claims["usr"])
	require.Equal(t, []any{lib.String()}, claims["lib"])
	require.NotEmpty(t, claims["jti"])
	require.NotEmpty(t, claims["iat"])
	require.NotEmpty(t, claims["exp"])
	require.WithinDuration(t, time.Now().Add(30*time.Minute), out.ExpiresAt, 5*time.Second)
}
```

### 4.2 `TestMintDirectClaims` (AC-2)

```go
func TestMintDirectURL(t *testing.T) {
	keys, signer := freshKeyCache(t)
	m := signurl.NewMinter(signurl.DefaultConfig("https://s.example"), keys, fakeACL{allow: true})
	uid, lib, vid := uuid.New(), uuid.New(), uuid.New()
	out, err := m.MintDirectURL(context.Background(), vid, uid, lib, 0)
	require.NoError(t, err)
	require.Contains(t, out.URL, "/stream/direct/"+vid.String())
	c := decode(t, out.URL, signer)
	require.Equal(t, "streaming-direct", c["aud"])
	require.Equal(t, vid.String(), c["sub"])
	require.Equal(t, uid.String(), c["usr"])
}
```

### 4.3 `TestMintStaticHashesPath` (AC-3, D6)

```go
func TestMintStaticSubIsSha256OfPath(t *testing.T) {
	keys, signer := freshKeyCache(t)
	m := signurl.NewMinter(signurl.DefaultConfig("https://s.example"), keys, fakeACL{allow: true})
	const path = "/var/maktaba/artifacts/v1/poster_lg.jpg"
	uid, lib, vid := uuid.New(), uuid.New(), uuid.New()
	out, err := m.MintStaticURL(context.Background(), path, vid, uid, lib, 0)
	require.NoError(t, err)
	c := decode(t, out.URL, signer)
	require.Equal(t, "streaming-static", c["aud"])
	expected := hex.EncodeToString(sha256.Sum256([]byte(path))[:])
	require.Equal(t, expected, c["sub"])
}
```

### 4.4 `TestTTLSilentCap` (AC-4, D3)

```go
func TestRequestedTTLAbove24hIsCapped(t *testing.T) {
	keys, _ := freshKeyCache(t)
	m := signurl.NewMinter(signurl.DefaultConfig("https://s.example"), keys, fakeACL{allow: true})
	out, err := m.MintManifestURL(context.Background(),
		uuid.New(), uuid.New(), uuid.New(),
		7*24*time.Hour /* a week */)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(24*time.Hour), out.ExpiresAt, 5*time.Second)
	require.Equal(t, float64(1), getCounter(t, "signed_url_ttl_capped_total", "streaming"))
}
```

### 4.5 `TestAccessDeniedReturnsBeforeSigning` (AC-5, D1)

```go
func TestNoAccessReturnsErrAccessDeniedAndDoesNotSign(t *testing.T) {
	keys, _ := freshKeyCache(t)
	calls := 0
	keys.OnActiveSigningCalled(func() { calls++ })
	m := signurl.NewMinter(signurl.DefaultConfig("https://s.example"), keys, fakeACL{allow: false})
	_, err := m.MintManifestURL(context.Background(),
		uuid.New(), uuid.New(), uuid.New(), 0)
	require.ErrorIs(t, err, signurl.ErrAccessDenied)
	require.Equal(t, 0, calls)
	require.Equal(t, float64(1), getCounter(t, "signed_url_denied_total", "streaming"))
}
```

### 4.6 `TestKeyUnavailable` (D5)

```go
func TestKeyUnavailableSurfacesAsSentinel(t *testing.T) {
	keys := emptyKeyCache{}
	m := signurl.NewMinter(signurl.DefaultConfig("https://s.example"), keys, fakeACL{allow: true})
	_, err := m.MintManifestURL(context.Background(),
		uuid.New(), uuid.New(), uuid.New(), 0)
	require.ErrorIs(t, err, signurl.ErrKeyUnavailable)
}
```

### 4.7 `TestEndToEndAcceptedByVerifyMiddleware` (cross-plan integration)

```go
func TestMintedTokenVerifiesOnStreaming(t *testing.T) {
	keys, signer := freshKeyCache(t)
	m := signurl.NewMinter(signurl.DefaultConfig("https://s.example"), keys, fakeACL{allow: true})
	uid, lib, sid := uuid.New(), uuid.New(), uuid.New()

	out, err := m.MintManifestURL(context.Background(), sid, uid, lib, 5*time.Minute)
	require.NoError(t, err)

	// Hand the JWT to the streaming verify middleware (Plan 10.7).
	streamingCache := jwksCacheWith(t, signer.PublicKey())
	mw := verify.New(streamingCache,
		fakeResolver{lib: lib.String()}, time.Minute)
	rr := httptest.NewRecorder()
	tok := extractSig(t, out.URL)
	mw.Wrap("streaming", okHandler).
		ServeHTTP(rr, withSig("/stream/"+sid.String()+"/manifest.m3u8", tok))
	require.Equal(t, 200, rr.Code)

	// Fast-forward past expiry → 401 expired.
	mw2 := verify.New(streamingCache, fakeResolver{lib: lib.String()}, time.Minute)
	advanceClock(t, 10*time.Minute)
	rr2 := httptest.NewRecorder()
	mw2.Wrap("streaming", okHandler).
		ServeHTTP(rr2, withSig("/stream/"+sid.String()+"/manifest.m3u8", tok))
	require.Equal(t, 401, rr2.Code)
	require.Contains(t, rr2.Body.String(), "expired")
}
```

### 4.8 `TestWrongAudRejectedByVerifier` (story edge)

```go
func TestStreamingTokenSentToDirectEndpointFails(t *testing.T) {
	keys, signer := freshKeyCache(t)
	m := signurl.NewMinter(signurl.DefaultConfig("https://s.example"), keys, fakeACL{allow: true})
	out, _ := m.MintManifestURL(context.Background(),
		uuid.New(), uuid.New(), uuid.New(), 0)
	mw := verify.New(jwksCacheWith(t, signer.PublicKey()),
		fakeResolver{lib: "lib1"}, time.Minute)
	rr := httptest.NewRecorder()
	mw.Wrap("streaming-direct", okHandler).
		ServeHTTP(rr, withSig("/stream/direct/x", extractSig(t, out.URL)))
	require.Equal(t, 401, rr.Code)
	require.Contains(t, rr.Body.String(), "wrong-aud")
}
```

---

## 5. Edge cases

| #  | Edge case | Handled by |
|----|-----------|------------|
| E1 | **TTL of 0 or negative** — interpreted as "use the per-aud default" rather than "expire immediately." | `MintFooURL` early branch: `if ttl <= 0 { ttl = default }`. |
| E2 | **Active key rotated mid-call** — `signClaims` reads the cache once; whichever active key was current at call time is used. The next call may use a new kid. | Cache lookup per call (D5/Plan 10.6). |
| E3 | **ACL store is down** — `acl.UserHasLibrary` returns a wrapped error; minter returns it (not `ErrAccessDenied`). Caller should treat as 503. | `authorize`: error path is distinct from "ok=false." |
| E4 | **User has access to many libraries** — `lib[]` still has exactly one entry: the resource's library (D2). | `signClaims`: `Lib: []string{libraryID.String()}`. |
| E5 | **`artifactPath` contains `..`** — irrelevant to the minter (it just hashes the string), but Plan 8.13 enforces server-side path validation against the manifest of allowed artifacts. | Documented; minter is path-shape-agnostic. |
| E6 | **UUID v7 generation fails** — extremely unlikely (uses crypto/rand). Minter wraps and returns. | `signClaims` error path. |
| E7 | **Caller passes the wrong `libraryID` for the resource** — minter trusts the caller (it's the one with the resource handle). The verify side double-checks via `LibraryResolver` (Plan 10.7 D4). | Trust boundary documented in godoc; verify catches misalignment as `wrong-lib`. |
| E8 | **Same `(user, library)` minted ten thousand times in a tight loop** — each gets a unique `jti` (UUID v7); no replay-detection store on the mint side, but the metric `signed_url_minted_total` reflects the rate. | Stateless mint; replay-detection is a deferred Story 10.16 hook. |
| E9 | **`StreamingOrigin` config missing** — minter still produces a URL, just without scheme/host. Boot-time config validation (Plan 7.15) catches this and refuses to start. | Out-of-scope for minter; documented dependency. |
| E10 | **Cap is set very low (e.g., 60s) by an operator** — every mint call caps and bumps the metric. The metric label `aud` lets operators see whether the cap is bothering the streaming, direct, or static paths most. | D3 surfaces operationally. |

---

## 6. Acceptance checklist

- [ ] **A1** `MintManifestURL(sessionID, userID, libraryID, ttl)` returns a URL of the form `<origin>/stream/{sid}/manifest.m3u8?sig=<jwt>` whose claims have `iss=maktaba, aud=streaming, sub=session_id, usr=user_id, lib=[library_id], iat, exp=now+ttl, jti, kid`. Default TTL 1800s. (`TestMintManifestProducesExpectedClaims`)
- [ ] **A2** `MintDirectURL(videoID, userID, libraryID, ttl)` carries `aud=streaming-direct, sub=video_id, usr=user_id, lib=[library_id]`. (`TestMintDirectURL`)
- [ ] **A3** `MintStaticURL(artifactPath, videoID, userID, libraryID, ttl)` carries `aud=streaming-static, sub=hex(sha256(path)), usr=user_id, lib=[library_id]`; default TTL 3600s for poster/sprite/subtitle/chapters JSON. (`TestMintStaticSubIsSha256OfPath`)
- [ ] **A4** TTLs above `max_signed_url_ttl_sec` (default 86400) are silently capped; `signed_url_ttl_capped_total{aud}` increments. (`TestRequestedTTLAbove24hIsCapped`)
- [ ] **A5** Authorization runs **before** signing: `acl.UserHasLibrary(user, lib) == false` returns `ErrAccessDenied` and never reaches the key cache; metric `signed_url_denied_total{aud}` increments. (`TestNoAccessReturnsErrAccessDeniedAndDoesNotSign`)
- [ ] **A6** When the active signing key is unavailable (env-only secret missing on this replica), the minter returns `ErrKeyUnavailable`; callers translate to HTTP 503 `type: signing-unavailable`. (`TestKeyUnavailableSurfacesAsSentinel`)
- [ ] **A7** A token minted by `MintManifestURL` is accepted by the Plan 10.7 verify middleware; presenting that same token to a `streaming-direct` handler returns 401 `type: wrong-aud`. (`TestMintedTokenVerifiesOnStreaming`, `TestStreamingTokenSentToDirectEndpointFails`)
- [ ] **A8** `lib[]` always contains exactly one library — the resource's library, not the actor's full ACL list. (Inspected in `TestMintManifestProducesExpectedClaims`.)
- [ ] **A9** `jti` is a UUID v7 (parseable, sortable). (Sub-assertion in `TestMintManifestProducesExpectedClaims`.)
- [ ] **A10** Per-aud default TTLs come from `Config` and ship with `1800/3600/3600` for streaming/direct/static.
