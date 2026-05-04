# Implementation Plan — Story 13.1 macOS App

> Companion to [story-13-01-macos.md](story-13-01-macos.md).
> Tauri 2 wrapper of the same web bundle as Epics 11–12.
> Universal binary; macOS 13+; notarized + hardened runtime.
>
> **Base scaffolding plan.** The other 7 Epic 13 plans (13-02 through
> 13-08) assume the Cargo workspace, `src-tauri/capabilities/` directory,
> and `tauri.conf.json` baseline established here.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Framework | Tauri 2.x. |
| Placement | `apps/desktop/` (Tauri root). `src-tauri/` Rust crate, `dist/` web bundle (synced from `web/dist`). |
| Targets | macOS 13+; universal2 (x86_64 + aarch64). |
| Signing | `apple-codesign` + Apple Developer ID + notarization via Apple `notarytool`. |
| Distribution | `.dmg` (notarized) + Homebrew Cask tap (`maktaba/tap/maktaba`). |
| Window state | Persisted via `tauri-plugin-window-state`. |
| Out of scope | System tray (Story 13.4); auto-update (13.8); mDNS (13.5); drag-and-drop (13.6); keyboard shortcuts (13.7). |

## 1. Project layout

```
apps/desktop/
├── package.json                 # scripts: dev, build, sync-web
├── tauri.conf.json
├── src-tauri/
│   ├── Cargo.toml
│   ├── build.rs
│   ├── src/
│   │   ├── main.rs              # Tauri entry, plugin registration
│   │   ├── menu.rs              # native menu bar
│   │   ├── window.rs            # window builder + state restore
│   │   └── platform/
│   │       ├── macos.rs         # macOS-only hooks (NSApplicationDelegate)
│   │       ├── windows.rs       # Story 13.2
│   │       └── linux.rs         # Story 13.3
│   ├── icons/
│   │   ├── icon.icns
│   │   └── icon.ico
│   └── entitlements.plist       # hardened runtime entitlements
├── dist/                        # web bundle copied here
└── scripts/
    ├── sync-web.ts              # cp -R ../../web/dist ./dist
    ├── build-mac.sh             # universal2 + sign + notarize
    └── build-mac-dmg.sh         # bundle .dmg
```

## 2. tauri.conf.json (excerpt)

```json
{
  "productName": "Maktaba",
  "version": "0.1.0",
  "identifier": "com.maktaba.desktop",
  "build": {
    "beforeBuildCommand": "npm run sync-web",
    "frontendDist": "dist"
  },
  "app": {
    "windows": [{
      "label": "main",
      "title": "Maktaba",
      "width": 1280, "height": 800,
      "minWidth": 800, "minHeight": 600,
      "center": true,
      "decorations": true,
      "fullscreen": false,
      "transparent": false
    }],
    "trayIcon": null
  },
  "bundle": {
    "active": true,
    "targets": ["dmg", "app"],
    "macOS": {
      "minimumSystemVersion": "13.0",
      "hardenedRuntime": true,
      "entitlements": "src-tauri/entitlements.plist",
      "exceptionDomain": "",
      "signingIdentity": "Developer ID Application: Maktaba (TEAMID)",
      "providerShortName": null
    }
  }
}
```

## 3. Native menu

```rust
// menu.rs
pub fn build_menu(app: &AppHandle) -> Menu<Wry> {
    let app_menu = SubmenuBuilder::new(app, "Maktaba")
        .text("about", "About Maktaba")
        .separator()
        .text("preferences", "Settings…").accelerator("Cmd+,")
        .separator()
        .quit().accelerator("Cmd+Q")
        .build().unwrap();

    let file_menu = SubmenuBuilder::new(app, "File")
        .text("new_window", "New Window").accelerator("Cmd+N")
        .text("close_window", "Close Window").accelerator("Cmd+W")
        .build().unwrap();

    let view_menu = SubmenuBuilder::new(app, "View")
        .text("reload", "Reload").accelerator("Cmd+R")
        .text("toggle_fullscreen", "Toggle Fullscreen").accelerator("Ctrl+Cmd+F")
        .build().unwrap();

    Menu::with_items(app, &[&app_menu, &file_menu, &view_menu]).unwrap()
}
```

Menu actions emit Tauri events that the React app listens to (`@tauri-apps/api/event`).

## 4. Window state restore

```toml
# Cargo.toml
[dependencies]
tauri-plugin-window-state = "2"
```

```rust
// main.rs
fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_window_state::Builder::default().build())
        .menu(build_menu)
        .setup(|app| {
            #[cfg(target_os = "macos")] platform::macos::setup(app)?;
            window::restore_state(app)?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

`tauri-plugin-window-state` saves position+size in `~/Library/Application Support/com.maktaba.desktop/window-state.json`.

## 5. Theme

The web bundle's `<ThemeProvider>` (Story 11.8) reads `system` by default. macOS dark mode flips via `prefers-color-scheme` already; no additional native code required. We do override `NSWindow.appearance` so the title bar follows:

```rust
// macos.rs
use cocoa::base::id;
use cocoa::appkit::{NSAppearance, NSAppearanceNameDarkAqua, NSAppearanceNameAqua};

pub fn apply_appearance(window: &Window, dark: bool) {
    let ns_window = window.ns_window().unwrap() as id;
    unsafe {
        let name = if dark { NSAppearanceNameDarkAqua } else { NSAppearanceNameAqua };
        let appearance: id = msg_send![class!(NSAppearance), appearanceNamed: name];
        let _: () = msg_send![ns_window, setAppearance: appearance];
    }
}
```

The web side fires a Tauri command on theme change.

## 6. Multi-window

`Cmd+N` opens a new window via the web `app.emit` handler:

```rust
.invoke_handler(tauri::generate_handler![open_new_window])
```

```rust
#[tauri::command]
fn open_new_window(app: AppHandle) {
    let _ = WebviewWindowBuilder::new(&app, format!("w_{}", uuid::Uuid::new_v4()), WebviewUrl::App("index.html".into()))
        .title("Maktaba")
        .inner_size(1280.0, 800.0)
        .build();
}
```

## 7. Build & notarization

`scripts/build-mac.sh`:

```bash
npm run sync-web
cd src-tauri
cargo tauri build --target universal-apple-darwin
codesign --force --deep --options runtime --entitlements entitlements.plist \
  --sign "Developer ID Application: Maktaba (TEAMID)" \
  ../target/universal-apple-darwin/release/bundle/macos/Maktaba.app
xcrun notarytool submit ../target/universal-apple-darwin/release/bundle/macos/Maktaba.dmg \
  --apple-id "$APPLE_ID" --team-id "$TEAM_ID" --password "$NOTARY_PASS" --wait
xcrun stapler staple ../target/universal-apple-darwin/release/bundle/macos/Maktaba.dmg
```

CI runs on macOS GitHub Actions with secrets injected.

## 8. Edge cases

| Case | Handling |
|---|---|
| macOS 12 launch | Tauri's `minimum-system-version` rejects with friendly OS dialog. We optionally show a custom installer message in the DMG. |
| Notarization revoked on stale build | Document publish-new-build process. |
| Multiple windows | Each has independent state; window-state plugin keys per `label`. |

## 9. Test cases

### 9.1 Smoke (CI)

- `cargo tauri build` succeeds.
- Notarization step succeeds in nightly run.
- DMG opens, `.app` mountable via `hdiutil`.

### 9.2 Manual

- Install via DMG, drag to Applications, first launch: no Gatekeeper prompt.
- Quit + relaunch: window opens at last position with last route.
- macOS dark mode toggle: app follows.

## 10. Performance

- Cold launch ≤ 2 s on Apple Silicon.
- Idle RAM ~80 MB.

## 11. Dependencies

- Web bundle from Epic 11.
- Stories 13.4 (tray), 13.5 (mDNS), 13.6 (drag-drop), 13.7 (shortcuts), 13.8 (auto-update) extend this base.

## 12. Single-instance lock

A single-instance lock prevents two app processes from racing on the same
machine (window-state file, updater, tray icon ownership). Implemented via
`tauri-plugin-single-instance`; cross-platform (used here for macOS and
referenced from plans 13-02 / 13-03).

```toml
# Cargo.toml
[dependencies]
tauri-plugin-single-instance = "2"
```

```rust
// main.rs
fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        // ... other plugins
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

A second-launch invocation is intercepted; the running instance is
focused. `args` carries the second-launch argv (used by Story 13.2 for
`.maktaba` deep-link handoff).

## 13. Capabilities (Tauri 2 ACL)

Tauri 2 enforces an Access Control List per command/plugin via JSON files
under `src-tauri/capabilities/`. Without these files, every `invoke()`,
plugin call, fs operation, global-shortcut registration, and updater call
fails at runtime with a permission-denied error.

```
src-tauri/capabilities/
  desktop.json          # main window: core:default, core:webview:default, core:event:default
  fs.json               # plan-13-06: fs:allow-read-file, fs:allow-write-file scoped to library roots
  tray.json             # plan-13-04: core:tray:default
  updater.json          # plan-13-08: updater:default
  shortcut.json         # plan-13-07: global-shortcut:allow-register, allow-unregister
  notification.json     # notification:default (for download-complete toasts)
  single-instance.json  # §12 above: single-instance:default
```

Sample `src-tauri/capabilities/desktop.json` skeleton:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "desktop",
  "description": "Capabilities required by the main Maktaba window.",
  "windows": ["main"],
  "permissions": [
    "core:default",
    "core:webview:default",
    "core:event:default",
    "core:window:allow-show",
    "core:window:allow-hide",
    "core:window:allow-set-focus"
  ]
}
```

`tauri.conf.json` references these by identifier:

```json
"app": {
  "security": {
    "capabilities": ["desktop", "fs", "tray", "updater", "shortcut", "notification", "single-instance"]
  }
}
```

Each downstream plan that adds a new capability lists it in its own
"ACL" section and refers back here.
