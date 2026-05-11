# Implementation Plan — Story 25.18 APNs dispatcher

> Companion to [story-25-18-apns-dispatcher.md](story-25-18-apns-dispatcher.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Where | `--role=worker`. Long-running drainer for `cloud_push_outbox` rows with iOS/iPadOS/tvOS device platforms. |
| Lib | `github.com/sideshow/apns2` (mature; supports HTTP/2, persistent conn, JWT auth, sandbox+prod). |
| JWT | ES256 signed with `.p8` private key; cached and rotated proactively (50 min). |
| Topics | Bundle id selected from `cloud_devices.app_bundle_id` (or fallback config default per platform). |
| Concurrency | 200 in-flight per worker; HTTP/2 supports 100/conn so we maintain 2-3 conns. |
| Out of scope | Template rendering (25.17 already produced title/body). Outbox claim (25.17 worker pattern). |

## 1. APNs client

```go
// cloud/internal/push/apns.go
type APNsDispatcher struct {
    jwt       *AppleJWT       // ES256 minter
    prod      *apns2.Client
    sandbox   *apns2.Client
    bundles   map[string]string  // platform -> bundle id default
    repo      OutboxRepo
    deviceRepo DeviceRepo
    metrics   *APNsMetrics
}

func NewAPNs(cfg APNsConfig, repo OutboxRepo, dRepo DeviceRepo) (*APNsDispatcher, error) {
    keyBytes, err := os.ReadFile(cfg.KeyPath); if err != nil { return nil, err }
    token := &token.Token{
        AuthKey: parseP8(keyBytes),
        KeyID:   cfg.KeyID,
        TeamID:  cfg.TeamID,
    }
    prod := apns2.NewTokenClient(token).Production()
    sb   := apns2.NewTokenClient(token).Development()
    return &APNsDispatcher{prod: prod, sandbox: sb, bundles: cfg.Bundles, repo: repo, deviceRepo: dRepo}, nil
}
```

`apns2` handles HTTP/2 conn pooling and JWT minting (1h cap) internally.

## 2. Send

```go
func (a *APNsDispatcher) Send(ctx context.Context, row OutboxJoinedRow) {
    payload := payload.NewPayload().
        AlertTitle(row.Title).AlertBody(row.Body).
        Badge(0).Sound("default").
        ThreadID(row.Kind).
        Custom("maktaba_kind", row.Kind).
        Custom("maktaba_ref_id", row.RefID).
        Custom("maktaba_server_id", row.ServerID.String())

    bundle := row.AppBundleID
    if bundle == "" { bundle = a.bundles[row.Platform] }

    n := &apns2.Notification{
        DeviceToken: a.decryptToken(row.TokenSealed),
        Topic:       bundle,
        Payload:     payload,
        Priority:    row.Priority,
        CollapseID:  row.APNsCollapseID,
        Expiration:  row.NotAfter,
    }
    if row.Kind == "system.error" {
        n.PushType = apns2.PushTypeAlert
        payload.Custom("apns-interruption-level", "time-sensitive")
    }

    var client *apns2.Client
    if row.Environment == "sandbox" { client = a.sandbox } else { client = a.prod }

    res, err := client.PushWithContext(ctx, n)
    a.metrics.Requests.With(prometheus.Labels{"result": a.label(res, err)}).Inc()
    if err != nil { a.fail(ctx, row, err.Error(), retryable); return }
    switch res.StatusCode {
    case 200:
        a.repo.MarkDispatched(ctx, row.ID)
    case 410:        // Unregistered
        a.deviceRepo.Revoke(ctx, row.DeviceID, "apns_unregistered")
        a.repo.MarkFailed(ctx, row.ID, "unregistered")
    case 400:        // BadDeviceToken or PayloadTooLarge
        if res.Reason == apns2.ReasonBadDeviceToken { a.deviceRepo.Revoke(ctx, row.DeviceID, "apns_bad_token") }
        a.repo.MarkFailed(ctx, row.ID, res.Reason)
    case 403:
        a.repo.MarkFailed(ctx, row.ID, res.Reason)
        // Likely cert / config; operator alert.
    case 429:
        a.backoff(ctx, row)
    case 500, 503:
        a.retryLater(ctx, row)
    default:
        a.repo.MarkFailed(ctx, row.ID, fmt.Sprintf("%d:%s", res.StatusCode, res.Reason))
    }
}
```

## 3. Backoff / retry

`retryLater` increments `retries`, sets `dispatched_at=NULL`, clears `in_flight_lock` so the next drain picks it up after a per-row delay computed from `retries`:

```go
func backoffDelay(retries int) time.Duration {
    base := time.Duration(1<<min(retries, 6)) * time.Second
    return base + time.Duration(rand.Intn(int(base/2)))
}
```

After 3 retries, mark `failed`.

## 4. JWT cache

We rely on apns2's built-in JWT cache (mints + uses for 50 minutes). We *also* observe the token age via a counter `apns_jwt_minted_total` and add a watchdog that force-reloads the `.p8` on SIGUSR1 (for rotation).

## 5. Concurrency

```go
func (a *APNsDispatcher) Run(ctx context.Context, drainer *Drainer) {
    sem := make(chan struct{}, 200)
    drainer.OnRow(func(row OutboxJoinedRow) {
        sem <- struct{}{}
        go func() { defer func(){ <-sem }(); a.Send(ctx, row) }()
    })
}
```

## 6. Per-bundle routing

iOS user-facing notifications: `app.maktaba.cloud`. tvOS: `app.maktaba.cloud.tv`. iPadOS shares iOS bundle.

Config:

```toml
[apns]
team_id  = ""
key_id   = ""
key_path = "/var/run/secrets/apns/AuthKey_KEYID.p8"
ios_bundle    = "app.maktaba.cloud"
tvos_bundle   = "app.maktaba.cloud.tv"
```

`a.bundles = {"ios": cfg.IOSBundle, "ipad": cfg.IOSBundle, "tvos": cfg.TVOSBundle}`.

## 7. Test plan

### 7.1 Unit

| Test | Pins |
|---|---|
| `TestParseP8` | Valid key parses; junk → error. |
| `TestRoutingPerBundle` | tvos → tvos bundle; ios → ios bundle. |
| `TestPayloadShape` | aps.alert.title|body + custom maktaba_kind present. |
| `TestPushTypeForSystemError` | time-sensitive set on system.error. |
| `TestTTLExpiredSkipped` | NotAfter < now → skipped before send. |

### 7.2 Integration (mock APNs over HTTP/2)

| Test | Pins |
|---|---|
| `Test200MarksDispatched` | row.dispatched_at set. |
| `Test410Revokes` | device.revoked_at = now. |
| `Test400BadDeviceTokenRevokes` | same. |
| `Test429BackoffRetries` | retry counter increments; eventually succeeds. |
| `Test500RetryUpTo3` | After 3 fails → failed. |
| `TestConcurrent200Rows` | 200 succeed; in-flight cap 200. |
| `TestSandboxRouting` | environment=sandbox → sandbox client used. |
| `TestPayloadTruncated` | Oversize from 25.17 already truncated; defense-in-depth at apns2 layer. |
| `TestJWTRotationOnSIGUSR1` | Touch signal; new JWT minted. |
| `TestConnectionRecovery` | Server resets H2 stream → next request reconnects, retries. |

## 8. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Sandbox vs prod | tag in `cloud_devices.environment`. | `TestSandboxRouting`. |
| collapse-id | mapped from `dedupe_key` (≤64B). | Implementation. |
| Background pushes | `content-available=1` opt-in by kind (system.status). | Implementation. |
| Critical alerts | Off by default; no entitlement. | Spec. |
| Time-sensitive | Only system.error. | Implementation. |
| Region | Single global endpoint. | Spec. |
| `apns-push-type=alert` required iOS 13+ | Set unconditionally. | Implementation. |
| Sound | "default"; respect user OS settings. | Spec. |
| Multiple devices, no multicast | Per-device send. | Spec. |
| APNs outage | Outbox grows; alert > 10k pending. | Metric/alert. |
| Connection-level reset | apns2 reconnects; we retry. | `TestConnectionRecovery`. |
| Locale | Pre-localized at ingest (25.17). | Spec. |

## 9. Dependencies

- 25.17 (outbox model, devices, templates).
- 25.1 (config, metrics, secrets file mount).

## 10. Acceptance checklist

- [ ] APNs client with ES256 JWT; sandbox + prod.
- [ ] Per-bundle routing.
- [ ] Permanent failures → device revoked.
- [ ] Retry with backoff; 3-attempt cap.
- [ ] Concurrency cap 200.
- [ ] Metrics `apns_sent_total{result}`, `apns_request_duration_seconds`, `apns_jwt_minted_total`.
- [ ] Tests in §7 pass.
