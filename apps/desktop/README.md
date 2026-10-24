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
        ├── lang.rs         ←   the tray's words, English or Arabic
        ├── agent.rs        ←   login item (SMAppService), launchctl, --server-agent
        ├── agent_windows.rs ←  Epic 38: the sign-in task, the Windows agent, the stop event
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
./scripts/build.sh windows      # .msi + NSIS .exe     (on Windows; the client alone)
make win-app                    # the Windows app with the server inside: NSIS only (tools/release/win-app)
./scripts/build.sh linux        # .AppImage + .deb + .rpm, the shell only (on Linux;
                                #   `make linux-app` builds them with the server inside)
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
| `macos-15` (arm64) | `.dmg` of Maktaba.app **with the server inside** (`--target aarch64-apple-darwin`, `tools/release/mac-app/payload.sh`) |
| `macos-15` (x86_64) | `.dmg` of the client alone (`--target x86_64-apple-darwin`) |
| `windows-latest` | the NSIS `-setup.exe` with the server inside (`tools/release/win-app/payload.sh`; current user, `%LOCALAPPDATA%\Maktaba`, no `.msi`), plus a portable `.zip` of the raw `Maktaba.exe` (the client) |
| `ubuntu-22.04`, `ubuntu-22.04-arm` | `.AppImage` + `.deb` + `.rpm` with the server inside (Epic 39; no ffmpeg: the `.deb` and `.rpm` depend on the distribution's; installs `libwebkit2gtk-4.1`, `libayatana-appindicator3`, `librsvg2`, `patchelf`, `rpm`, and `ffmpeg` for the payload's check, first) |

Each runner builds with `tauri build` and checks its bundles; nothing is
published from there. The workflow's `publish` job, after every runner,
attaches the checked bundles to the release and then writes `latest.json`
(the updater feed, `tools/release/cmd/updater-feed`) over all of them, so
an updater is never pointed at a bundle that failed its checks.
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
| **Auto-update** | `updater` plugin checks `releases/latest/download/latest.json`; updates are signed with the Tauri updater key, whose public key `desktop-release.yml` stamps into `tauri.conf.json` at build time (the `TAURI_UPDATER_PUBKEY` variable; the repository keeps the placeholder; [auto-update](../../docs/auto-update.md)) |

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

## Windows runs the server too (Epic 38)

On Windows the same build with the server payload (`<install>\server\`, story 38.11) is the
whole installation ([spec](../../specs/epics/38-windows-app/README.md),
[contract](../../specs/epics/38-windows-app/contract-agent.md)). The modes, the setup screens
and the tray menu are the Mac's; what differs is how the server starts:

- **The sign-in task.** When setup finishes the shell registers the task
  `\Maktaba Server <the user's SID>` (one per user; this user, at sign-in + 30 s, least
  privilege, no time limit) through `schtasks /Create /XML` and runs it. A task of the earlier
  shared name `\Maktaba Server` that belongs to this user is moved to that name once. A Windows
  service is not used: it would run in session 0, where the user's mapped NAS drives (`O:`,
  `Y:`) do not exist.
- **The agent.** The task runs `Maktaba.exe --server-agent` (`agent_windows.rs`), a GUI
  program, which starts `server\bin\maktaba-launcher.exe --app-bundle <install>\server serve`
  with `CREATE_NO_WINDOW`: the launcher gets a console without a window that the server and
  every role inherit, so CTRL_BREAK reaches them and nothing flashes. The agent appends to
  `%LOCALAPPDATA%\Maktaba\logs\server.log`, restarts a failing launcher with backoff, and holds
  its tree in a kill-on-close job.
- **The watchdog.** Task Scheduler's "restart on failure" only retries a task that cannot
  start, not an action that crashed, so the task also triggers every 5 minutes; while the
  agent runs that does nothing (`IgnoreNew`), and an agent that died starts again.
- **Pause / Resume.** Pause sets the named event `Local\Maktaba.Server.Stop`: the launcher stops
  the server cleanly and exits 0, the agent exits 0. The pause is kept in
  `%LOCALAPPDATA%\Maktaba\state\server-paused` with the sign-in it belongs to: an agent the
  watchdog starts in that sign-in exits at once, the window restarting keeps it, and Resume
  (wait for a stopping agent, then `schtasks /Run`) or the next sign-in ends it.
- **Installer hooks.** `Maktaba.exe --server-start | --server-stop | --server-remove` (the
  NSIS hooks: stop the server before files are replaced, start it after, remove the task on
  uninstall). `--server-stop` and an update also set the install hold
  (`state\server-held`), so the watchdog does not start the server in the middle of an install;
  `--server-start`, Resume and the window's start end it. An update that fails after the stop
  starts the server again (unless it was paused).
- **Status.** As on the Mac, plus: while the agent runs but the supervisor has not answered
  for 3 minutes, the tray says "Maktaba has a problem: …" with the reason from `server.log`
  (another launcher holds this install, a port in use…), or "the server does not answer".
- **The tray icon** (story 38.4) carries a status badge drawn at run time on the app icon
  (`tray::badge`: red a problem or a server that does not answer, else violet an update ready,
  else green running, amber starting, grey paused or stopped; the Mac's menu-bar item stays
  plain). A left click opens the window, the right button the menu. While the server starts,
  the status line and the tooltip (up to Windows' 127 characters) show what the server reports
  a process waits on, or why it cannot start: its `detail` in `/processes`, which the server
  fills from story 38.12 (#194) on, a failing process's first. The window's starting screen
  says it in its own language from the detail's `detail_code` and `detail_params`
  (`setup.starting.detail.<code>` in `web/src/i18n`, one for each code of
  `cmd/maktaba-server/detail_codes.json`, the list the server embeds and the web test reads),
  with the `error`, `restore_error` and `log` values and the English under Details.
- **The tray's words and the error dialogs'** (`lang.rs`) are English or Arabic, from the
  system's UI language when the app starts (Windows' display language, the Mac's preferred
  languages, `LANGUAGE` / `LC_ALL` / `LC_MESSAGES` / `LANG` on Linux). An Arabic tray leaves the
  server's English detail out; a line of `server.log` that says why the server does not answer,
  and a program's own error under a dialog's sentence, are shown as they are.
- **mode.json** is in `%LOCALAPPDATA%\com.hamzalabs.maktaba\` (not the roaming profile, which
  would carry this PC's server mode to another PC); one in the roaming folder is moved once.
- Every program the shell starts (`schtasks`, the setup steps) is created without a window.

`cargo test` on Windows runs the agent's loop against a stand-in launcher; CI also registers
real tasks on `windows-latest` (`MAKTABA_TEST_WINDOWS_TASK=1`, never on a PC that runs
Maktaba): the XML is accepted, the watchdog starts a crashed stand-in agent again,
"restart on failure" does not, a running agent is not started twice and Resume waits for one
that is stopping, and an old-named task is replaced once.

**Developing it.** `tauri dev` runs outside a bundle, so point it at a `maktaba-server`
for the setup screens: `MAKTABA_SERVER_BIN=/path/to/maktaba-server pnpm tauri dev`.
`cargo test` in `src-tauri/` runs the shell's tests; CI runs them on macOS (`_unit.yml`).

## Out of scope for the scaffold

- Bundle icons — generate via `pnpm exec tauri icon ./logo.png`
  (see [`src-tauri/icons/README.md`](src-tauri/icons/README.md))
- Apple Developer ID / Microsoft EV cert / Linux signing

## Linux runs the server too (Epic 39)

On Linux the AppImage (or the .deb / .rpm) with the server payload (`/usr/share/maktaba/server`
inside it, story 39.2) is the whole installation
([spec](../../specs/epics/39-linux-app/README.md),
[contract](../../specs/epics/39-linux-app/contract-runtime.md)). The modes, the setup screens and
the tray menu are the Mac's. What differs is where the server runs and how it starts
(`agent_linux.rs`):

- **The runtime copy.** The server never runs from the app's files: an AppImage is mounted at a
  new `/tmp/.mount_*` path on every run, and a package upgrade replaces
  `/usr/share/maktaba/server` under a running server (the payload is there, not in `usr/lib`,
  because linuxdeploy rewrites ELF files under `usr/lib`). Before the first setup step after
  `status`, the shell runs the payload's own `maktaba-server runtime install --activate --owner
  app`, which copies the payload into `~/.local/share/maktaba/runtime/<version>` and points
  `current` at it. Every later step runs from `runtime/current/bin/maktaba-server` with
  `MAKTABA_APP_BUNDLE=runtime/current`, so the venv is built against the copy's Python, not the
  AppImage's mount.
- **The user service.** `maktaba-server service install` writes and starts the systemd user
  unit `maktaba-server.service`, which runs `runtime/current/bin/maktaba-launcher`.
  `service linger` lets it start at boot without a login: polkit allows that from the desktop
  session, and where it is refused, setup's *Allow* button asks again. Pause and Resume are
  `service stop` and `service start`. The shell never writes the unit itself.
- **After an update.** The Tauri updater replaces the AppImage, or installs the .deb / .rpm
  through the system's admin prompt. Switching the server to the new payload is the server's own
  (D3: the home's run, or its catch-up), not the app's: the app activates its payload only when
  it is newer than `runtime/current` and the server has not taken it yet, never the same release
  built again. The server stops while `current` moves; a paused server stays paused until
  Resume or the next boot. The app reads `XDG_DATA_HOME` from the unit's own `Environment=`, so
  it finds the update order and attempts where the server keeps them, and asks every 10 minutes
  whether its server runs a newer release (the catch-up offer).
- **An older app** (an old AppImage opened again, a package downgraded) never installs its
  payload: the server's database may be migrated past it. `runtime status` says `downgrade`,
  the newer runtime keeps running, and the app tells the user it is older than the server.
- **The tray** is a StatusNotifier item (libayatana-appindicator). Where no StatusNotifier host
  runs (GNOME without the AppIndicator extension), closing the window quits the app and the
  server keeps running, and the window's *Server* menu has the tray's items (status, Pause,
  Resume, Open logs, Stop server and quit, and an update when one is ready). With a host,
  `~/.config/autostart/maktaba.desktop` starts the tray at login with the window hidden
  (`--hidden`). The entry is written once: removed by the user, it stays removed. It follows the
  AppImage if it moves (never to an older app), keeps the user's on/off, and has `TryExec`, so the
  desktop skips it once the app is gone.
- **Language.** The shell's own words (the tray, its dialogs, the window's *Server* menu) come
  from `lang.rs`, as on Windows and the Mac: Arabic when gettext's order says so (`LANGUAGE`,
  then the first of `LC_ALL`, `LC_MESSAGES` and `LANG` that is set), else English. The
  autostart entry carries both languages.
- **Logs** are in `~/.local/state/maktaba/logs` (*Open logs* uses `xdg-open`).
