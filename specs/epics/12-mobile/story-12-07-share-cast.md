# Story 12.7 — Share / AirPlay / Chromecast support

Native share sheet and casting on both platforms.

**Anchors:** [`architecture.md` §6.3](../../architecture.md). Depends on
[Story 12.3](story-12-03-native-player.md) (native player).

## AC

- "Share" button on video detail: opens the native share sheet with a
  deep link `https://{server}/watch/{id}` and a fallback poster.
- AirPlay: integrated via AVPlayer; AirPlay button visible in player
  controls when the device sees a receiver.
- Chromecast: integrated via the Cast SDK; Cast button visible when a
  receiver is on the LAN. Cast session published to `MediaSession`.
- Share to Messages, Mail, Notes, third-party apps: all use the same
  metadata payload (URL + title + poster).
- Receiving a shared link in another Maktaba app opens the deep link
  (Story 12.9).

## TC

- Share to Messages: link previews with poster and title.
- AirPlay during playback: receiver picks up the stream within 3 s; local
  device shows "Now playing on Apple TV".
- Chromecast on a network with two receivers: picker lists both;
  selection persists for the session.

## EC

- Receiver doesn't support the source codec: we fall back to a
  HLS-transcoded stream
  ([architecture.md §4.1](../../architecture.md) mode 3).
- AirPlay 2 multi-room: only the primary room is targeted (multi-room
  audio is out of v1 scope).
- Cast session lost mid-playback (receiver power-cycled): we surface a
  toast and offer "Resume on this device".
