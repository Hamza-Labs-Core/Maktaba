# Story 12.11 — API: per-device "downloaded" flag sync

**Status:** **NEW** — added in response to
[REVIEW §3.4](../../REVIEW.md): Story 12.6 promised "marking a video
downloaded sets a server-side flag" but no endpoint existed. This story
owns it.

When the user has downloaded a video to a device for offline viewing
([Story 12.6](story-12-06-offline-downloads.md)), the server records
which device(s) currently hold the video so that the same user on a
different device can see "Downloaded on iPhone" badges without having to
discover that out-of-band.

## AC

### Schema

- New table `device_downloads`:
  - `device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE`
    (linked to [Story 12.10](story-12-10-device-registration-api.md))
  - `video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE`
  - `quality TEXT NOT NULL` (`audio`, `480p`, `720p`, `1080p`)
  - `bytes BIGINT NOT NULL` (size of the downloaded file)
  - `downloaded_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `last_played_at TIMESTAMPTZ`
  - `pinned BOOLEAN NOT NULL DEFAULT false`
  - `PRIMARY KEY (device_id, video_id)`
- Index `(video_id)` for fast cross-device "who has this" lookups.
- Migration owner: this story.

### Endpoints

- `POST /api/videos/{video_id}/downloaded {quality, bytes, pinned?}` →
  `201` or `200` (upserts the row for the caller's device);
  `device_id` is derived from the auth context (the device's PAT or
  refresh-token-bound session). If the auth context isn't a device
  session, return `403 not-a-device-session`.
- `DELETE /api/videos/{video_id}/downloaded` → `204` (deletes the row
  for the caller's device); idempotent.
- `GET /api/videos/{video_id}/downloaded` → list of devices that have
  this video downloaded (visible only to the owning user; for shared
  multi-user libraries, only the caller's own devices show).
- `PATCH /api/videos/{video_id}/downloaded {pinned}` → toggle pin.

### Behavior

- The video-detail GraphQL `Video` type gains a
  `downloads: [DeviceDownload!]!` field; clients use it to render
  "Downloaded on iPhone" badges.
- If a device is revoked
  ([Story 12.10](story-12-10-device-registration-api.md)), its download
  rows are kept until the device is hard-deleted (preserves history); UI
  shows "Last seen 12 days ago" for revoked devices.
- The endpoint never moves bytes; it is metadata only. The actual file
  lives on the device.

## TC

- iPhone client downloads a video and POSTs `{quality: "720p", bytes:
  1_200_000_000}`: row created.
- Same user opens the video on the iPad: GraphQL `downloads` field
  returns one entry; UI shows "Downloaded on iPhone — 720p · 1.2 GB".
- iPhone deletes the local file and DELETEs the endpoint: row removed;
  iPad UI no longer shows the badge.
- Pin a download: subsequent downloads that would evict it skip it.

## EC

- The user reinstalls the app, losing local files: the next sync (e.g.,
  on next login) reconciles by listing local files and DELETEing the
  rows for files that no longer exist on the device.
- The video is deleted server-side: cascade removes all `device_downloads`
  rows; clients see the badge disappear after their next sync.
- Two devices download different qualities: both rows exist; UI shows
  both.
- A `POST` from a non-device session (e.g., a web cookie session): 403,
  preventing accidental "the web tab claims to have downloaded" rows.
