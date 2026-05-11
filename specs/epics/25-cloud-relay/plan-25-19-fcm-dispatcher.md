# Implementation Plan — Story 25.19 FCM dispatcher (Android + Web)

> Companion to [story-25-19-fcm-dispatcher.md](story-25-19-fcm-dispatcher.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Where | `--role=worker`, sibling to APNs (25.18). |
| Lib | Direct HTTP/2 calls to FCM HTTP v1 (`google.golang.org/api/transport/http` for OAuth; no full Firebase Admin SDK to keep image small). |
| Auth | Service account JSON; cache OAuth access token for 55min; refresh on SIGUSR1 or natural expiry. |
| Concurrency | 100 in-flight (HTTP/2 limit). |
| Web push | Per-device send via FCM webpush; click_action = deep link. Safari out of v1. |
| Out of scope | Topic broadcasting (unused in v1). Data-only silent messages (used internally by app sync, deliverable but no UI). |

## 1. FCM client

```go
// cloud/internal/push/fcm.go
type FCMDispatcher struct {
    projectID   string
    httpClient  *http.Client       // OAuth-injected
    tokenSource oauth2.TokenSource
    repo        OutboxRepo
    deviceRepo  DeviceRepo
    metrics     *FCMMetrics
}

func NewFCM(cfg FCMConfig, repo OutboxRepo, dRepo DeviceRepo) (*FCMDispatcher, error) {
    data, err := os.ReadFile(cfg.ServiceAccountPath)
    if err != nil { return nil, err }
    creds, err := google.CredentialsFromJSON(context.Background(), data,
        "https://www.googleapis.com/auth/firebase.messaging")
    if err != nil { return nil, err }
    return &FCMDispatcher{
        projectID:   cfg.ProjectID,
        tokenSource: creds.TokenSource,
        httpClient:  oauth2.NewClient(context.Background(), creds.TokenSource),
        repo: repo, deviceRepo: dRepo,
    }, nil
}
```

## 2. Send

```go
func (f *FCMDispatcher) Send(ctx context.Context, row OutboxJoinedRow) {
    msg := fcmMessage{
        Message: fcmMessageBody{
            Token: f.decrypt(row.TokenSealed),
            Notification: fcmNotification{Title: row.Title, Body: row.Body},
            Data: map[string]string{
                "maktaba_kind": row.Kind,
                "maktaba_ref_id": row.RefID,
                "maktaba_server_id": row.ServerID.String(),
            },
        },
    }
    switch row.Platform {
    case "android", "androidtv":
        msg.Message.Android = &fcmAndroid{
            Priority: priorityFor(row.Priority),       // "HIGH" or "NORMAL"
            TTL:      fmt.Sprintf("%ds", int(time.Until(row.NotAfter).Seconds())),
            Notification: &fcmAndroidNotification{ChannelID: row.ChannelID, Tag: row.FCMTag},
        }
    case "web":
        msg.Message.Webpush = &fcmWebpush{
            Notification: &fcmWebpushNotification{Title: row.Title, Body: row.Body},
            FcmOptions:   &fcmWebpushFcmOptions{Link: deepLinkFor(row)},
            Headers:      map[string]string{"TTL": fmt.Sprintf("%d", int(time.Until(row.NotAfter).Seconds()))},
        }
    }

    body, _ := json.Marshal(msg)
    url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", f.projectID)
    req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    resp, err := f.httpClient.Do(req)
    if err != nil { f.fail(ctx, row, "network", true); return }
    defer resp.Body.Close()
    switch resp.StatusCode {
    case 200:
        f.repo.MarkDispatched(ctx, row.ID)
    case 404:
        f.deviceRepo.Revoke(ctx, row.DeviceID, "fcm_unregistered")
        f.repo.MarkFailed(ctx, row.ID, "unregistered")
    case 400:
        f.repo.MarkFailed(ctx, row.ID, "invalid_argument")
    case 403:
        f.repo.MarkFailed(ctx, row.ID, "sender_mismatch")
        f.metrics.OperatorAlerts.Inc()       // PagerDuty downstream
    case 429:
        f.backoff(ctx, row)
    case 500, 503:
        f.retryLater(ctx, row)
    default:
        f.repo.MarkFailed(ctx, row.ID, fmt.Sprintf("%d", resp.StatusCode))
    }
}
```

## 3. Deep link

```go
func deepLinkFor(row OutboxJoinedRow) string {
    switch row.Kind {
    case "library.video_ready", "download.complete":
        return "https://app.maktaba.app/r/" + row.RefID
    default:
        return "https://app.maktaba.app/"
    }
}
```

## 4. OAuth refresh

`oauth2.NewClient` automatically refreshes; we additionally expose `f.tokenSource.Token()` for explicit checks. SIGUSR1 reloads the service-account file (atomic-rename swap), recreating the client. Documented in runbook.

## 5. Re-registration

When a device that was previously revoked reactivates (story EC), `cloud_devices.UpsertDevice` (25.17) clears `revoked_at`. The next push for that device works.

## 6. Test plan

### 6.1 Unit

| Test | Pins |
|---|---|
| `TestServiceAccountParse` | Valid JSON loads; missing fields → error. |
| `TestPriorityMapping` | priority=10 → "HIGH"; priority=5 → "NORMAL". |
| `TestDeepLinkPerKind` | `library.video_ready` → `/r/<ref>`. |
| `TestPayloadShapeAndroid` | Channel ID present. |
| `TestPayloadShapeWeb` | Notification + FCMOptions.Link present. |
| `TestTTLComputation` | NotAfter→seconds. |

### 6.2 Integration (mock FCM)

| Test | Pins |
|---|---|
| `Test200MarksDispatched` | Happy path. |
| `Test404Revokes` | Device revoked. |
| `Test403Alert` | Operator alert metric. |
| `Test429Backoff` | Retry honored. |
| `TestOAuthRefresh` | After 1h, fresh token used. |
| `TestConcurrent100Rows` | Cap respected. |
| `TestSIGUSR1ReloadsCreds` | Touch signal → new token source. |
| `TestReactivationAfterRevoke` | Re-register clears `revoked_at`. |

## 7. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Service account rotation | File swap + SIGUSR1. | Doc. |
| Web push VAPID | FCM handles for Chromium. | Spec. |
| Safari web push | Out for v1. | Spec. |
| Android channels | Mismatched channel → silent; documented. | Spec. |
| Topic broadcast | Not used in v1. | Spec. |
| Data-only messages | Used internally for sync. | Spec. |
| Token format collision | Gated by `platform` column. | Spec. |
| Notification grouping (`tag`) | Set to `dedupe_key`. | Implementation. |

## 8. Dependencies

- 25.17.
- 25.1 (config + secrets).

## 9. Acceptance checklist

- [ ] FCM client with service-account OAuth.
- [ ] HTTP v1 `messages:send` endpoint.
- [ ] Android + Web variants.
- [ ] Permanent failures revoke device.
- [ ] Retry with backoff; 3-attempt cap.
- [ ] Concurrency cap 100.
- [ ] Metrics `fcm_sent_total{result}` and operator alert metric.
- [ ] Tests in §6 pass.
