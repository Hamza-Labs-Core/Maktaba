# Implementation Plan — Story 23.3 Transport security

> Companion to [story-23-03-transport-security.md](story-23-03-transport-security.md).
> Story states *what* and *why*; this plan states *how*.
> Caddy reverse-proxy comes from
> [Story 22.3](plan-22-03-container-images.md). Inter-service gRPC is
> defined in [architecture.md §1.4](../../architecture.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| TLS termination | Caddy (front of stack). Local-CA on Mac, Let's Encrypt on Linux. |
| TLS config | TLS 1.2 minimum, modern cipher suite list, OCSP stapling, ALPN h2. |
| HSTS | Default-on (`max-age=31536000; includeSubDomains`); env opt-out via `MAKTABA_DISABLE_HSTS=true`. |
| Inter-service mTLS | Each service gets a SPIFFE-style cert from a small in-process CA owned by the API. |
| Loopback exception | When all services bind to `127.0.0.1` the mTLS path is replaced by a documented loopback-only trust; a startup banner warns. |
| Captive-portal defense | Native clients refuse to send credentials over a downgraded TLS connection (cert-pin to issuer fingerprint at first connect). |
| Out of scope | Application-layer auth (Stories 23.1, 23.2); secrets storage (23.4). |

## 1. Architecture diagram

```
                ┌─────────────────────┐
   browser/    │ Caddy (TLS 1.2+)    │
   client ────►│  HSTS, OCSP staple  │ ALPN h2
                │  ALPN h2            │
                └──────┬──────────────┘
                       │ h2c (loopback)
       ┌───────┬───────┼───────┬───────────┐
       ▼       ▼       ▼       ▼           ▼
      api  streaming  pipeline  postgres   web
       │       │         │
       └───────┴─────────┘   gRPC mTLS (when not loopback colocated)
            internal-ca: in-process; rotates with the streaming key.
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `deploy/docker/caddy/snippets/tls-modern.conf` | The vetted TLS block; included from the main Caddyfile. |
| `api/internal/intca/ca.go` | In-process CA: mints internal mTLS certs for streaming + pipeline. Rotation tied to the JWT signing key (Story 23.1) for simplicity. |
| `api/internal/intca/dispense.go` | gRPC `Diagnostics.IssueServiceCert` — only callable from a process with the bootstrap secret on disk. |
| `streaming/internal/grpcclient/mtls.go` | gRPC dial options (ClientCert, RootCA). |
| `pipeline/src/maktaba_pipeline/grpc/mtls.py` | gRPC server creds (TLS, client-auth required). |
| `tools/tls-doctor.sh` | Wrapper around `nmap --script ssl-enum-ciphers` for TC1. |
| `web/src/lib/tls-pin.ts` | Mobile/desktop cert-pin storage helper. |
| Tests — `_test.go` per file plus `tls_smoke_test.sh`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `deploy/docker/caddy/Caddyfile` | Include `tls-modern.conf`; opt-in HSTS env wiring. |
| `api/cmd/api/serve.go` | Bootstraps the internal CA; emits the bootstrap secret on first run. |
| `api/internal/grpcserver/*` | Wraps server with `credentials.NewTLS`. |
| `streaming/internal/grpcserver/*` | Same. |

### 2.3 Caddyfile snippets

`deploy/docker/caddy/snippets/tls-modern.conf`:

```
(tls-modern) {
    tls {
        protocols tls1.2 tls1.3
        # Modern profile per Mozilla: prefer AEAD ciphers.
        ciphers TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384 \
                TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384 \
                TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256 \
                TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256 \
                TLS_AES_128_GCM_SHA256 \
                TLS_AES_256_GCM_SHA384 \
                TLS_CHACHA20_POLY1305_SHA256
        curves x25519 secp384r1 secp256r1
        alpn h2 http/1.1
    }
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
    }
}
```

`Caddyfile` includes the snippet under each site:

```
{$MAKTABA_HOSTNAME:localhost} {
    import tls-modern

    @hsts_disabled expression {env.MAKTABA_DISABLE_HSTS} == "true"
    handle @hsts_disabled {
        header -Strict-Transport-Security
    }
    # ...routes per Story 22.3
}
```

OCSP stapling is on by default in Caddy; nothing to enable.

### 2.4 The internal CA

`api/internal/intca/ca.go`:

```go
type CA struct {
    cert *x509.Certificate
    key  *rsa.PrivateKey
    pool *x509.CertPool
    db   *db.Queries
}

func NewCA(ctx context.Context, q *db.Queries) (*CA, error) {
    // Fetch the persisted CA from DB; if absent, generate.
    row, err := q.GetInternalCA(ctx)
    if errors.Is(err, sql.ErrNoRows) {
        return generateAndStore(ctx, q)
    }
    if err != nil { return nil, err }
    cert, key := decodeCA(row.Pem, row.PrivateKey)
    pool := x509.NewCertPool()
    pool.AddCert(cert)
    return &CA{cert: cert, key: key, pool: pool, db: q}, nil
}

// Issue mints a leaf cert for an internal service. The CN is the service
// name (`streaming`, `pipeline`); SANs include `localhost` and the
// container name.
func (c *CA) Issue(name string, sans []string, ttl time.Duration) (certPEM, keyPEM []byte, err error) {
    leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
    template := &x509.Certificate{
        SerialNumber: bigRand(),
        Subject:      pkix.Name{CommonName: name},
        DNSNames:     append([]string{name}, sans...),
        NotBefore:    time.Now().Add(-1 * time.Minute),
        NotAfter:     time.Now().Add(ttl),
        KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
        ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
    }
    leafDER, err := x509.CreateCertificate(rand.Reader, template, c.cert, &leafKey.PublicKey, c.key)
    if err != nil { return nil, nil, err }
    certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
    keyPEM  = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY",
        Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
    return certPEM, keyPEM, nil
}
```

Leaf certs have `ttl = 24h` and rotate via a small daemon in each
service that re-fetches when the cert is within 1 h of expiry.

### 2.5 Dispense flow

The streaming and pipeline services bootstrap by calling the API's
internal gRPC endpoint with a bootstrap token shared via
`MAKTABA_INTERNAL_BOOTSTRAP_TOKEN`. The endpoint serves the leaf cert +
key + CA bundle:

```go
// IssueServiceCert(ctx, ServiceName) returns CertBundle.
func (s *DiagSvc) IssueServiceCert(ctx context.Context, in *pb.IssueRequest) (*pb.IssueResponse, error) {
    if !s.bootstrap.Verify(ctx) { return nil, status.Error(codes.PermissionDenied, "bad bootstrap") }
    cert, key, err := s.ca.Issue(in.Name, in.Sans, 24*time.Hour)
    if err != nil { return nil, err }
    return &pb.IssueResponse{
        CertPem: cert, KeyPem: key, CaPem: s.ca.PEM(),
    }, nil
}
```

Bootstrap token is rotated on every API restart and printed once to
stdout; operators set the env on the streaming/pipeline containers.
After first issue, services persist their cert/key under
`/var/maktaba/run/<svc>.pem` and don't need the bootstrap token again
(rotation uses cert-auth). On a fresh container rebuild, the bootstrap
flow re-runs; documented in EC3.

### 2.6 Streaming dial options

`streaming/internal/grpcclient/mtls.go`:

```go
func DialAPI(ctx context.Context, addr string, certPath string, loopback bool) (*grpc.ClientConn, error) {
    if loopback {
        // EC: allowed in single-host all-loopback topology. Documented threat
        // model: only "another process on the same host" can talk; that
        // process is already trusted.
        return grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    }
    cert, err := tls.LoadX509KeyPair(filepath.Join(certPath, "cert.pem"),
                                     filepath.Join(certPath, "key.pem"))
    if err != nil { return nil, err }
    pool := x509.NewCertPool()
    caBytes, _ := os.ReadFile(filepath.Join(certPath, "ca.pem"))
    pool.AppendCertsFromPEM(caBytes)
    creds := credentials.NewTLS(&tls.Config{
        Certificates: []tls.Certificate{cert},
        RootCAs:      pool,
        MinVersion:   tls.VersionTLS12,
    })
    return grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(creds))
}
```

`loopback` is detected on startup: if all services resolve to a
`127.0.0.0/8` address and the boot config has `internal_mtls=auto` or
`off`, the insecure path is used and a startup warning logs. Setting
`internal_mtls=on` always requires mTLS regardless of topology
(production hardening).

### 2.7 Native client cert-pin

`web/src/lib/tls-pin.ts` (used only on Capacitor / Tauri; the browser
trusts the OS root store):

```ts
import { Capacitor } from '@capacitor/core';
import { Preferences } from '@capacitor/preferences';

const KEY = 'maktaba.tls-pin';

export async function pinIssuerOnFirstConnect(host: string, fingerprint: string) {
    const stored = (await Preferences.get({ key: KEY })).value;
    if (!stored) {
        await Preferences.set({ key: KEY, value: JSON.stringify({ host, fingerprint }) });
        return { trustOnFirstUse: true };
    }
    const got = JSON.parse(stored);
    if (got.host !== host || got.fingerprint !== fingerprint) {
        // Captive-portal-style downgrade or active MITM.
        return { trustOnFirstUse: false, mismatch: true };
    }
    return { trustOnFirstUse: false };
}
```

The mobile / desktop app refuses to send the password if `mismatch ===
true`; the user is shown a "this device's saved certificate doesn't
match — likely network attack" dialog (EC3).

## 3. Test plan

### 3.1 Cipher floor (TC1)

| Test | What it pins |
|---|---|
| `TestNmapNoWeak` | `tools/tls-doctor.sh` against the running Caddy reports zero `weak` and zero `broken` lines. |
| `TestTLSv11Refused` | A `curl --tlsv1.1` request fails the handshake. |
| `TestSSLv3Refused` | OpenSSL `s_client -ssl3` refuses to connect. |

### 3.2 HSTS (TC2)

| Test | What it pins |
|---|---|
| `TestHSTSPresentByDefault` | Fresh load returns `Strict-Transport-Security: max-age=31536000; includeSubDomains`. |
| `TestHSTSDisabled` | `MAKTABA_DISABLE_HSTS=true` removes the header. |
| `TestHSTSOnlyOverHttps` | Plain-HTTP responses (Caddy 80 → 443 redirect) do not include the header (per RFC). |

### 3.3 mTLS (TC3)

| Test | What it pins |
|---|---|
| `TestMtlsHandshake` | Streaming dials API with a valid cert; gRPC connection established. |
| `TestMtlsCertMismatchRefused` | Streaming with a self-signed cert → handshake fails with a clear "bad certificate" error in logs. |
| `TestLoopbackBypassesAndWarns` | All services on `127.0.0.1` and `internal_mtls=auto` → log line `LOOPBACK_TLS_BYPASS_ACTIVE` once on startup; gRPC plaintext works. |
| `TestLoopbackForcedMtls` | `internal_mtls=on` while loopback-only → mTLS still required; bootstrap proceeds. |
| `TestLeafCertExpiryRotates` | Set leaf TTL to 5 s; daemon re-issues; gRPC connection survives (no dropped requests). |

### 3.4 Native cert-pin (EC3)

| Test | What it pins |
|---|---|
| `TestPinTrustOnFirstUse` | First connect stores fingerprint; second connect with same fingerprint → no prompt. |
| `TestPinMismatchRefusesCreds` | A different fingerprint → app refuses to POST `/api/auth/login`; UI shows downgrade warning. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Self-signed cert on fresh install (EC1) | Mac compose path uses Caddy local-CA which is trusted via `caddy trust` on first run; mobile/desktop apps go through trust-on-first-use; web (browser) requires the user to import the cert manually (documented). | `TestSelfSignedAcceptedByApps` |
| LE rate limit (EC2) | Caddy retries with backoff (built-in); `/api/health` returns alive but `ready=false` until cert acquired. The compose healthcheck for caddy waits up to 5 min before flagging unhealthy. | `TestCaddyAcmeBackoff` |
| Captive-portal downgrade (EC3) | Native cert-pin refuses to send credentials. Web/browser path: HSTS prevents downgrade after first visit. | `TestPinMismatchRefusesCreds` |
| Operator runs Caddy behind another reverse proxy | Documented: the outer proxy must terminate TLS; Caddy then runs on `:80` internal-only. The mTLS path between API/streaming/pipeline is unaffected. | n/a |
| OCSP stapling fetch fails | Caddy falls back to "must-staple-not-stapled"; clients warn but the connection still completes. | `TestOcspFallback` |
| Renewal mid-burst | Caddy renews on background goroutine; in-flight TLS sessions complete on the old cert; new sessions use the new one. | n/a |
| HTTP/2 downgrade attack | ALPN list is `h2 http/1.1`; HTTP/0.9 / HTTP/1.0 refused. | `TestAlpnRestricted` |
| `internal_mtls=auto` with mixed topology (some loopback, some not) | The mode is conservative: any non-loopback peer forces mTLS for all peers. Documented. | `TestMixedTopologyForcesMtls` |
| Bootstrap token leaked | Token is in-memory + printed once; it expires on next API restart. Compromise window is bounded; documented in ops. | n/a |
| Internal CA private key leaked | Rotation: bump the JWT signing key (Story 23.1) — the CA-rotation daemon ties to that. All leaves expire within 24 h naturally. | `TestCaRotationFlushesLeaves` |
| Pinned fingerprint after a server-cert rotation | The native app prompts the user to re-pin; this is intentional friction to surface unexpected cert rotations. | UX flow |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `caddy` | 2.x | TLS termination. |
| `crypto/tls`, `crypto/x509`, `crypto/rsa` | stdlib | mTLS + CA. |
| `google.golang.org/grpc` | latest | gRPC creds. |
| `nmap` (test only) | 7.x+ | TLS profile audit. |
| `@capacitor/preferences` | latest | Native pin storage. |

## 6. Acceptance checklist

**TLS profile**
- [ ] TLS 1.2 minimum; 1.3 preferred.
- [ ] Modern cipher list; nmap reports zero weak/broken.
- [ ] OCSP stapling on; ALPN `h2 http/1.1`.

**HSTS**
- [ ] Default-on with `max-age=31536000; includeSubDomains`.
- [ ] Opt-out via `MAKTABA_DISABLE_HSTS=true`.

**mTLS**
- [ ] Internal CA persisted in DB; leaf TTL 24 h.
- [ ] Streaming and pipeline obtain certs via the bootstrap dispense flow.
- [ ] Loopback bypass documented; logs banner on startup.
- [ ] `internal_mtls=on` forces mTLS regardless of topology.

**Native pin**
- [ ] Trust-on-first-use stores issuer fingerprint.
- [ ] Mismatch refuses credentials; surfaces UI warning.
