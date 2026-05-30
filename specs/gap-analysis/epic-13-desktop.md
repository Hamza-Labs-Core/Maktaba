# Epic 13 — Desktop Apps (Tauri): Spec vs Implementation Gap Analysis

**Verdict (one line):** The Tauri shell is a thin scaffold — window/menu/tray/updater plugins are *registered* but mostly inert; menu items are unhandled, and the four headline features (mDNS discovery 13.5, drag-drop import 13.6, desktop shortcuts 13.7, auto-update UX 13.8) plus all platform packaging/signing (13.1–13.3) are **entirely absent**. Roughly 90% of ACs are missing or unwired.

## Scope of code examined

- `apps/desktop/src-tauri/src/lib.rs` (137 lines — the only real logic)
- `apps/desktop/src-tauri/src/main.rs` (26 lines — calls `lib::run()`; unused imports)
- `apps/desktop/src-tauri/tauri.conf.json`, `Cargo.toml`, `capabilities/default.json`
- `apps/desktop/package.json`
- `web/src/**` — searched for `@tauri-apps`, mDNS, drag-drop, updater, server picker: **zero hits**. The shared web bundle has **no desktop integration code whatsoever**.
- `.github/workflows/**` — searched for notarization/codesign/tauri build: **zero hits** (no desktop CI/release pipeline).
- No `src-tauri/icons/` directory exists (referenced by `tauri.conf.json:36-41`; `mdns.rs` / `desktop/DropOverlay.tsx` from plans do not exist).

---

## Story 13.1 — macOS app

| AC | Status | Evidence / Gap |
|---|---|---|
| Targets macOS 13+; universal binary | partial | `tauri.conf.json:48` `minimumSystemVersion: "13.0"` set. No universal/`aarch64`+`x86_64` lipo config; no CI building either arch. EC "macOS 12 friendly refusal" not implemented. |
| Native menu bar (Maktaba/File/Edit/View/Library/Window/Help) + std shortcuts | **partial/unwired** | `lib.rs:70-137` builds all 7 submenus with accelerators (`Cmd+,`, `Cmd+N`, `Cmd+R`). BUT `app.set_menu(menu)` (`lib.rs:31`) has **no `on_menu_event` handler** — the custom items `preferences`, `new-window`, `scan-library`, `open-docs` (`lib.rs:73,78,107,122`) do nothing when clicked. Predefined items (quit/close/copy) work; all custom items are dead. |
| Window restore: position & size persisted | partial | `tauri_plugin_window_state` registered (`lib.rs:22`). Plugin persists size/position by default, but AC/TC also require **last route** restored — no route persistence anywhere. |
| Light/dark follows OS; Maktaba theme override | missing | No theme/appearance handling in Rust or any wired frontend. |
| Notarized + hardened runtime; Gatekeeper passes | missing | No notarization/entitlements/signing config; no CI. |
| Distributable via `.dmg` + Homebrew tap | missing | `bundle.targets: "all"` (`tauri.conf.json:35`) but no Homebrew tap, no release pipeline, no icons dir → bundle would fail. |

## Story 13.2 — Windows app

| AC | Status | Evidence / Gap |
|---|---|---|
| Targets Win10 1809+; ARM64 v1.1 | missing | No Windows target/min-version config beyond generic `wix` lang (`tauri.conf.json:52-54`). |
| Native chrome, snap zones, high-DPI | partial | Default Tauri window (`decorations: true`, `tauri.conf.json:22`) gives native chrome implicitly; no explicit DPI handling; unverifiable without build. |
| WebView2 auto-install via bootstrapper | missing | No `webviewInstallMode`/bootstrapper config. |
| Start Menu entry, `.maktaba` file assoc, taskbar pin | missing | No `fileAssociations` in `tauri.conf.json`; no shortcut/protocol handling. |
| EV-signed installer; SmartScreen passes | missing | No signing config; no CI. |

## Story 13.3 — Linux app

| AC | Status | Evidence / Gap |
|---|---|---|
| WebKitGTK; Ubuntu 22.04+/Fedora 38+/Debian 12+ | partial | Default Tauri Linux uses WebKitGTK; `linux.appimage.bundleMediaFramework: true` (`tauri.conf.json:57-60`) set. No distro testing matrix. |
| `.deb` installs `.desktop` launcher + registers MIME (`.maktaba`, `application/x-mpegurl`) | missing | No `linux.deb` config, no `desktop` entry, no MIME/`mimeType` registration. |
| `.AppImage` portable | partial | AppImage in `targets: "all"`; would not build (no icons dir). |
| Wayland + X11; fractional scaling | missing | No explicit handling; relies on defaults; unverified. |

## Story 13.4 — System tray integration

| AC | Status | Evidence / Gap |
|---|---|---|
| Tray icon on macOS/Win/Linux | partial | `TrayIconBuilder` (`lib.rs:38-61`) created with tooltip & menu. **No `.icon(...)` set** and no icons dir → tray icon likely renders blank/fails on macOS/Win. |
| Click → menu: Now Playing, Queue(count), Recently Added, Settings, Quit | **missing** | Tray menu has only `Show Maktaba / Hide / Quit` (`lib.rs:34-37`). None of the 5 required dynamic items exist. |
| "Now Playing" live; click opens player to video | missing | No now-playing state, no IPC from frontend, no deep-link to route. |
| Tray badge dot/count when jobs running (configurable) | missing | No badge/count logic; no job-state subscription; no setting. |
| Click-through closes window without quitting; Quit exits | partial | Quit works (`lib.rs:55-57`); Show/Hide work. No window-close-interception (`WindowEvent::CloseRequested` → hide) — closing the window quits, contradicting AC. `main.rs` imports `WindowEvent` but never uses it. |

## Story 13.5 — Local server auto-discovery (mDNS)

| AC | Status | Evidence / Gap |
|---|---|---|
| Advertise/resolve `_maktaba._tcp.local.` | **missing** | Plan specifies `src-tauri/src/mdns.rs` + `discover_servers` command using `mdns-sd` crate. File does not exist; `mdns-sd` not in `Cargo.toml`; no command registered (`invoke_handler` only has `app_version`, `lib.rs:65`). |
| First-launch wizard: list servers + manual entry | missing | No picker UI in `web/src/`; no `useQuery(['mdns','servers'])` (plan §83). |
| "Switch server" menu command re-opens picker | missing | No such command; menu unwired anyway. |
| Passive discovery (minimal bandwidth) | missing | N/A — nothing implemented. |
| QR pairing across LAN | missing | Not implemented. |

## Story 13.6 — File drag-and-drop

| AC | Status | Evidence / Gap |
|---|---|---|
| Drop-zone overlay per page "Drop here to add to {lib}" | **missing** | Plan specifies `web/src/features/desktop/DropOverlay.tsx` — does not exist. No drop overlay anywhere in `web/src/`. |
| Drop semantics: copy default / Shift move / Cmd+Ctrl reference | missing | No `WindowEvent::DragDrop` handler in `lib.rs` (only `fileDropEnabled: true` in `tauri.conf.json:26`, which Tauri 2 ignores — the modern key is per-window `dragDropEnabled` + a Rust handler). No modifier logic. |
| Video-extension filter; reject others with toast | missing | No extension validation. |
| Dropped file shows immediately in library with `DISCOVERED` + "Watching" badge | missing | No frontend integration; no scan/register API call. |
| Multi-file batched single progress toast | missing | Not implemented. |

## Story 13.7 — Keyboard shortcuts (desktop)

| AC | Status | Evidence / Gap |
|---|---|---|
| Menu maps all 11.9 shortcuts + desktop: `Cmd+N`, `Cmd+Shift+N`, `Cmd+1..9`, `Cmd+,`, `Cmd+R`, `Cmd+F` | **partial/unwired** | Only `Cmd+N` (new-window, `lib.rs:78`), `Cmd+,` (prefs, `:73`), `Cmd+R` (scan-library, `:107`) declared as accelerators — and **none are wired** (no menu event handler). Missing entirely: `Cmd+Shift+N` private session, `Cmd+1..9` library switch, `Cmd+F` focus search. |
| Native accelerators visible next to items | partial | Accelerators set on the items that exist; would display, but actions are no-ops. |
| Global media keys (Play/Pause/Next/Prev) when unfocused | **missing** | No `tauri-plugin-global-shortcut` in `Cargo.toml`; no media-key registration. |

## Story 13.8 — Auto-update

| AC | Status | Evidence / Gap |
|---|---|---|
| Channels `stable`/`beta` (opt-in Settings→Advanced) | **missing** | `tauri.conf.json:63-71` has a single hardcoded endpoint; no channel file read (plan §4 `read_channel_file`); no Settings UI. |
| Update check on launch + every 24h | missing | Updater plugin registered (`lib.rs:26`) but **never invoked** — no `check()` call in Rust or frontend; no 24h timer. |
| Ed25519-signed; pubkey bundled at build | partial | `pubkey: "REPLACE_WITH_PUBKEY"` placeholder (`tauri.conf.json:66`) — not a real key; signing not wired in CI. |
| "Update available" toast: Install on quit / Install now | missing | No updater UI in `web/src/`; no `@tauri-apps/plugin-updater` frontend usage. |
| Background download with resume; apply at restart | missing | No download orchestration code. |
| Version skew with server surfaced in Settings→About | missing | `app_version` command exists (`lib.rs:14-16`) but is **never called** by any frontend; no skew comparison. |

---

## Top gaps by impact

1. **Menu items are entirely dead (13.1/13.7).** `lib.rs:31` calls `app.set_menu(menu)` but no `on_menu_event` is attached at the app level (only the *tray* menu at `lib.rs:41` has one). Every custom menu item — Settings, New Window, Scan Library, Documentation — and every desktop accelerator is a visual-only no-op. This breaks the central AC of Stories 13.1 and 13.7.

2. **mDNS discovery (13.5) 100% missing.** No `mdns.rs`, no `mdns-sd` dependency, no `discover_servers` command, no picker UI. The desktop app cannot find a server — the primary onboarding path is non-functional.

3. **Drag-drop import (13.6) 100% missing.** No Rust `WindowEvent::DragDrop` handler, no `DropOverlay.tsx`, no copy/move/reference logic, no extension filter. The headline desktop value-add does not exist; `fileDropEnabled` in config is the legacy Tauri-1 key and is ignored by Tauri 2.

4. **No frontend integration at all.** `web/src/` contains zero `@tauri-apps` imports. Updater UX, server picker, drop overlay, version-skew display, theme-follow — every AC requiring native↔web IPC is unreachable.

5. **No packaging/signing/CI (13.1–13.3, 13.8).** No notarization, EV cert, MIME registration, file associations, Homebrew tap, or release workflow. `src-tauri/icons/` is absent, so `tauri build` would fail outright. `updater.pubkey` is a literal `"REPLACE_WITH_PUBKEY"` placeholder.

6. **Window-close quits the app (13.4).** No `CloseRequested` interception to hide-to-tray; contradicts the "click-through closes without quitting" AC. `main.rs` imports `WindowEvent`/`TrayIconEvent` but never uses them (dead imports).

## Summary counts

Approx. 38 discrete ACs across 8 stories:

- **Complete:** 0
- **Partial (scaffold/config only, behavior absent):** ~9
- **Unwired (code exists but unreachable — menu items, plugins, `app_version`):** ~5
- **Missing (no code):** ~24
- **Stub:** updater pubkey placeholder; tray menu (3 generic items vs 5 required dynamic).

The audit's claim that "menu/tray/window-state/updater exist" is **misleading**: they are *registered/declared* but functionally inert — no menu handler, no updater invocation, no tray icon asset, no route persistence. The audit is correct that 13.5/13.6/13.7 are absent; it understates that 13.4 and 13.8 are also non-functional and 13.1–13.3 have no packaging.
