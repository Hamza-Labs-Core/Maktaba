# Implementation Plan — Story 12.11 Downloaded-Flag Sync API

> Companion to [story-12-11-downloaded-flag-api.md](story-12-11-downloaded-flag-api.md).
> Server-side metadata only — the file lives on the device.
> Auth context **must** be a device session (refresh-token-bound or device PAT); web cookie sessions are rejected.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration | `shared/db/migrations/0041_device_downloads.sql` (Postgres) + `0041_device_downloads.sqlite.sql`. Sequential after Story 12.10's `devices`. |
| Endpoints | `api/internal/http/device_downloads.go`. Routes under `/api/videos/{video_id}/downloaded`. |
| GraphQL | Adds `downloads: [DeviceDownload!]!` to the `Video` type in `shared/graphql/schema.graphql`. |
| Auth gate | Endpoint reads `IdentityFrom(r)`; if `Source != 'refresh' && Source != 'device-pat'`, 403 `not-a-device-session`. |
| Out of scope | Local download mechanics (Story 12.6); device registry (Story 12.10). |

## 1. Schema

```sql
CREATE TABLE device_downloads (
  device_id      UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  video_id       UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  quality        TEXT NOT NULL CHECK (quality IN ('audio','480p','720p','1080p')),
  bytes          BIGINT NOT NULL CHECK (bytes >= 0),
  downloaded_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_played_at TIMESTAMPTZ,
  pinned         BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (device_id, video_id)
);

CREATE INDEX device_downloads_video_idx ON device_downloads (video_id);
```

## 2. Identity → device resolution

```go
func deviceIDFromIdentity(ident auth.Identity) (string, error) {
    switch ident.Source {
    case "refresh":
        // refresh tokens are bound to devices via refresh_tokens.device_id
        if ident.DeviceID == "" { return "", ErrNotDeviceSession }
        return ident.DeviceID, nil
    case "device-pat":
        // a PAT created with category=device carries DeviceID directly
        return ident.DeviceID, nil
    default:
        return "", ErrNotDeviceSession
    }
}
```

`refresh_tokens.device_id` is added by Epic 10 Story 10.3 — we depend on it being present (REVIEW §1.1.h). If absent in v1, this plan adds the column under the same migration prefixed with a backfill comment.

## 3. Endpoints

### `POST /api/videos/{video_id}/downloaded`

```go
func (h *Handler) Upsert(w http.ResponseWriter, r *http.Request) {
    deviceID, err := deviceIDFromIdentity(auth.IdentityFrom(r))
    if err != nil { problem.Write(w, 403, "not-a-device-session"); return }

    var p UpsertPayload
    if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
        problem.Write(w, 400, "bad-request"); return
    }
    videoID := chi.URLParam(r, "video_id")
    res, err := h.svc.Upsert(r.Context(), deviceID, videoID, p)
    if err != nil { /* map errors */ return }
    if res.Created { w.WriteHeader(201) } else { w.WriteHeader(200) }
    json.NewEncoder(w).Encode(res)
}
```

```sql
INSERT INTO device_downloads (device_id, video_id, quality, bytes, pinned)
VALUES ($1,$2,$3,$4, COALESCE($5,false))
ON CONFLICT (device_id, video_id)
DO UPDATE SET quality = EXCLUDED.quality, bytes = EXCLUDED.bytes,
              pinned = COALESCE(EXCLUDED.pinned, device_downloads.pinned),
              downloaded_at = now()
RETURNING (xmax = 0) AS created;
```

### `DELETE /api/videos/{video_id}/downloaded`

Idempotent: 204 even if no row.

### `GET /api/videos/{video_id}/downloaded`

Returns rows scoped to the caller's user (i.e., devices owned by the same `user_id`). Joined to `devices` for `device_label`, `platform`.

### `PATCH /api/videos/{video_id}/downloaded`

Body `{ pinned: bool }`; updates only the caller's row.

## 4. GraphQL

```graphql
type DeviceDownload {
  device: Device!
  quality: String!
  bytes: Int!
  downloadedAt: DateTime!
  lastPlayedAt: DateTime
  pinned: Boolean!
}

extend type Video {
  downloads: [DeviceDownload!]!
}
```

Resolver pulls all rows for `video_id` filtered to devices owned by the current user.

## 5. Reconciliation flow (Story 12.6 calls)

After install or sync, the client lists local files and DELETEs rows for files no longer present. Endpoint shape unchanged; client orchestration. The server can also expose a convenience endpoint `GET /api/me/devices/{device_id}/downloads` for bulk read; included here:

```go
// Returns []DeviceDownloadRow for the calling user's device id (must match)
```

## 6. Edge cases

| Case | Handling |
|---|---|
| Video deleted server-side | Cascade removes rows; clients see badge disappear next sync. |
| Two qualities downloaded | Per `(device_id, video_id)` PK, only one quality at a time; switching quality replaces the row. |
| POST from non-device session | 403 `not-a-device-session`. |
| Revoked device | Row preserved until device is hard-deleted; UI shows "Last seen N days ago". |

## 7. Test cases

### 7.1 Unit (Go)

| Test | Asserts |
|---|---|
| `non-device session 403` | Cookie session calls POST → 403. |
| `upsert created vs updated` | First call returns Created=true; second Created=false. |
| `pinned defaults preserved` | PATCH with `{pinned:true}` sticks; subsequent POST without pinned keeps true. |
| `delete idempotent` | Two DELETEs in a row both 204. |
| `list scoped to caller's devices` | Cross-user rows hidden. |

### 7.2 Integration

- Two devices, two qualities: `GET .../downloaded` returns 2 rows.
- iPhone POST then DELETE; iPad GraphQL `downloads` field reflects within one fetch.
- Cascade: delete video → both device_downloads rows gone.

## 8. Performance

- All endpoints O(1) on the PK / scoped index.
- GraphQL N+1 avoided by a single join in the resolver.

## 9. Dependencies

- Story 12.10 (devices table).
- Epic 10 Story 10.3 (refresh_tokens carries `device_id`).
- Story 12.6 invokes these endpoints.
