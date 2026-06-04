# Maktaba desktop (Tauri 2)

macOS, Windows, and Linux desktop apps via **Tauri 2**. The frontend is
the same `web/dist` bundle the browser and mobile clients run — the
native shell adds the menu bar, system tray, window-state persistence,
file dialogs, `maktaba://` deep links, and the auto-update channel.

- **identifier / bundle id:** `com.hamzalabs.maktaba`
- **title:** `Maktaba`
- **window:** 1280×800, resizable (min 960×600), centered, dark title bar
- **dev URL:** Vite dev server `http://localhost:5173`
- **frontend:** `../../../web/dist` (built by `beforeBuildCommand`)

## Layout

```
apps/desktop/
├── package.json            ← Tauri JS bindings + scripts
├── scripts/build.sh        ← per-platform `tauri build` wrapper
└── src-tauri/
    ├── Cargo.toml          ← Rust deps (tauri + plugins)
    ├── tauri.conf.json     ← bundle id, window, CSP, deep-link, updater
    ├── capabilities/       ← per-window permission allowlists
    ├── icons/              ← bundle icons (see icons/README.md)
    └── src/
        ├── main.rs         ← thin entry → lib::run()
        └── lib.rs          ← menu bar, tray, deep links, mDNS, updater
```

## Prerequisites

- **Rust** toolchain — https://rustup.rs
- Platform build deps:
  - **macOS:** Xcode Command Line Tools
  - **Windows:** Microsoft C++ Build Tools + WebView2 (preinstalled on Win 11)
  - **Linux:** `libwebkit2gtk-4.1-dev`, `libgtk-3-dev`, `libayatana-appindicator3-dev`, `librsvg2-dev`, `patchelf`
- **Node + pnpm** (for the web bundle and Tauri CLI)

## Build

```bash
# from repo root
make desktop-build              # = apps/desktop/scripts/build.sh (host OS)

# or directly, selecting the host platform's installers
cd apps/desktop
pnpm install
pnpm tauri dev                  # dev window pointed at the Vite dev server
./scripts/build.sh macos        # .app + .dmg          (on macOS)
./scripts/build.sh windows      # .msi + NSIS .exe     (on Windows)
./scripts/build.sh linux        # .deb + .AppImage     (on Linux)
./scripts/build.sh debug        # fast unsigned debug build
```

Tauri does **not** cross-compile the native shell, so each OS is built on
its own runner; `scripts/build.sh` refuses a target that doesn't match the
host. Installers land in `src-tauri/target/release/bundle/`.

## Native surface (`src-tauri/src/lib.rs`)

| Feature | Notes |
|---|---|
| **Menu bar** | custom items emit a `menu` event; `web/src/lib/desktop.ts` routes them (navigate / scan / server picker / Cmd+1..9 library slots) |
| **System tray** | **Play / Pause** (forward through the `menu` channel → `media-control` action, no window focus), Show / Hide, Settings, **Quit** |
| **Close-to-tray** | closing the window hides it; only Quit exits |
| **Deep links** | `maktaba://watch/{id}` etc. — scheme registered in `tauri.conf.json`; the Rust `on_open_url` handler emits a `deep-link` event the web shell maps to an SPA route via the same `deepLinkToPath` parser the Capacitor shell uses |
| **Single instance** | on Windows/Linux a `maktaba://` open spawns a new process; the single-instance plugin (with the `deep-link` feature) focuses the running window and forwards the URL |
| **mDNS** | `discover_servers` browses `_maktaba._tcp.local.` for the server picker |
| **Auto-update** | `updater` plugin checks `releases/latest/download/latest.json`; signing key is a follow-up (`pubkey` placeholder in config) |

## Out of scope for the scaffold

- Bundle icons — generate via `pnpm exec tauri icon ./logo.png`
  (see [`src-tauri/icons/README.md`](src-tauri/icons/README.md))
- Apple Developer ID / Microsoft EV cert / Linux signing
- The updater private key (the `pubkey` in `tauri.conf.json` is a
  placeholder until the signing key lands)
