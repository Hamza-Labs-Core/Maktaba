# Story 12.6 — Download for offline viewing

The user can mark a video for offline download; the file (and its
subtitles + poster) is downloaded to encrypted device storage and
playable when offline. Downloads are pause-resumable and survive app
suspension.

**Anchors:** [`architecture.md` §4.1](../../architecture.md), §6.3.
Depends on [Story 12.11](story-12-11-downloaded-flag-api.md) for the
server-side flag API.

## AC

- "Download" action on every video detail page; per-quality picker
  (1080p / 720p / 480p / Audio-only).
- iOS: `URLSession` background tasks; Android: WorkManager + DownloadManager.
- Download status surface: progress bar, pause/resume/cancel; a "Downloads"
  tab lists all current and completed downloads.
- Storage quota: configurable cap (default 5 GB) with an LRU eviction
  policy; users can pin items to prevent eviction.
- Encryption at rest: per-app sandboxed storage on iOS; Android scoped
  storage (`MediaStore.Downloads/Maktaba/`) with file-level encryption
  via the device's Keystore-derived key.
- Offline playback: when offline, the detail page shows the local file as
  the source; subtitle tracks load from local sidecar.
- Sync: marking a video downloaded sets a server-side flag via
  [Story 12.11](story-12-11-downloaded-flag-api.md) so other devices see
  "downloaded on Phone".

## TC

- Download a 2 GB video on Wi-Fi only: download proceeds; switching to
  cellular pauses (configurable).
- Resume a partial 800 MB download after an app kill: HTTP Range request
  resumes from the byte offset.
- Watch the video offline: player loads the local URL; progress syncs to
  the server when network returns.
- Cap exceeded (5.5 GB requested with 5 GB cap): the oldest unpinned
  download is evicted with a confirmation banner.

## EC

- Cellular data limit hit mid-download: pause, surface "Resume on Wi-Fi?".
- File integrity: each completed download is BLAKE3-checksummed against
  the server's `content_hash`; mismatch → discard and re-download.
- App reinstall: downloads are lost (sandbox cleared); UI resets the
  download flag (Story 12.11 reflects this on next sync).
- iOS background download budget exhausted: the system pauses and
  resumes when conditions allow; we surface "Will resume when conditions
  allow" rather than failing.
