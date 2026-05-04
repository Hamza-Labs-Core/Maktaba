# Implementation Plan — Story 12.5 Background Playback

> Companion to [story-12-05-background-playback.md](story-12-05-background-playback.md).
> Builds on Story 12.3 (native player). Uses `AVAudioSession` (iOS) +
> foreground service (Android) for off-screen audio.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| iOS audio session | `AVAudioSession.Category.playback`, `mode = .moviePlayback`, options `.allowAirPlay`. |
| Android service | Foreground service of type `mediaPlayback` with persistent notification (declared in manifest, Story 12.2). |
| Lock-screen / notification controls | iOS via `MPRemoteCommandCenter`; Android via `MediaSessionCompat` + `MediaStyle` notification. |
| Headphone unplug | Listen for route changes; pause unconditionally; auto-resume only if user opted in. |
| PiP | iPad and Android (API 26+); auto-engage on swipe-to-home if user opted in. |
| Out of scope | Native player itself (Story 12.3); chromecast/AirPlay UX (Story 12.7). |

## 1. iOS implementation

```swift
// In NativePlayer.swift initialization (Story 12.3)
let session = AVAudioSession.sharedInstance()
try session.setCategory(.playback, mode: .moviePlayback, options: [.allowAirPlay])
try session.setActive(true)

NotificationCenter.default.addObserver(
    forName: AVAudioSession.routeChangeNotification, object: nil, queue: .main
) { [weak self] note in
    guard let reason = note.userInfo?[AVAudioSessionRouteChangeReasonKey] as? UInt,
          let r = AVAudioSession.RouteChangeReason(rawValue: reason),
          r == .oldDeviceUnavailable else { return }
    self?.player.pause()
    if !UserDefaults.standard.bool(forKey: "autoResumeOnHeadphoneReconnect") { return }
    // wait for the next route change to a usable output and resume
}
```

Lock screen / Now Playing controls already wired in Story 12.3 via `MPNowPlayingInfoCenter`. Add `MPRemoteCommandCenter` wiring:

```swift
let cc = MPRemoteCommandCenter.shared()
cc.playCommand.addTarget { _ in player.play(); return .success }
cc.pauseCommand.addTarget { _ in player.pause(); return .success }
cc.skipForwardCommand.preferredIntervals = [10]
cc.skipForwardCommand.addTarget { _ in player.seek(by: 10); return .success }
cc.changePlaybackPositionCommand.addTarget { evt in
    if let e = evt as? MPChangePlaybackPositionCommandEvent {
        player.seek(to: CMTime(seconds: e.positionTime, preferredTimescale: 600))
    }
    return .success
}
```

## 2. Android implementation

`PlayerActivity` (Story 12.3) launches `MediaPlaybackService` (foreground service) when entering background:

```kotlin
class MediaPlaybackService : Service() {
    private lateinit var session: MediaSessionCompat
    private lateinit var player: ExoPlayer
    private val callback = object : MediaSessionCompat.Callback() {
        override fun onPlay()  { player.play() }
        override fun onPause() { player.pause() }
        override fun onSeekTo(pos: Long) { player.seekTo(pos) }
    }

    override fun onCreate() {
        super.onCreate()
        player = (application as MaktabaApp).sharedPlayer
        session = MediaSessionCompat(this, "MaktabaMedia").apply {
            setCallback(callback); isActive = true
        }
        startForeground(NOTIFICATION_ID, buildNotification())
        registerReceiver(noisyReceiver, IntentFilter(AudioManager.ACTION_AUDIO_BECOMING_NOISY))
    }

    private val noisyReceiver = object : BroadcastReceiver() {
        override fun onReceive(c: Context, i: Intent) {
            player.pause()  // pause on headphones unplug
        }
    }
}
```

`buildNotification` uses `NotificationCompat.MediaStyle().setMediaSession(session.sessionToken)` and includes Play/Pause/SeekBack/SeekForward actions.

## 3. PiP

- iOS: `AVPlayerViewController.allowsPictureInPicturePlayback = true` (Story 12.3 already sets this).
- Android: `Activity.enterPictureInPictureMode(PictureInPictureParams.Builder()...)` triggered from `onUserLeaveHint()`.

User opts in via Settings → Playback → "Auto PiP on swipe-to-home".

## 4. Background WS / progress sync

- iOS: `URLSession` background task category submits `POST /api/stream/sessions/{id}/progress` every 10 s; WS reconnect throttled to ≥ 60 s while backgrounded.
- Android: Foreground service + WorkManager `OneTimeWorkRequest` queues every 10 s for progress; WS reconnect throttled.

## 5. File layout

```
apps/mobile/plugins/native-player/    (extends Story 12.3)
├── ios/
│   └── BackgroundAudio.swift          # category + remote command + route change
└── android/
    └── MediaPlaybackService.kt
```

Web side reuses `useWatchProgress` (Story 11.3) plus a new `useAutoResumePref` hook reading `Settings.autoResumeOnHeadphoneReconnect`.

## 6. Edge cases

| Case | Handling |
|---|---|
| iOS bans background WebSocket > 30 s | Position sync via background-fetch URLSession; WS resumes on foreground. |
| Android Doze | Foreground service exempt; we never wake-lock the device. |
| Bluetooth latency causes seek desync | Native player handles its own resync; no double correction. |
| Headphones reconnect with auto-resume off | Manual play required; we do nothing. |

## 7. Test cases

### 7.1 Manual

- Lock iPhone mid-playback: audio continues; lock screen shows scrubber + play/pause.
- Pixel + Android Auto: media controls appear in Auto UI.
- Unplug headphones (any platform): playback pauses immediately.

### 7.2 Automated

- iOS UI test: emulate route change → `pause` called.
- Android instrumented test: `ACTION_AUDIO_BECOMING_NOISY` broadcast → `player.pause()` invoked.
- Both: `setActive(false)` only when player is destroyed; lock-screen controls disappear.

## 8. Performance

- Foreground service notification renders within 500 ms of backgrounding.
- Background progress posts succeed under iOS `BGTaskScheduler` budget (one post / 10 s).

## 9. Dependencies

- Story 12.3 (native player).
- Story 12.2 (manifest declares foreground service).
- Settings UI: Story 11.6 → Playback section adds "Auto-resume on headphone reconnect" toggle.
