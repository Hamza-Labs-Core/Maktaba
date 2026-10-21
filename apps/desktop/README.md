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
    ├── LaunchAgents/       ← the server login item plist (Epic 36)
    └── src/
        ├── main.rs         ← thin entry → lib::run()
        ├── lib.rs          ← menu bar, deep links, mDNS, updater
        ├── shell.rs        ← Epic 36: menu-bar item, status poller, setup commands
        ├── status.rs       ←   supervisor /processes → running/starting/…
        ├── tray.rs         ←   the menu-bar menu as data
        ├── agent.rs        ←   login item (SMAppService), launchctl, --server-agent
        ├── setup.rs        ←   `maktaba-server setup --json` step runner
        └── mode.rs         ←   server mode vs client mode (mode.json)
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

## CI build & packaging

[`.github/workflows/desktop-release.yml`](../../.github/workflows/desktop-release.yml)
runs on every `v*` tag and builds the installers on all three native
runners in parallel, attaching them to the GitHub Release for the tag:

| Runner | Bundles |
|---|---|
| `macos-latest` | `.dmg` — **universal** (arm64 + x86_64 via `--target universal-apple-darwin`) |
| `windows-latest` | `.msi` + NSIS `.exe`, plus a portable `.zip` of the raw `Maktaba.exe` |
| `ubuntu-22.04` | `.deb` + `.AppImage` (installs `libwebkit2gtk-4.1`, `libayatana-appindicator3`, `librsvg2`, `patchelf` first) |

The build uses the official
[`tauri-apps/tauri-action@v0`](https://github.com/tauri-apps/tauri-action).
Because the repo has **no pnpm workspace**, the config's
`beforeBuildCommand` (`pnpm --filter maktaba-web build`) can't resolve in
CI — so the workflow builds `web/dist` itself (install → design tokens →
`vite build`) and passes
`--config {"build":{"beforeBuildCommand":""}}` so `tauri build` just
bundles the already-built frontend.

**Updates are signed; the installers are not yet.** The updater key is
set up: `TAURI_SIGNING_PRIVATE_KEY*` sign each updater bundle, and the
public key, the repository variable `TAURI_UPDATER_PUBKEY`, is stamped
into `tauri.conf.json` at build time (the committed file keeps the
`REPLACE_WITH_PUBKEY` placeholder; [auto-update](../../docs/auto-update.md)).
OS code signing is still deferred: when certificates are provisioned,
wire them in via the secrets documented at the top of the workflow
(`APPLE_*` for Developer ID + notarisation, `WINDOWS_CERTIFICATE*` for
Authenticode).

Build the same installers locally for the host OS with `make
package-desktop` (= `scripts/build.sh`).

## Native surface (`src-tauri/src/lib.rs`)

| Feature | Notes |
|---|---|
| **Menu bar** | custom items emit a `menu` event; `web/src/lib/desktop.ts` routes them (navigate / scan / server picker / Cmd+1..9 library slots) |
| **System tray** | **Play / Pause** (forward through the `menu` channel → `media-control` action, no window focus), Show / Hide, Settings, **Quit** |
| **Close-to-tray** | closing the window hides it; only Quit exits |
| **Deep links** | `maktaba://watch/{id}` etc. — scheme registered in `tauri.conf.json`; the Rust `on_open_url` handler emits a `deep-link` event the web shell maps to an SPA route via the same `deepLinkToPath` parser the Capacitor shell uses |
| **Single instance** | on Windows/Linux a `maktaba://` open spawns a new process; the single-instance plugin (with the `deep-link` feature) focuses the running window and forwards the URL |
| **mDNS** | `discover_servers` browses `_maktaba._tcp.local.` for the server picker |
| **Auto-update** | `updater` plugin checks `releases/latest/download/latest.json`; each update is signed with the updater key, whose public half (the repository variable `TAURI_UPDATER_PUBKEY`) is stamped into the build ([auto-update](../../docs/auto-update.md)) |

## Maktaba.app runs the server (Epic 36)

On a Mac the app is the whole installation ([spec](../../specs/epics/36-mac-app/README.md)).
A build that carries the server payload (`Contents/Resources/server/`, built by story 36.7)
has two modes, saved in `~/Library/Application Support/com.hamzalabs.maktaba/mode.json`:

| Mode | The window shows | The menu bar |
|---|---|---|
| **server** (this Mac runs Maktaba) | "Starting…" until the server answers, then the local web app `http://127.0.0.1:<web port>` | status (running / starting / problem / paused / stopped / setup needed), the language model (ready / downloading n% / not downloaded), *Update ready*, Open Maktaba, Pause/Resume server, Open logs, Quit (server keeps running), Stop server and quit |
| **client** (connect to another Maktaba) | that Maktaba's address, or the bundled web app as before | Open Maktaba, Connect to another Maktaba… (back to setup), Play/Pause, Settings, Quit |

With no saved mode the window opens the **first-run setup** (`web/src/pages/Setup/`):
this Mac or another Maktaba → what will run + disk/RAM → admin account → library folders and
NAS path map → models (Hugging Face token or the NAS copy) → optional Plex (PIN flow) →
install (pipeline venv + the language model + finish) → the server starts. The language
model (`qwen2.5:7b`, about 5 GB) stops setup only when the disk is too small for it; any
other failure leaves the download to the server, which finishes it in the background. Each screen runs one
`maktaba-server setup --json --step <name>` ([contract](../../specs/epics/36-mac-app/contract-setup.md));
passwords and tokens go to it on stdin only and are never logged.

**The login item.** `Contents/Library/LaunchAgents/com.hamzalabs.maktaba.server.plist` is
registered with `SMAppService.agent(plistName:)` when setup finishes (it then shows as
"Maktaba" in System Settings → Login Items). launchd runs `Maktaba --server-agent`, which
appends to `~/Library/Logs/Maktaba/server.log` and execs
`Contents/Resources/server/bin/maktaba-launcher serve`. Pause sends SIGTERM through
`launchctl kill`; `KeepAlive {SuccessfulExit: false}` restarts crashes but not a clean stop.
Resume is `launchctl kickstart`.

**Status** comes from the supervisor's `GET http://127.0.0.1:9103/processes`, polled every 3 s
(its `llm` field gives the language model's line).

**Developing it.** `tauri dev` runs outside a bundle, so point it at a `maktaba-server`
for the setup screens: `MAKTABA_SERVER_BIN=/path/to/maktaba-server pnpm tauri dev`.
`cargo test` in `src-tauri/` runs the shell's tests; CI runs them on macOS (`_unit.yml`).

## Out of scope for the scaffold

- Bundle icons — generate via `pnpm exec tauri icon ./logo.png`
  (see [`src-tauri/icons/README.md`](src-tauri/icons/README.md))
- Apple Developer ID / Microsoft EV cert / Linux signing
