# Story 12.5 — Background playback

Audio continues playing when the screen is off or the user switches apps,
on both iOS and Android, with system controls for play/pause/seek.

**Anchors:** [`architecture.md` §6.3](../../architecture.md). Depends on
[Story 12.3](story-12-03-native-player.md).

## AC

- iOS: audio session category `.playback`, `audioMode = .moviePlayback`.
- Android: foreground service of type `mediaPlayback` with a persistent
  notification.
- Lock-screen / notification-shade controls: play/pause, seek ±10 s,
  next/previous chapter, scrubber.
- Pulling out headphones pauses; reconnecting does not auto-resume
  unless the user has set "Auto-resume on headphone reconnect" in
  Settings → Playback.
- Picture-in-picture (PiP) supported on iPad and Android; auto-engaged on
  swipe-to-home if the user has enabled it.
- Background tasks: position sync every 10 s; resilient to brief
  network drops (≤ 30 s).

## TC

- Start a lecture, lock the iPhone: audio continues; lock screen shows
  controls.
- Tap PiP on iPad mid-playback, switch to Notes: the player floats; tap
  it to expand back.
- Headphone unplug pauses; "Auto-resume" off → manual play required.
- WebSocket disconnects in background: reconnects on foreground; does not
  spam reconnect attempts in background.

## EC

- iOS bans background WebSocket beyond ~30 s: position sync uses
  background-fetch URLSession instead.
- Android Doze mode: the foreground service exempts us; position sync
  continues. We never claim a wake lock.
- Bluetooth latency causes seek desync: native player handles its own
  resync; we do not double-correct.
