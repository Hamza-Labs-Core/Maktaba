# Story 11.9 — Keyboard shortcuts

A global shortcut layer with a help overlay (`?`) that lists every
shortcut.

**Anchors:** [`architecture.md` §6.2](../../architecture.md). Depends on
Story 11.3 (player shortcuts).

## AC

- Shortcuts:
  - `g l` → go Library, `g s` → Search, `g q` → Queue, `g h` → Home, `g ,` →
    Settings.
  - `/` → focus header search.
  - `?` → toggle keyboard help overlay.
  - In player: see [Story 11.3](story-11-03-video-player.md).
  - `j / k` on lists → move focus next/previous; `Enter` → open.
- Shortcuts are disabled when any text input is focused (except global
  `Esc`).
- Help overlay is filterable, RTL-aware, and lists per-context shortcuts
  (player, list, search).
- Shortcuts honor RTL: `←/→` semantics in the player flip in RTL mode so
  `→` is "back" in Arabic UI (configurable: "use logical arrows" toggle in
  settings).

## TC

- Press `g l` from the Home page: navigates to Library within 200 ms.
- Press `?` while focused in the search box: nothing happens (input is
  active).
- Press `?` from anywhere else: overlay opens.
- Hold `g` for 2 s without a follow-up: nothing happens; the leader is
  silently dropped.

## EC

- IME composing text (Arabic, Japanese): shortcuts disabled until
  composition ends.
- A shortcut conflicts with a browser shortcut (e.g., `Ctrl+S`): the
  Maktaba shortcut should not preempt the browser default.
- On Linux/Windows, `Cmd` is not present: documentation maps to `Ctrl`
  uniformly.
