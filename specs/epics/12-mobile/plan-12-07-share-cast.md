# Implementation Plan — Story 12.7 Share / AirPlay / Chromecast

> Companion to [story-12-07-share-cast.md](story-12-07-share-cast.md).
> Builds on Story 12.3 (native player). AirPlay via AVPlayer, Cast via Cast SDK.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Share | `@capacitor/share` plugin; payload includes deep link + poster fallback. |
| AirPlay | Inherent to AVPlayer (Story 12.3); we surface the AirPlay button via `AVRoutePickerView`. |
| Chromecast | `google-cast-sdk` integrated into the native player Activity (Story 12.3 Android). |
| Receiver fallback | If receiver can't decode source codec, server returns transcoded HLS (architecture §4.1 mode 3). |
| Out of scope | Multi-room AirPlay 2 (post v1); receiver app (we use Cast Default Receiver). |

## 1. Share

```ts
// web/src/features/share/ShareButton.tsx
import { Share } from '@capacitor/share';

async function onShare(video: VideoSummary, serverHost: string) {
  await Share.share({
    title: video.title,
    text: video.title,
    url: `https://${serverHost}/watch/${video.id}`,
  });
}
```

Falls back to `navigator.share` on web/desktop where Capacitor isn't present.

## 2. AirPlay (iOS)

`AVPlayerViewController` exposes a "Routes" button by default; we additionally embed an `AVRoutePickerView` next to the inline web player so the affordance is visible without going fullscreen:

```swift
// In NativePlayer.swift
let routePicker = AVRoutePickerView()
routePicker.activeTintColor = .systemBlue
routePicker.tintColor = .label
// presented as an overlay accessory; visible only when a receiver is detected
```

Web bridge exposes `airplay.isAvailable()` so the inline player can toggle the picker UI.

## 3. Chromecast (Android)

Add `play-services-cast` + `play-services-cast-framework` to `apps/mobile/android/app/build.gradle.kts`. App-level `CastOptionsProvider`:

```kotlin
class CastOptionsProvider : OptionsProvider {
    override fun getCastOptions(ctx: Context): CastOptions {
        return CastOptions.Builder()
            .setReceiverApplicationId(CastMediaControlIntent.DEFAULT_MEDIA_RECEIVER_APPLICATION_ID)
            .build()
    }
    override fun getAdditionalSessionProviders(ctx: Context) = emptyList<SessionProvider>()
}
```

Manifest:

```xml
<meta-data
    android:name="com.google.android.gms.cast.framework.OPTIONS_PROVIDER_CLASS_NAME"
    android:value="com.maktaba.app.CastOptionsProvider"/>
```

`PlayerActivity` (Story 12.3) hosts a `MediaRouteButton` in its toolbar; clicking opens the route picker. On session start, `RemoteMediaClient.load` queues the manifest URL.

## 4. Cast → MediaSession

When casting, `MediaSessionCompat` is updated to reflect remote playback so the lock screen continues to show controls (poster + scrubber) while cast is active.

## 5. Edge cases

| Case | Handling |
|---|---|
| Receiver cannot decode source | Switch to HLS-transcoded stream (server side picks `mode=transcode`). |
| Two Chromecasts on LAN | Picker lists both; selection persists per session. |
| Cast session lost | Toast + "Resume on this device" CTA. |
| AirPlay 2 multi-room | Only primary room targeted in v1. |

## 6. Test cases

### 6.1 Manual

- Share to Messages: link previews with poster + title.
- AirPlay: tap during playback; receiver picks up within 3 s.
- Chromecast: select receiver; playback continues there; switching back resumes position.

### 6.2 Automated (best effort)

- iOS: Mock `AVAudioSession` route change → AirPlay button surfaces.
- Android: Cast SDK simulator (when present) → `RemoteMediaClient.load` called with correct media info.
- Share plugin: Vitest stub asserts payload shape.

## 7. Performance

- Share sheet open ≤ 200 ms.
- Cast session start ≤ 3 s.

## 8. Dependencies

- Story 12.3 (native player).
- Server: `mode=transcode` (Epic 8 Story 8.5).
- Settings → Playback offers "Always cast in HLS" toggle (Story 11.6).
