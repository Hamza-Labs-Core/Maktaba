# Epic 13 — Desktop Apps (Tauri)

> `plan-13-01-macos.md` is the base scaffolding plan; the other 7 plans
> assume its setup (Cargo workspace, capabilities directory, `tauri.conf.json`
> baseline).

**Goal.** A Tauri 2 wrapper of the same web bundle producing native
binaries for macOS, Windows, and Linux. Native menus, file associations,
system tray, file drag-and-drop, auto-update, mDNS server discovery.

**Anchors:** [`architecture.md` §6.4](../../architecture.md), §2.1
(Tauri 2).

---

## Stories

| # | Story | Status |
|---|-------|--------|
| 13.1 | [macOS app](story-13-01-macos.md) | spec |
| 13.2 | [Windows app](story-13-02-windows.md) | spec |
| 13.3 | [Linux app](story-13-03-linux.md) | spec |
| 13.4 | [System tray integration](story-13-04-system-tray.md) | spec |
| 13.5 | [Local server auto-discovery (mDNS)](story-13-05-mdns-discovery.md) | spec |
| 13.6 | [File drag-and-drop](story-13-06-drag-drop.md) | spec |
| 13.7 | [Keyboard shortcuts](story-13-07-keyboard-shortcuts.md) | spec |
| 13.8 | [Auto-update](story-13-08-auto-update.md) | spec |

---

## Dependencies

- **Epic 11** (Web UI) is the source bundle.
- **Epic 17** (Design System) Stories 17.1, 17.2.
- **Epic 15** (Discovery) Story 15.1 (mDNS server-side advertising) is a
  prerequisite for [Story 13.5](story-13-05-mdns-discovery.md).

## Cross-cutting checklist

- **Code signing:** macOS notarized + hardened runtime; Windows EV cert;
  Linux SHA256 + GPG sig published alongside artifacts.
- **Tray icon assets:** monochrome variants for macOS dark menu bar;
  full-color for Windows / Linux per platform conventions.
- **Window state persistence:** position, size, route per-window.
- **Single-instance lock:** opening a deep link from the OS reuses the
  existing window rather than spawning a new one.

## Out of scope

- Snap / Flatpak packaging for Linux (post-v1; AppImage + .deb cover
  primary distros).
- Microsoft Store distribution (post-v1; .msi covers enterprise).
- Mac App Store distribution (post-v1; sandbox restrictions on
  filesystem access make the local-library use case awkward).
