# Implementation Plan — Story 12.3 Native Player Plugin

> Companion to [story-12-03-native-player.md](story-12-03-native-player.md).
> Capacitor plugin that opens AVPlayer (iOS) / ExoPlayer (Android).
> Web layer always calls `POST /api/stream/sessions` first (Story 11.3 handshake).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Plugin name | `@maktaba/native-player` (`apps/mobile/plugins/native-player/`). |
| iOS class | `NativePlayer` (Swift) — opens `AVPlayerViewController`; integrates `MPNowPlayingInfoCenter`. |
| Android class | `NativePlayer` (Kotlin) — uses ExoPlayer + `MediaSessionCompat`. |
| API | `nativePlayer.open({ manifestUrl?, directUrl?, posterUrl, title, durationSec, startSec, audioTrackIndex?, subtitleLanguage? })` → returns session handle. Plugin emits events: `progress`, `closed`, `trackChanged`, `error`. |
| Direct vs manifest | Mode picked by web from session response. Direct URLs use audience `streaming-direct` (Epic 10 Story 10.8); only native players are allowed to call `GET /stream/direct/{video_id}` per REVIEW §4.1 resolution. |
| Out of scope | AirPlay/Cast UX (Story 12.7); background audio policies (Story 12.5). |

## 1. Plugin layout

```
apps/mobile/plugins/native-player/
├── package.json
├── README.md
├── src/
│   ├── definitions.ts          # interface + types
│   ├── index.ts                # registerPlugin()
│   └── web.ts                  # web fallback (no-op; the web player handles inline)
├── ios/
│   ├── Plugin.swift
│   ├── NativePlayer.swift
│   └── Plugin.podspec
└── android/
    ├── build.gradle.kts
    └── src/main/java/com/maktaba/nativeplayer/
        ├── NativePlayerPlugin.kt
        └── PlayerActivity.kt
```

## 2. TypeScript definition

```ts
// definitions.ts
export interface OpenOptions {
  manifestUrl?: string;
  directUrl?: string;
  posterUrl?: string;
  title: string;
  durationSec: number;
  startSec?: number;
  audioTrackIndex?: number;
  subtitleLanguage?: string;
  sessionId: string;          // streaming session for progress posts
}

export interface NativePlayerPlugin {
  open(opts: OpenOptions): Promise<{ handle: string }>;
  close(handle: { handle: string }): Promise<void>;
  setSubtitleLanguage(opts: { handle: string; language: string }): Promise<void>;
  addListener(event: 'progress', cb: (e: { offsetSec: number }) => void): PluginListenerHandle;
  addListener(event: 'closed',   cb: (e: { offsetSec: number }) => void): PluginListenerHandle;
  addListener(event: 'trackChanged', cb: (e: { audioIndex?: number; subtitleLanguage?: string }) => void): PluginListenerHandle;
  addListener(event: 'error',    cb: (e: { code: string; message: string }) => void): PluginListenerHandle;
}
```

## 3. iOS implementation

```swift
@objc(NativePlayer)
public class NativePlayer: CAPPlugin {
    private var players: [String: AVPlayerViewController] = [:]

    @objc func open(_ call: CAPPluginCall) {
        guard let urlString = call.getString("directUrl") ?? call.getString("manifestUrl"),
              let url = URL(string: urlString) else { call.reject("missing-url"); return }
        let item = AVPlayerItem(url: url)
        let player = AVPlayer(playerItem: item)
        if let start = call.getDouble("startSec") {
            player.seek(to: CMTime(seconds: start, preferredTimescale: 600))
        }
        let vc = AVPlayerViewController()
        vc.player = player
        vc.allowsPictureInPicturePlayback = true

        configureNowPlaying(call, player: player)
        observeProgress(player, sessionId: call.getString("sessionId") ?? "")

        // Insert into the dictionary BEFORE present() so a fast close({handle})
        // can find the entry — present() is async and may complete after the JS
        // side has already received the resolve and called close.
        let handle = UUID().uuidString
        players[handle] = vc
        call.resolve(["handle": handle])

        DispatchQueue.main.async {
            self.bridge?.viewController?.present(vc, animated: true) {
                player.play()
            }
        }
    }

    private func observeProgress(_ player: AVPlayer, sessionId: String) {
        let interval = CMTime(seconds: 10, preferredTimescale: 1)
        player.addPeriodicTimeObserver(forInterval: interval, queue: .main) { [weak self] time in
            self?.notifyListeners("progress", data: ["offsetSec": time.seconds])
        }
    }

    private func configureNowPlaying(_ call: CAPPluginCall, player: AVPlayer) {
        var info: [String: Any] = [:]
        info[MPMediaItemPropertyTitle] = call.getString("title") ?? "Maktaba"
        info[MPMediaItemPropertyPlaybackDuration] = call.getDouble("durationSec") ?? 0
        // Publish title+duration immediately so lock-screen/Now Playing populate
        // even before artwork is fetched.
        MPNowPlayingInfoCenter.default().nowPlayingInfo = info

        if let posterUrl = call.getString("posterUrl"), let url = URL(string: posterUrl) {
            // Asynchronous fetch — never block the main `open` path on a network
            // read, which can hang for many seconds on cellular.
            URLSession.shared.dataTask(with: url) { data, _, _ in
                guard let data, let img = UIImage(data: data) else { return }
                DispatchQueue.main.async {
                    var info = MPNowPlayingInfoCenter.default().nowPlayingInfo ?? [:]
                    info[MPMediaItemPropertyArtwork] = MPMediaItemArtwork(boundsSize: img.size) { _ in img }
                    MPNowPlayingInfoCenter.default().nowPlayingInfo = info
                }
            }.resume()
        }
    }
}
```

Subtitle switching uses `AVPlayerItem.selectMediaOption` on the `legible` characteristic; events fire back via `notifyListeners`.

## 4. Android implementation

```kotlin
@CapacitorPlugin(name = "NativePlayer")
class NativePlayerPlugin : Plugin() {
    @PluginMethod
    fun open(call: PluginCall) {
        val url = call.getString("directUrl") ?: call.getString("manifestUrl")
            ?: return call.reject("missing-url")
        val intent = Intent(activity, PlayerActivity::class.java).apply {
            putExtra("url", url)
            putExtra("title", call.getString("title"))
            putExtra("posterUrl", call.getString("posterUrl"))
            putExtra("startSec", call.getDouble("startSec") ?: 0.0)
            putExtra("sessionId", call.getString("sessionId"))
        }
        activity.startActivity(intent)
        val handle = UUID.randomUUID().toString()
        call.resolve(JSObject().put("handle", handle))
    }
}

class PlayerActivity : AppCompatActivity() {
    private lateinit var player: ExoPlayer
    private lateinit var session: MediaSessionCompat
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_player)
        val view = findViewById<PlayerView>(R.id.player_view)
        player = ExoPlayer.Builder(this).build().also { view.player = it }

        val url = intent.getStringExtra("url")!!
        val item = MediaItem.fromUri(url)
        player.setMediaItem(item)
        player.prepare()
        intent.getDoubleExtra("startSec", 0.0).let { if (it > 0) player.seekTo((it * 1000).toLong()) }
        player.play()

        session = MediaSessionCompat(this, "MaktabaPlayer").apply {
            setMetadata(MediaMetadataCompat.Builder()
                .putString(METADATA_KEY_TITLE, intent.getStringExtra("title"))
                .putLong(METADATA_KEY_DURATION, player.duration)
                .build())
            isActive = true
        }

        val sessionId = intent.getStringExtra("sessionId") ?: run {
            Log.w(TAG, "no sessionId extra; finishing")
            finish()
            return
        }
        startProgressTicker(sessionId)
    }

    override fun onDestroy() { player.release(); session.release(); super.onDestroy() }

    companion object { private const val TAG = "PlayerActivity" }
}
```

Progress events bubble back through a static reference to the plugin instance (or, more cleanly, a LiveEventBus / shared `Channel`).

## 5. Web wiring

```ts
// web/src/features/player/useNativeHandoff.ts
import { Capacitor } from '@capacitor/core';
import { NativePlayer } from '@maktaba/native-player';

export function useNativeHandoff(session: StreamSession, opts: OpenOptions) {
  if (!Capacitor.isNativePlatform()) return null;
  return async function openNative() {
    const handle = await NativePlayer.open({
      ...(session.mode === 'direct' && session.direct_url
        ? { directUrl: session.direct_url }
        : { manifestUrl: session.manifest_url }),
      ...opts,
      sessionId: session.session_id,
    });
    NativePlayer.addListener('progress', e =>
      api.post(`/stream/sessions/${session.session_id}/progress`, { offset_sec: e.offsetSec }));
    NativePlayer.addListener('closed', e => onClose?.(e.offsetSec));
  };
}
```

The fullscreen button on the web player calls this when running inside Capacitor; otherwise it triggers the browser's fullscreen API.

## 6. Edge cases

| Case | Handling |
|---|---|
| HLS audio language disagrees with manifest | Prefer manifest; log warning. |
| AirPlay receiver lacks codec | Server already responded with transcoded HLS for those receivers; native side never pushes unsupported codec. |
| Background audio | Story 12.5 owns audio session config. |
| App force-closed | Server-side reaper closes session within 90 s. |

## 7. Test cases

### 7.1 Native unit

- iOS XCTest: open with start=120 → AVPlayerItem at 2:00; `MPNowPlayingInfoCenter.nowPlayingInfo` populated.
- Android instrumented test: Intent extras → ExoPlayer state RUNNING; `MediaSessionCompat.isActive == true`.

### 7.2 e2e

- iPhone: tap fullscreen on a video, swipe to AirPlay → stream continues; lock screen scrubber present.
- Pixel: cast picker lists Chromecast; selection switches output; resume on phone preserves position.

### 7.3 Regression

- Audio-track change closes the existing session via `DELETE /api/stream/sessions/{id}` and reopens with new `audio_track_index` (web layer + plugin coordination).

## 8. Performance

- Plugin overhead ≤ 50 ms from `open()` call to first frame on iPhone 13.
- Progress ticker fires every 10 s exactly (drift < 0.5 s/min).

## 9. Dependencies

- Web side: Story 11.3.
- API: Epic 7 Story 7.10, Epic 8 Story 8.3, Epic 10 Story 10.8.
- Story 12.5 (background audio) wires audio session category.
