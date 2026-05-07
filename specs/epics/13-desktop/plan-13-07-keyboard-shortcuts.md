# Implementation Plan — Story 13.7 Desktop Keyboard Shortcuts

> Companion to [story-13-07-keyboard-shortcuts.md](story-13-07-keyboard-shortcuts.md).
> Native menu accelerators + global media keys + the in-app layer from Story 11.9.
>
> ACL: see `plan-13-01-macos.md` §Capabilities. This story requires
> `src-tauri/capabilities/shortcut.json` (granting
> `global-shortcut:allow-register` and `global-shortcut:allow-unregister`).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Native accelerators | Defined per menu item in `src-tauri/src/menu.rs` (Story 13.1). |
| Global shortcuts | `tauri-plugin-global-shortcut` for `MediaPlayPause`, `MediaNextTrack`, `MediaPrevTrack`. |
| Multi-window | `Cmd/Ctrl+N` opens new window; `Cmd/Ctrl+Shift+N` opens private session (no shared local cache). |
| Library hotkeys | `Cmd/Ctrl+1..9` switch to library N. |
| Out of scope | Player keys (Story 11.3); generic in-app keys (Story 11.9). |

## 1. Menu accelerators (extends Story 13.1's menu)

```rust
// menu.rs – additions
let new_window      = MenuItem::with_id(app, "new_window",      "New Window")
    .accelerator("CmdOrCtrl+N").build()?;
let new_private     = MenuItem::with_id(app, "new_private",     "New Private Window")
    .accelerator("CmdOrCtrl+Shift+N").build()?;
let reload          = MenuItem::with_id(app, "reload",          "Reload")
    .accelerator("CmdOrCtrl+R").build()?;
let focus_search    = MenuItem::with_id(app, "focus_search",    "Find…")
    .accelerator("CmdOrCtrl+F").build()?;
let prefs           = MenuItem::with_id(app, "preferences",     "Settings…")
    .accelerator("CmdOrCtrl+,").build()?;

// Library 1..9 sub-items
let library_subs: Vec<_> = (1..=9).map(|i|
    MenuItem::with_id(app, format!("library_{i}"), format!("Library {i}"))
        .accelerator(format!("CmdOrCtrl+{i}")).build().unwrap()
).collect();
```

Menu actions emit Tauri events; the React layer routes them.

## 2. Action handlers

```rust
fn handle_menu(app: &AppHandle, id: &str) {
    match id {
        "new_window"   => open_new_window(app, /*private=*/false),
        "new_private"  => open_new_window(app, /*private=*/true),
        "reload"       => app.emit("menu:reload", ()),
        "focus_search" => app.emit("menu:focus_search", ()),
        "preferences"  => app.emit("menu:open_settings", ()),
        s if s.starts_with("library_") => {
            let n: usize = s["library_".len()..].parse().unwrap_or(0);
            let _ = app.emit("menu:switch_library", n);
        }
        _ => {}
    }
}
```

The "private" flag is implemented at the Tauri layer: the private
window is built with its own `data_directory` under the OS temp dir, so
cookies, localStorage, IndexedDB, and the service-worker cache are
isolated from the main window. See §6 below for the snippet.

## 3. Global media keys

```rust
use tauri_plugin_global_shortcut::{Code, GlobalShortcutExt, Modifiers, Shortcut, ShortcutState};

app.handle().plugin(
    tauri_plugin_global_shortcut::Builder::new()
        .with_handler(|app, shortcut, event| {
            if event.state == ShortcutState::Pressed {
                match shortcut.key {
                    Code::MediaPlayPause => app.emit("media:toggle_play", ()).ok(),
                    Code::MediaTrackNext => app.emit("media:next_chapter", ()).ok(),
                    Code::MediaTrackPrevious => app.emit("media:prev_chapter", ()).ok(),
                    _ => None,
                };
            }
        })
        .build()
)?;
app.global_shortcut().register(Shortcut::new(None, Code::MediaPlayPause))?;
app.global_shortcut().register(Shortcut::new(None, Code::MediaTrackNext))?;
app.global_shortcut().register(Shortcut::new(None, Code::MediaTrackPrevious))?;
```

The web player listens via `listen('media:toggle_play', …)` and dispatches into `playerApi`.

## 4. Conflict yielding

Global media keys use `tauri-plugin-global-shortcut`'s default behavior of relinquishing when another app most recently had foreground (Spotify/Apple Music). Documented per platform; tested manually.

## 5. Library N picker

```ts
listen('menu:switch_library', ({ payload: n }) => {
  const lib = libraries.value[n - 1];
  if (lib) navigate(`/library/${lib.id}`);
});
```

If `n > libraries.length`, no-op.

## 6. New private session

A query string (`?private=1`) read by the web layer is **not** sufficient
for actual privacy: WebView cookies, localStorage, IndexedDB, and the
service-worker cache are all keyed off the OS-level data directory, so
two windows sharing it leak credentials and history into the "private"
window. Real isolation comes from giving the private window its own
`data_directory`:

```rust
use std::path::PathBuf;

fn open_new_window(app: &AppHandle, private: bool) {
    let id = uuid::Uuid::new_v4();
    let label = format!("w_{id}");
    let mut builder = WebviewWindowBuilder::new(
        app,
        &label,
        WebviewUrl::App("index.html".into()),
    ).inner_size(1280.0, 800.0);

    if private {
        // Per-private-window data directory under the OS temp dir.
        // Cleaned up by the OS / on next boot; no shared state with the main window.
        let dir: PathBuf = std::env::temp_dir().join(format!("maktaba-private-{id}"));
        std::fs::create_dir_all(&dir).ok();
        builder = builder.data_directory(dir);
    }

    let _ = builder.build();
}
```

The web app may still inspect `window.__TAURI_PRIVATE__` (set via an
init script on the private window) to skip SW registration and choose
in-memory TanStack stores, but the **storage isolation is enforced at
the WebView level** by `data_directory`, not by web-side opt-ins.

## 7. Test cases

### 7.1 Manual

- `Cmd+R` reloads route without losing player state (player React state preserved by route key).
- Hardware Play/Pause key with Maktaba in background: playback toggles.
- Conflict with global OS shortcut: OS wins.
- `Cmd+1..9` switches library; `Cmd+0` no-op (deliberately not bound).

### 7.2 Smoke

- Build with global-shortcut plugin: no panics on macOS, Windows, Linux start.
- Menu accelerators visible in native menu bar.

## 8. Dependencies

- Story 13.1 (menu base).
- Story 11.9 (in-app key layer is the source of non-menu shortcuts).
- Story 11.3 player exposes `playerApi` for media-key handlers.
