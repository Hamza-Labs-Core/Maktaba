# Implementation Plan — Story 15.2 Global discovery (optional cloud relay)

> Companion to [story-15-02-cloud-relay.md](story-15-02-cloud-relay.md).
> The story states *what* and *why*; this plan states *how*.
> Resolves [REVIEW §5.5](../../REVIEW.md) by binding TLS SPKI pinning into
> the trust path. Tier gating from
> [Epic 16 Story 16.2](../16-subscriptions/story-16-02-premium-features.md);
> client surface from each platform's networking layer.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Server outbound agent | New binary target `api/cmd/relay-agent` (Go) — long-lived QUIC connection from server to a relay edge. Reuses the API service's auth + DB; runs as a goroutine inside the API service when `[relay] enabled = true` (no separate service). |
| Relay edge | `services/relay/` (new directory) — a Go service that accepts QUIC connections from servers and HTTPS connections from clients, routes by `mdns_id`. Owns `relay.proto` (gRPC over QUIC) and edge routing. |
| Cert rotation endpoint | `GET /api/system/cert-rotation` — signed by the **current** TLS cert (proves the server holds the private key); returns the upcoming SPKI hash and rotation window. |
| TLS SPKI pinning | Client-side per-platform: iOS/tvOS via `URLSessionDelegate`'s pinned challenge handler; Android via `OkHttp.CertificatePinner`; web (browser-side) cannot pin — documented limitation, web traffic over relay is bound to browser CA trust. |
| Quota | Per-server monthly quota gated by tier; counted in `relay_usage` table. |
| Out of scope | Federation (Story 15.3); QR pairing (Story 15.5); license key resolution (Epic 16 Story 16.4). |

## 1. Architecture diagram

```
   ┌──────────────────────────┐                ┌──────────────────────────┐
   │ Maktaba server (home LAN)│                │ Client (cellular)        │
   │  ┌────────────────────┐  │                │  ┌────────────────────┐  │
   │  │ relay-agent        │  │                │  │ HTTPS client +     │  │
   │  │  - outbound QUIC   │◄─┼──── tunnel ───►│  │ SPKI pinning       │  │
   │  │  - mux             │  │                │  │                    │  │
   │  └────────────────────┘  │                │  └─────────┬──────────┘  │
   └──────────────────────────┘                └────────────┼─────────────┘
                ▲                                            │
                │                                            ▼
                │                                ┌──────────────────────┐
                │                                │ relay edge           │
                │                                │  - terminate clients │
                │                                │  - route by mdns_id  │
                │                                │  - opaque to TLS     │
                │ TLS terminates HERE            │    (passthrough)     │
                └────────────────────────────────┴──────────────────────┘
                       (server holds the cert; relay sees ciphertext)
```

## 2. Database

`shared/db/migrations/0051_relay.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE relay_settings (
    id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    enabled         BOOLEAN NOT NULL DEFAULT false,
    region          TEXT NOT NULL DEFAULT 'us'
                    CHECK (region IN ('us','eu','ap')),
    next_spki_sha256 TEXT,                  -- pre-announced for rotation
    next_spki_until  TIMESTAMPTZ
);
INSERT INTO relay_settings (id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE relay_usage (
    period_start    DATE NOT NULL,
    bytes_in        BIGINT NOT NULL DEFAULT 0,
    bytes_out       BIGINT NOT NULL DEFAULT 0,
    sessions        BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (period_start)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS relay_usage;
DROP TABLE IF EXISTS relay_settings;
-- +goose StatementEnd
```

## 3. Relay protocol

`services/relay/proto/relay.proto`:

```proto
service RelayEdge {
    // Server registers and keeps the connection open.
    rpc Register(stream ServerFrame) returns (stream ClientFrame);
}

message ServerFrame {
    oneof body {
        Hello hello = 1;             // sent once on connect
        SessionData session_data = 2;
        SessionEOF session_eof = 3;
        Heartbeat heartbeat = 4;
    }
}

message Hello {
    string mdns_id = 1;
    string license_token = 2;       // signed token gating quota/tier
    string version = 3;
    string region_pref = 4;
}

message ClientFrame {
    oneof body {
        SessionOpen session_open = 1; // a client wants to talk to the server
        SessionData session_data = 2;
        SessionEOF session_eof = 3;
    }
}
```

Each client HTTPS connection is mapped to one logical session inside the QUIC stream. The relay never decrypts the inner TLS — it forwards the bytes between the client TCP/TLS connection and a stream multiplexed on the QUIC tunnel.

## 4. relay-agent

`api/internal/relay/agent.go`:

```go
type Agent struct {
    cfg        Config
    license    license.Token
    cert       *tls.Certificate
    log        *slog.Logger
}

func (a *Agent) Run(ctx context.Context) error {
    for {
        if err := a.connectAndPump(ctx); err != nil {
            a.log.Warn("relay disconnect; backoff", "err", err)
            // Exponential backoff with jitter; capped at 60 s.
            select {
            case <-time.After(a.backoff.Next()):
            case <-ctx.Done(): return ctx.Err()
            }
            continue
        }
    }
}

func (a *Agent) connectAndPump(ctx context.Context) error {
    conn, err := quic.DialAddr(ctx, a.cfg.EdgeAddr, &tls.Config{
        ServerName: a.cfg.EdgeServerName,
        NextProtos: []string{"maktaba-relay"},
    }, nil)
    if err != nil { return err }
    defer conn.CloseWithError(0, "shutdown")
    stream, _ := conn.OpenStreamSync(ctx)
    enc, dec := newCodecs(stream)
    if err := enc.Send(&pb.Hello{
        MdnsId: a.cfg.MdnsID,
        LicenseToken: a.license.Encoded(),
        Version: version.Version,
        RegionPref: a.cfg.Region,
    }); err != nil { return err }

    for {
        var frame pb.ClientFrame
        if err := dec.Recv(&frame); err != nil { return err }
        switch f := frame.Body.(type) {
        case *pb.ClientFrame_SessionOpen:
            go a.handleSession(f.SessionOpen, enc)
        case *pb.ClientFrame_SessionData:
            a.dispatchSessionData(f.SessionData)
        }
    }
}
```

`handleSession` opens a local TLS listener back-to-back with the server's API process and pipes bytes. **The TLS terminates inside the API process**, not in the relay-agent — the agent just shuttles raw TLS records.

## 5. Cert-rotation endpoint

`api/internal/http/cert_rotation.go`:

```go
// GET /api/system/cert-rotation
// Returns the current and upcoming SPKI hashes with a JWS signed by the
// current TLS leaf private key. Clients use this to pre-trust an
// upcoming rotation 7 days before it lands.
func certRotation(s store.RelaySettings, certs *certmgr.Manager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cur := certs.Current()
        cs := store.GetRelaySettings(r.Context())
        body := map[string]any{
            "current_spki_sha256": certs.SPKIHash(cur),
            "next_spki_sha256":    cs.NextSPKISha256,
            "next_until":          cs.NextSpkiUntil,
            "issued_at":           time.Now().UTC(),
        }
        sig, _ := jws.Sign(body, cur.PrivateKey)
        writeJSON(w, 200, map[string]any{"body": body, "sig": sig})
    }
}
```

The signature uses the existing TLS leaf as the signing key — proving the server still holds it. Clients verify with the SPKI they already pinned.

## 6. Client-side SPKI pinning

### 6.1 iOS/tvOS

```swift
final class SPKIDelegate: NSObject, URLSessionDelegate {
    let pinned: Set<String>     // sha256(spki) hex
    func urlSession(_ s: URLSession,
                    didReceive ch: URLAuthenticationChallenge,
                    completionHandler: @escaping (...) -> Void) {
        guard let trust = ch.protectionSpace.serverTrust,
              let leaf  = SecTrustGetCertificateAtIndex(trust, 0),
              let spki  = SPKI.sha256(of: leaf) else {
            return completionHandler(.cancelAuthenticationChallenge, nil)
        }
        if pinned.contains(spki) {
            completionHandler(.useCredential, URLCredential(trust: trust))
        } else {
            completionHandler(.cancelAuthenticationChallenge, nil)
        }
    }
}
```

### 6.2 Android

```kotlin
val pinner = CertificatePinner.Builder()
    .add("api.example.com", "sha256/${pinnedHash}")
    .build()
val client = OkHttpClient.Builder().certificatePinner(pinner).build()
```

### 6.3 Pin store

A platform-specific keychain entry maps `mdns_id → set<spki_hash>`; populated:
- on first LAN auth (the auth response includes `tls_spki_sha256`),
- on relay-route auth (rejected if pin mismatch),
- on QR pair (Story 15.5; pin baked into the QR).

When `cert-rotation` advertises a `next_spki_sha256`, the client adds it to the pin set; after `next_until`, the old hash is removed.

## 7. Quota enforcement

The relay edge counts bytes per `mdns_id` and writes them to a Redis-style counter. At billing-period boundaries (UTC midnight on the first), counters reset to 0; during the period, the edge enforces the per-tier ceiling and refuses new sessions when the ceiling is hit. Bytes already in flight on existing sessions continue (per AC: "reads continue but new sessions block until next cycle").

The agent reports usage to the API service every 60 s; the API reconciles into `relay_usage` so admin dashboards show cumulative usage.

## 8. Test plan

### 8.1 Server unit

| Test | What it pins |
|---|---|
| `TestAgentReconnectBackoff` | Edge refuses; agent retries with exponential backoff, capped at 60 s. |
| `TestAgentLicenseTokenRequired` | License token expired → edge rejects; agent surfaces a clear log line. |
| `TestCertRotationSignedBody` | `GET /api/system/cert-rotation` body is JWS-verifiable with the current leaf cert's pubkey. |
| `TestQuotaUsageRolledUp` | Edge sends 100 MB usage; API's `relay_usage` row sums to 100 MB. |

### 8.2 Edge

| Test | What it pins |
|---|---|
| `TestEdgePassthroughDoesNotDecrypt` | Send a known ciphertext through; edge's egress bytes equal ingress bytes (no MITM). |
| `TestEdgeQuotaExhaustedRejectsNewSession` | Set tier 50 GB, transfer 50 GB, 51st GB session_open → SESSION_DENIED `quota-exhausted`. |
| `TestEdgeFailoverWithin30s` | Kill the registered server's connection; `Register` retries; new node accepts within 30 s. |

### 8.3 Client

| Test | What it pins |
|---|---|
| `TestClientPinsAfterFirstAuth` | Auth on LAN; client stores SPKI; subsequent relay request verifies. |
| `TestClientRejectsUnpinnedCert` | Stub a malicious cert; client refuses and surfaces "Server identity changed". |
| `TestRotationOverlapAccepted` | Pre-fetch `next_spki_sha256`; cert rotates; both old and new hashes in pin set; connection succeeds. |
| `TestRotationOverlapExpiredFails` | Pre-fetched 8 days ago; expiry passed; client refuses. |
| `TestQRPinUsedOnFirstRelayConnect` | No LAN bootstrap; pair via QR; client pins the QR-supplied SPKI; connection succeeds. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Server outbound firewalled | Agent fails to connect; UI surfaces diagnostic. Free-tier and LAN unaffected. | `TestAgentBlockedOutbound` |
| Edge node failover | Agent retries; new node accepts within 30 s. | `TestEdgeFailoverWithin30s` |
| Quota exhausted mid-stream | In-flight reads continue; new sessions denied; clear UI message. | `TestEdgeQuotaExhaustedRejectsNewSession` |
| Cert rotation during active session | Active session uses the old pin until session ends; next `URLSession` task uses the new pin (which is in the set). | `TestRotationDuringSession` |
| First-ever relay (no LAN) | Pair via QR; QR-supplied SPKI is the trust anchor. | `TestQRPinUsedOnFirstRelayConnect` |
| Pin mismatch (suspected MITM) | Refuse and surface "Server identity changed — re-pair via QR". | `TestClientRejectsUnpinnedCert` |
| Region-residency requirement | Operator picks region at opt-in; agent's `RegionPref` honored by edge. | `TestRegionRouting` |
| Web client behind relay | Browser cannot pin; documented limitation; relay terminates HTTPS to the relay's domain (browser trust); the inner end-to-end protection is intact only for native clients. | `docs/operations/relay-web.md` |
| License token expires mid-session | Agent reconnects with refreshed token; in-flight sessions continue. | `TestLicenseExpiryReconnect` |
| Both servers (multi-instance) using same `mdns_id` | Edge rejects the second registration with `mdns-id-conflict`. | `TestMDNSIDConflict` |

## 10. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/quic-go/quic-go` | latest | Server↔edge transport. |
| `URLSession` | system | iOS/tvOS pinning. |
| `okhttp` | 5.x | Android pinning. |
| `mdns-sd`, `webpki` | latest | Tauri/Rust client. |

## 11. Acceptance checklist

**Server**
- [ ] `relay-agent` runs alongside API when `[relay] enabled = true`.
- [ ] Quota enforced; usage rolled up.
- [ ] `cert-rotation` endpoint signed by current cert.

**Edge**
- [ ] Routes by `mdns_id`; never decrypts.

**Clients**
- [ ] iOS/tvOS, Android, AndroidTV, desktop pin SPKI on first auth or QR.
- [ ] Pin mismatch refuses connection.

**Tests**
- [ ] All §8 tests pass.

**Docs**
- [ ] `docs/operations/relay.md` and `relay-web.md` published.
- [ ] `specs/epics/15-discovery/README.md` ticks story 15.2.
