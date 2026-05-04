# Implementation Plan — Story 13.4 System Tray

> Companion to [story-13-04-system-tray.md](story-13-04-system-tray.md).
> Tray icon on macOS menu bar, Windows system tray, Linux system tray.
> Uses Tauri 2's `tauri-plugin-system-tray` (built-in `tray` API).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Implementation | `src-tauri/src/tray.rs`. |
| Icons | Monochrome variants for macOS dark menu bar; full-color for Win/Linux. |
| State | Tray menu rebuilds when Now Playing or Queue counts change (subscribed to web events). |
| Click semantics | macOS/Windows: click closes window (without quit) unless user disabled in Settings. Quit menu item exits. |
| Linux | Best-effort; document AppIndicator extension fallback for GNOME 40+. |
| Out of scope | Settings → Tray section UI lives in Story 11.6. |

## 1. tauri.conf.json

```json
"app": {
  "trayIcon": {
    "id": "main",
    "iconPath": "src-tauri/icons/tray.png",
    "iconAsTemplate": true,
    "menuOnLeftClick": false,
    "tooltip": "Maktaba"
  }
}
```

`iconAsTemplate: true` makes macOS render the icon monochrome appropriate for dark/light menu bar.

## 2. tray.rs

```rust
pub fn build_tray(app: &AppHandle) -> tauri::Result<TrayIcon<Wry>> {
    let menu = build_menu(app, &TrayState::default())?;
    let tray = TrayIconBuilder::with_id("main")
        .menu(&menu)
        .icon(app.default_window_icon().unwrap().clone())
        .icon_as_template(true)
        .on_menu_event(|app, event| handle_menu(app, event.id().as_ref()))
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click { button: MouseButton::Left, .. } = event {
                if let Some(window) = tray.app_handle().get_webview_window("main") {
                    let _ = window.show(); let _ = window.set_focus();
                }
            }
        })
        .build(app)?;
    Ok(tray)
}

fn build_menu(app: &AppHandle, state: &TrayState) -> tauri::Result<Menu<Wry>> {
    let now_playing = MenuItemBuilder::with_id("now_playing", state.now_playing_label())
        .enabled(state.now_playing.is_some())
        .build(app)?;
    let queue = MenuItemBuilder::with_id("queue", format!("Queue ({})", state.queue_count))
        .build(app)?;
    let recents_submenu = SubmenuBuilder::new(app, "Recently Added")
        .items(&state.recents.iter().enumerate().map(|(i, v)|
            &MenuItemBuilder::with_id(format!("recent_{i}"), &v.title).build(app).unwrap()
        ).collect::<Vec<_>>())
        .build()?;
    let settings = MenuItemBuilder::with_id("settings", "Settings…").accelerator("Cmd+,").build(app)?;
    let quit = MenuItemBuilder::with_id("quit", "Quit").accelerator("Cmd+Q").build(app)?;

    MenuBuilder::new(app)
        .item(&now_playing).separator()
        .item(&queue).item(&recents_submenu).separator()
        .item(&settings).separator().item(&quit).build()
}
```

## 3. Live state updates

The web layer emits Tauri events when state changes:

```ts
// web/src/features/tray/sync.ts
import { invoke } from '@tauri-apps/api/core';

watch(currentlyPlaying, (np) => invoke('tray_update', { now_playing: np }));
watch(queueCount, (n) => invoke('tray_update', { queue_count: n }));
watch(recents, (r) => invoke('tray_update', { recents: r.slice(0, 8) }));
```

```rust
#[tauri::command]
fn tray_update(app: AppHandle, payload: TrayState) -> tauri::Result<()> {
    let tray = app.tray_by_id("main").unwrap();
    let menu = build_menu(&app, &payload)?;
    tray.set_menu(Some(menu))?;
    Ok(())
}
```

Renders throttled to ≤ 1 Hz to avoid flicker.

## 4. Click-through behavior

`X`/Close button on the window dispatches `WindowEvent::CloseRequested`; we hide instead of destroy when tray is enabled:

```rust
.on_window_event(|window, event| {
    if let WindowEvent::CloseRequested { api, .. } = event {
        if tray_enabled() {
            let _ = window.hide();
            api.prevent_close();
        }
    }
})
```

`tray_enabled()` reads a setting (default `true`). Settings → System (Story 11.6 nested under Appearance) toggles this; when off, close = quit.

## 5. Edge cases

| Case | Handling |
|---|---|
| Linux GNOME 40+ no tray | Document AppIndicator extension; tray gracefully degrades (no icon, app behaves as if `tray_enabled=false`). |
| User disables tray | Window-close means quit; tray menu still functional if user re-enables. |
| Multiple windows | Tray actions target the most-recent main window. |

## 6. Test cases

### 6.1 Smoke

- Build with tray enabled: tray icon present after launch.
- `tray_update` command invocation rebuilds menu with expected labels.

### 6.2 Manual

- Start a transcribe job: tray icon shows badge with count.
- Click "Now Playing": window comes to foreground at the correct route.
- Quit from tray: app exits; downloads paused (Story 12.6 behavior carries over, though desktop has no downloads in v1).

## 7. Performance

- Menu rebuild ≤ 5 ms.
- Throttled updates avoid > 1 Hz flicker.

## 8. Dependencies

- Story 13.1 (Tauri base).
- Tray-toggle setting in Story 11.6 (Appearance → System).
