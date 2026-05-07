# Implementation Plan — Story 12.6 Offline Downloads

> Companion to [story-12-06-offline-downloads.md](story-12-06-offline-downloads.md).
> Server flag sync owned by [Story 12.11](story-12-11-downloaded-flag-api.md).
> Downloads are pause-resumable, encrypted at rest, survive app suspension.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Plugin | `@maktaba/download-manager` (`apps/mobile/plugins/download-manager/`). |
| iOS | `URLSession` with `backgroundSessionConfiguration` (allows background completion). |
| Android | WorkManager (constraint = unmetered by default) + DownloadManager (system download manager) for the byte transfer. |
| Storage | iOS app sandbox (encrypted by default); Android scoped storage `MediaStore.Downloads/Maktaba/` with file-level encryption via Keystore-derived AES-GCM key. |
| Server flag | `POST /api/videos/{id}/downloaded` (Story 12.11) on completion; `DELETE` on local removal. |
| Quota | Default 5 GB cap; LRU eviction; pin to prevent eviction. |
| Source format | **v1: direct-play (single-file) videos only.** HLS streams are NOT downloadable. The download affordance is hidden when the chosen variant would require HLS transcode. |
| Out of scope | Web download cache (Story 11.10 explicitly excludes video bytes); HLS offline (see §12 v2 follow-up). |

## 1. Data model

```ts
// apps/mobile/plugins/download-manager/src/definitions.ts
export type DownloadQuality = 'audio' | '480p' | '720p' | '1080p';

export interface DownloadItem {
  id: string;             // local UUID
  videoId: string;
  quality: DownloadQuality;
  state: 'queued'|'downloading'|'paused'|'completed'|'failed'|'cancelled';
  bytesTotal: number;
  bytesDone: number;
  pinned: boolean;
  filePath: string;       // local
  subtitlePath?: string;
  posterPath?: string;
  contentHashSha256: string;
  createdAt: string;
  completedAt?: string;
}

export interface DownloadManager {
  start(opts: { videoId: string; quality: DownloadQuality; directUrl: string; sidecarSubtitleUrls?: string[]; posterUrl?: string }): Promise<{ id: string }>;
  pause(opts: { id: string }): Promise<void>;
  resume(opts: { id: string }): Promise<void>;
  cancel(opts: { id: string }): Promise<void>;
  list(): Promise<DownloadItem[]>;
  remove(opts: { id: string }): Promise<void>;
  setPinned(opts: { id: string; pinned: boolean }): Promise<void>;
  addListener(event: 'progress'|'state', cb: (e: any) => void): PluginListenerHandle;
}
```

Local persistence uses a SQLite DB inside the sandbox (`downloads.db`) with the schema mirroring `DownloadItem`.

## 2. iOS implementation

```swift
// DownloadManager.swift
class DownloadManagerNative: NSObject, URLSessionDownloadDelegate {
    lazy var session: URLSession = {
        let cfg = URLSessionConfiguration.background(withIdentifier: "com.maktaba.app.downloads")
        cfg.allowsCellularAccess = UserDefaults.standard.bool(forKey: "allowCellularDownloads")
        return URLSession(configuration: cfg, delegate: self, delegateQueue: nil)
    }()

    // v1: directUrl points at a single-file MP4/MKV. URLSessionDownloadTask
    // downloads the whole file. HLS streams (.m3u8 + segments) are out of
    // scope for v1 — see §12 v2 follow-up.
    func start(videoId: String, quality: String, directUrl: URL) -> String {
        let id = UUID().uuidString
        let task = session.downloadTask(with: directUrl)
        task.taskDescription = id
        task.resume()
        store.insertItem(id: id, videoId: videoId, quality: quality, total: 0, state: "downloading")
        return id
    }

    func urlSession(_ s: URLSession, downloadTask: URLSessionDownloadTask,
                    didFinishDownloadingTo location: URL) {
        let id = downloadTask.taskDescription!
        let dst = sandboxPath(for: id)
        try? FileManager.default.moveItem(at: location, to: dst)
        verifyAndMark(id: id, file: dst)
    }
    // ... didWriteData → emit progress; resumeData on pause; resume(with:) on resume
}
```

The iOS sandbox is encrypted by default (Data Protection); we set `.completeFileProtection` on each download file.

## 3. Android implementation

`WorkManager` orchestrates; the actual byte transfer uses `DownloadManager` (which survives app death):

```kotlin
class DownloadWorker(ctx: Context, params: WorkerParameters): CoroutineWorker(ctx, params) {
    override suspend fun doWork(): Result {
        val url = inputData.getString("url") ?: return Result.failure()
        val id = inputData.getString("id") ?: return Result.failure()
        val request = DownloadManager.Request(Uri.parse(url))
            .setDestinationInExternalFilesDir(applicationContext, Environment.DIRECTORY_DOWNLOADS, "$id.bin")
            .setAllowedOverMetered(prefs.allowCellular)
            .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE)
        val dm = applicationContext.getSystemService<DownloadManager>()!!
        val systemId = dm.enqueue(request)
        store.markStarted(id, systemId)
        return pollUntilDone(dm, id, systemId)
    }
}
```

Encryption: after the file is downloaded, encrypt with AES-GCM keyed by `KeyStore.getKey("maktaba.downloads", null)` and store ciphertext at `MediaStore.Downloads/Maktaba/<id>.enc`. Plaintext only re-derived during playback (streamed via a `ContentProvider`).

## 4. Quota & eviction

```ts
async function enforceQuota(addBytes: number) {
  const cap = await getQuotaBytes();           // 5 GB default
  let used = (await list()).reduce((s, d) => s + d.bytesDone, 0);
  if (used + addBytes <= cap) return;
  for (const d of (await list()).filter(d => !d.pinned).sort((a, b) => +new Date(a.completedAt!) - +new Date(b.completedAt!))) {
    if (used + addBytes <= cap) break;
    await remove({ id: d.id });
    used -= d.bytesDone;
  }
  if (used + addBytes > cap) throw new Error('quota-exceeded');
}
```

UI surfaces an "Evict oldest unpinned to make room?" confirmation when proactively asked; throws clear error if pinned items fill cap.

## 5. Integrity check

After completion, compute BLAKE3 (or SHA-256 if BLAKE3 not available) and compare with `content_hash` returned by `GET /api/videos/{id}` (Epic 7). On mismatch → discard + re-download once.

## 6. Server sync (Story 12.11)

```ts
async function onDownloadCompleted(item: DownloadItem) {
  await api.post(`/videos/${item.videoId}/downloaded`, {
    quality: item.quality,
    bytes: item.bytesDone,
    pinned: item.pinned,
  });
}
```

On `remove()` → `DELETE /api/videos/{videoId}/downloaded`.

On reinstall (no rows locally), call `GET /api/me/devices/{id}/downloads` (provided by Story 12.11) and DELETE rows whose local files no longer exist.

## 7. Web (in-Capacitor) UI

| Path | Purpose |
|---|---|
| `web/src/features/downloads/useDownloads.ts` | Lists local downloads; subscribes to plugin progress events. |
| `web/src/features/downloads/DownloadButton.tsx` | Renders on `/watch/{id}`; opens quality picker. |
| `web/src/features/downloads/DownloadsPage.tsx` | New `/downloads` route inside the mobile app (gated behind `Capacitor.isNativePlatform()`). |
| `web/src/features/player/Player.tsx` | When item is downloaded, swap source URL to local. |

## 8. Edge cases

| Case | Handling |
|---|---|
| 2 GB on Wi-Fi only | Download proceeds; switching to cellular pauses if `allowCellular=false`. |
| App killed mid-download | iOS background session resumes; Android DownloadManager resumes via Range. |
| Cap exceeded | Confirmation banner offers "Evict oldest". |
| Hash mismatch | Discard + re-download (one retry). |
| App reinstall | Local store empty; reconcile via Story 12.11 cleanup. |
| iOS background download budget exhausted | Surface "Will resume when conditions allow"; system handles the resume. |

## 9. Test cases

### 9.1 Plugin unit

| Test | Asserts |
|---|---|
| `start enqueues with correct destination` | iOS download task points to sandboxed URL; Android DownloadManager request has `dataSync` flag. |
| `pause then resume continues from offset` | Total bytes processed not double-counted. |
| `quota eviction skips pinned` | Pinned item survives; oldest unpinned evicted. |
| `hash mismatch retries once` | Second completion accepted; third (still mismatched) marks failed. |
| `server sync POST/DELETE on lifecycle` | Mock API; correct calls observed. |

### 9.2 Device

- Pixel: download 2 GB on Wi-Fi; toggle airplane mode briefly; resumes on reconnect.
- iPhone: kill app mid-download; relaunch → completion event fires.
- Both: watch downloaded video offline (manifest URL replaced with local path).

## 10. Performance

- Plugin overhead per progress event ≤ 1 ms.
- Encryption throughput ≥ 50 MB/s on Pixel 5 (AES-GCM hardware).
- Quota enforcement runs in the worker, not on UI thread.

## 11. Dependencies

- Story 12.11 for the server flag.
- Story 12.10 (devices) — `device_id` is the auth context owner.
- Subtitle sidecar URLs come from `GET /api/videos/{id}/subtitles` (Epic 7 Story 7.7).

## 12. v2 follow-up — HLS offline

v1 ships direct-play offline only. Full HLS-stream offline (manifest +
all segments + key rotation) is deferred. When picked up, expected work:

- **iOS:** swap `URLSessionDownloadTask` for `AVAssetDownloadTask` /
  `AVAssetDownloadURLSession` (purpose-built for HLS; handles segments,
  encryption keys, quality variants).
- **Android:** swap `DownloadManager` for ExoPlayer's
  `DownloadHelper` + `DownloadService` (segment-aware; uses an
  ExoPlayer offline cache and reads back through `CacheDataSource`).
- Re-encoding/integrity check moves from whole-file SHA-256 to
  per-segment hashes returned by the manifest endpoint.
- Server contract (`POST /api/videos/{id}/downloaded`) gains an
  optional `manifest_etag` field so reconciliation can detect a
  segment set that has rotated.
