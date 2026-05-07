# Epic 13 — Desktop Apps (Tauri)

> A Tauri 2 wrapper of the same web bundle producing native binaries for macOS, Windows, and Linux. Native menus, file associations, system tray, file drag-and-drop, auto-update, mDNS server discovery.

- **Spec README:** [`specs/epics/13-desktop/README.md`](../../../specs/epics/13-desktop/README.md)
- **Architecture anchors:** §6.4, §2.1 (Tauri 2)
- **Source bundle:** [Epic 11 Web UI](epic-11-web-ui.md) — everything in 11.1–11.12 ships inside the Tauri shell unchanged.
- **Out of scope:** Snap / Flatpak packaging (post-v1; AppImage + .deb cover primary distros), Microsoft Store distribution (post-v1; .msi covers enterprise), Mac App Store distribution (post-v1; sandbox restrictions on filesystem access make the local-library use case awkward).

## Stories & Plans

| #     | Story                                                  | Plan                                              | Status |
|-------|--------------------------------------------------------|---------------------------------------------------|--------|
| 13.1  | [macOS app](../../../specs/epics/13-desktop/story-13-01-macos.md) | [plan](../../../specs/epics/13-desktop/plan-13-01-macos.md) | spec |
| 13.2  | [Windows app](../../../specs/epics/13-desktop/story-13-02-windows.md) | [plan](../../../specs/epics/13-desktop/plan-13-02-windows.md) | spec |
| 13.3  | [Linux app](../../../specs/epics/13-desktop/story-13-03-linux.md) | [plan](../../../specs/epics/13-desktop/plan-13-03-linux.md) | spec |
| 13.4  | [System tray integration](../../../specs/epics/13-desktop/story-13-04-system-tray.md) | [plan](../../../specs/epics/13-desktop/plan-13-04-system-tray.md) | spec |
| 13.5  | [Local server auto-discovery (mDNS)](../../../specs/epics/13-desktop/story-13-05-mdns-discovery.md) | [plan](../../../specs/epics/13-desktop/plan-13-05-mdns-discovery.md) | spec |
| 13.6  | [File drag-and-drop](../../../specs/epics/13-desktop/story-13-06-drag-drop.md) | [plan](../../../specs/epics/13-desktop/plan-13-06-drag-drop.md) | spec |
| 13.7  | [Keyboard shortcuts](../../../specs/epics/13-desktop/story-13-07-keyboard-shortcuts.md) | [plan](../../../specs/epics/13-desktop/plan-13-07-keyboard-shortcuts.md) | spec |
| 13.8  | [Auto-update](../../../specs/epics/13-desktop/story-13-08-auto-update.md) | [plan](../../../specs/epics/13-desktop/plan-13-08-auto-update.md) | spec |

## DB tables owned

None. Desktop is a thin native shell over [Epic 11 Web UI](epic-11-web-ui.md). Window state, route, and last-known-server are stored in the OS-appropriate config dir (`~/Library/Application Support/Maktaba/`, `%APPDATA%\Maktaba\`, `~/.config/maktaba/`).

## API endpoints owned

None directly. Desktop calls the same REST + GraphQL surface as the web SPA. mDNS discovery (13.5) consumes the **server-side** advertising in Epic 15 story 15.1 — the desktop only emits SD-DNS queries.

## Mockups

| File | Story | Platform | UI states / contents |
|---|---|---|---|
| [`web/mockups/desktop/main-window.html`](../../../web/mockups/desktop/main-window.html) | 13.1, 13.2, 13.3 | desktop | Desktop chrome, native menu bar, window state |
| [`web/mockups/desktop/tray-menu.html`](../../../web/mockups/desktop/tray-menu.html) | 13.4 | desktop | Tray icon menu (active streams, queue, quit) |
| [`web/mockups/desktop/drag-drop.html`](../../../web/mockups/desktop/drag-drop.html) | 13.6 | desktop | Drag-and-drop ingest target, OS file source |
| [`web/mockups/admin/server-discovery.html`](../../../web/mockups/admin/server-discovery.html) | 13.5 | admin (web) | TOFU/trust prompts: Confirm · trust new server; Confirm · forget server; Dialog · key changed warning; Toast · connected; Toast · connection refused; Toast · timeout; Dropdown · server overflow menu; Skeleton · scanning network; Empty · no servers found; Empty · mDNS blocked; Tooltip · what is mDNS?; Inline · validation error |

## Diagrams

| Diagram | Type | Coverage |
|---|---|---|
| [`client-stories.drawio`](../../../specs/diagrams/client-stories.drawio) | Story-relationship | All Epic 13 stories grouped with 11/12/17 |
| [`system-architecture.drawio`](../../../specs/diagrams/system-architecture.drawio) | System | Desktop shells in the topology |
| [`data-flow.drawio`](../../../specs/diagrams/data-flow.drawio) | Flow | Desktop → mDNS → API + Streaming |
| [`security-architecture.drawio`](../../../specs/diagrams/security-architecture.drawio) | Security | Auto-update signing, TOFU on first server discovery |

## Dependencies on other epics

- **[Epic 11](epic-11-web-ui.md)** is the source bundle.
- **Epic 17 stories 17.1, 17.2** — design tokens.
- **Epic 15 story 15.1** (mDNS server-side advertising) is a prerequisite for [story 13.5](../../../specs/epics/13-desktop/story-13-05-mdns-discovery.md).
- **[Epic 10](epic-10-auth-security.md) story 10.3** — native JWT login (used same way as mobile, with OS keychain).
- **[Epic 10](epic-10-auth-security.md) story 10.17** — pairing flow when entering a code from another device.

## Key decisions

- **Tauri 2, not Electron.** WebView-based; Rust core; small binaries; no Chromium fork bundled.
- **Code signing is a release blocker.** macOS notarized + hardened runtime; Windows EV cert; Linux SHA256 + GPG sig published alongside artifacts.
- **Tray icon assets** are platform-specific. Monochrome variants for macOS dark menu bar; full-color for Windows / Linux per platform conventions.
- **Window state persistence** — position, size, route per-window.
- **Single-instance lock** — opening a deep link from the OS reuses the existing window rather than spawning a new one.
- **mDNS discovery** (13.5) is opt-in; the user explicitly *trusts* a discovered server (TOFU) before any credentials cross the wire. Key changes after first trust prompt the user.
- **Auto-update** (13.8) — Tauri's signed-update endpoint hosted alongside release artifacts. Channels: stable / beta / canary.
- **Drag-and-drop** (13.6) hands files to the API's standard ingest path — no special desktop-only ingestion endpoint.
