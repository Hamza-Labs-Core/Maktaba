# Implementation Plan — Story 25.23 TLS at the edge (wildcard ACME)

> Companion to [story-25-23-tls-edge.md](story-25-23-tls-edge.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| ACME library | `github.com/go-acme/lego/v4`. |
| Challenge | DNS-01 via Cloudflare API (token scoped `Zone.DNS:Edit` for `maktaba.app` and `maktaba.cloud` zones). |
| Storage | Postgres `cloud_tls_certs` table; key sealed with KMS data key. |
| Process | Issuance + rotation in `--role=worker` (cron-driven). |
| Reload | LB and relay reload on `SIGHUP` after a fresh cert lands. |
| Fallback CA | ZeroSSL; one-line flag swap. |
| Ciphers | TLS 1.2 AEAD-only + TLS 1.3; HSTS preload. |
| Out of scope | Per-user cert pinning. HTTP/3. RSA backup keypair (v2). |

## 1. Migration `00070002_tls_certs.sql` (slot 0007 extension)

```sql
-- +goose Up
CREATE TABLE cloud_tls_certs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host            TEXT NOT NULL,         -- '*.maktaba.app' | 'maktaba.app' | '*.maktaba.cloud'
    issuer          TEXT NOT NULL,         -- 'letsencrypt' | 'zerossl'
    fullchain_pem   TEXT NOT NULL,
    key_pem_sealed  BYTEA NOT NULL,
    issued_at       TIMESTAMPTZ NOT NULL,
    not_before      TIMESTAMPTZ NOT NULL,
    not_after       TIMESTAMPTZ NOT NULL,
    superseded_at   TIMESTAMPTZ
);
CREATE INDEX cloud_tls_certs_active_idx ON cloud_tls_certs(host, not_after) WHERE superseded_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS cloud_tls_certs;
```

## 2. ACME orchestrator

```go
// cloud/internal/tls/acme.go
type Orchestrator struct {
    cfDNS   *cloudflare.API
    accounts map[string]*lego.Account      // 'letsencrypt', 'zerossl'
    repo    *Repo
    sealer  Sealer
    clock   clock.Clock
}

func (o *Orchestrator) IssueIfNeeded(ctx context.Context, host string) error {
    cur, _ := o.repo.Active(ctx, host)
    if cur != nil && cur.NotAfter.Sub(o.clock.Now()) > 30*24*time.Hour {
        return nil
    }
    client := o.lego(o.activeIssuer())
    req := certificate.ObtainRequest{
        Domains: []string{host},
        Bundle:  true,
    }
    res, err := client.Certificate.Obtain(req)
    if err != nil {
        // Try fallback
        if o.activeIssuer() == "letsencrypt" {
            client = o.lego("zerossl")
            res, err = client.Certificate.Obtain(req)
        }
        if err != nil { return err }
    }
    sealed, err := o.sealer.Seal(res.PrivateKey)
    if err != nil { return err }
    return o.repo.Insert(ctx, host, "letsencrypt", res.Certificate, sealed, time.Now(), parseNotAfter(res.Certificate))
}
```

`o.lego(issuer)` returns a lego client wired with the Cloudflare DNS-01 challenge provider:

```go
cfg := lego.NewConfig(account)
cfg.CADirURL = issuerURL[issuer]
cfg.Certificate.KeyType = certcrypto.EC256
cli, _ := lego.NewClient(cfg)
prov, _ := cloudflareDNS01.NewDNSProvider()
cli.Challenge.SetDNS01Provider(prov)
```

`CLOUDFLARE_DNS_API_TOKEN` env var is read by the lego provider. Rotation: ops update Kubernetes/systemd secret + restart worker pod.

## 3. Rotation cron

```go
// cloud/internal/jobs/tls_rotate.go
func RotateCron(ctx context.Context, o *Orchestrator) error {
    t := time.NewTicker(6 * time.Hour); defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return ctx.Err()
        case <-t.C:
            for _, host := range []string{"*.maktaba.app", "maktaba.app", "*.maktaba.cloud"} {
                if err := o.IssueIfNeeded(ctx, host); err != nil {
                    page(ctx, host, err)   // PagerDuty path on failure
                }
            }
            o.reload(ctx)
        }
    }
}
```

## 4. Reload

On insert, a Postgres LISTEN `tls_renewed` is published; LB/relay pods subscribe and reload their `tls.Config.GetCertificate` callback that reads from a sync.Map keyed by host.

```go
// cloud/internal/tls/loader.go
type CertStore struct{ certs sync.Map }   // host → *tls.Certificate

func (s *CertStore) GetCertificate(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
    if c, ok := s.certs.Load(matchHost(hi.ServerName)); ok {
        return c.(*tls.Certificate), nil
    }
    return nil, errors.New("no cert")
}
```

In-flight TLS sessions complete on the old cert (`GetCertificate` is per-handshake), new ones use the new.

## 5. TLS posture

```go
func TLSConfig(store *CertStore) *tls.Config {
    return &tls.Config{
        MinVersion: tls.VersionTLS12,
        CipherSuites: []uint16{
            tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
            tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
            tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
            tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
            tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
        },
        CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
        NextProtos:        []string{"h2", "http/1.1"},
        GetCertificate:    store.GetCertificate,
    }
}
```

HSTS middleware on every response: `Strict-Transport-Security: max-age=31536000; includeSubDomains; preload`.

## 6. OCSP stapling

Lego returns a `lego.Resource` we can feed to `golang.org/x/crypto/ocsp`; we staple via the standard lib through `tls.Certificate.OCSPStaple`. Refreshed every 12h.

## 7. CT monitoring

A weekly job hits `crt.sh` for `%.maktaba.app` and `%.maktaba.cloud`; diffs against our `cloud_tls_certs` issuer list; alerts on unknown certs.

```go
func CTSentinel(ctx context.Context) {
    body, _ := http.Get("https://crt.sh/?q=%25.maktaba.app&output=json")
    // diff against cloud_tls_certs.serial
    // alert if unknown serial found
}
```

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestParseSAN` | Wildcard SAN extracted. |
| `TestTLSConfigMinVersion12` | TLS 1.0/1.1 rejected at config. |
| `TestHSTSHeaderShape` | Exact header value. |

### 8.2 Integration (LE staging)

| Test | Pins |
|---|---|
| `TestACMEIssueWildcardStaging` | LE staging → cert stored. |
| `TestRotateAt29Days` | Cert 29d old → renewal triggered. |
| `TestZeroSSLFallbackOnLEFailure` | LE down → ZeroSSL issues. |
| `TestReloadOnSIGHUP` | New cert in DB; pod reload swaps. |
| `TestInflightSessionsKeepOldCert` | In-flight session not disturbed. |
| `TestOCSPStaplePresent` | Handshake includes OCSP. |
| `TestCTSentinelDetectsRogue` | Unknown serial → alert metric. |
| `TestCloudflareTokenRotation` | New token in env → next renewal succeeds. |

### 8.3 Regression

| Test | Pins |
|---|---|
| `TestRejectTLS10` | Handshake fails. |
| `TestRejectWeakCipher` | CBC-only client → fail. |
| `TestPreloadHSTSValueImmutable` | Cannot lower max-age without conscious operator action. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| HSTS preload | Cannot be undone short-term; documented operational risk. | Doc. |
| CT logs | Sentinel scans for rogue issuance. | `TestCTSentinelDetectsRogue`. |
| must-staple | Not set in CSR (LE flakiness). | Spec. |
| Key compromise | Sealed in PG with KMS data key; rotation runbook. | Doc. |
| CF token scope | Zone.DNS:Edit only. | Doc. |
| `*.maktaba.app` does not cover `a.b.maktaba.app` | We never use 2-level subdomains. | Spec. |
| Cert size | ECDSA ~700B; fine. | Spec. |
| Old client compatibility | LE ISRG Root X2 + cross-sign. | Doc. |
| Renewal failure 3x | Page on-call. | Cron metric. |

## 10. Dependencies

- 25.1.
- 25.22 (subdomain coverage; the wildcard cert is single-handedly enough).
- Outside-scope ops: Cloudflare token rotation runbook in `docs/operations/`.

## 11. Acceptance checklist

- [ ] Migration 00070002 applies.
- [ ] ACME wildcard issuance against staging works.
- [ ] Rotation cron at 6h; renewal at <30d remaining.
- [ ] Fallback to ZeroSSL one-line flag.
- [ ] TLS config: 1.2+1.3, AEAD-only, HSTS preload.
- [ ] OCSP staple.
- [ ] CT sentinel.
- [ ] Tests in §8 pass.
