# Story 12.8 — Haptic feedback

Light haptic cues on key user actions, respecting the OS-level haptics
toggle.

**Anchors:** [`architecture.md` §6.3](../../architecture.md).

## AC

- Haptic events:
  - Tap a navigation tab → light tap.
  - Long-press a video card → medium impact.
  - Toggle a setting → selection-change.
  - Download complete → success notification haptic.
  - Error toast → warning notification haptic.
- iOS uses `UIImpactFeedbackGenerator` and
  `UINotificationFeedbackGenerator`; Android uses
  `HapticFeedbackConstants`.
- Respect "Reduce motion / haptics" in the OS settings.
- Configurable in Settings → Accessibility → Haptics (Off / Light / Full).

## TC

- iOS with haptics disabled in Settings: no haptics fire.
- Long-press card on Android: feels distinct from a tap.
- Error haptic does not fire on a routine validation error (only on
  network/server error).

## EC

- Devices without haptics (older Android tablets): silently no-op.
- Rapid-fire actions (typing in search): haptics throttled to ≤ 1 every
  100 ms.
