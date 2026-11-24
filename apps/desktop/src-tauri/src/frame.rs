// Maktaba's own window frame (Epic 13 story 13.10, designed in 13.9:
// docs/design/desktop/README.md, "Components", "How each app gets there").
//
// The main window opens with the system's frame. A page that draws Maktaba's title bar (the
// web app's DesktopFrame) claims the frame with `frame_claim`:
//   - Windows: no system frame (Tauri keeps the resize edges, the shadow and the rounded
//     corners of an undecorated window); the menu bar is hidden, not removed, so its
//     keyboard shortcuts still work, and the title bar's menu lists its items.
//     Snap Layouts: the page asks for them when the pointer rests on its maximize button
//     (`frame_action("snap")`, Win+Z, Windows 11 only).
//   - Linux: no system frame; the header bar's buttons follow gtk-decoration-layout.
//   - macOS: the overlay title bar: the traffic lights stay, the page draws under them, the
//     title is hidden; the menu bar stays the Mac's own (localized, lib.rs build_menu).
//
// The window loads pages it does not ship: the local server's web app, which may be an older
// release, or a home in client mode, whose pages cannot call the shell at all. So the claim
// lasts for one page load: each load starts a new generation, and a page that has not claimed
// the frame CLAIM_WAIT after it began loading gets the system's frame back (`release`). A
// page that cannot claim it keeps the system's frame from the start.

use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

use serde::Serialize;
use tauri::menu::MenuItemKind;
use tauri::{AppHandle, Emitter, Manager, Runtime, WebviewWindow};

use crate::{lang, webcache};

/// How long a page has, after it began loading, to claim the frame before the system's
/// comes back.
pub const CLAIM_WAIT: Duration = Duration::from_secs(4);

/// The title bar's menu's own Quit (the menu bar's Quit is a predefined item, which a page
/// cannot trigger by id).
pub const QUIT_ID: &str = "frame-quit";

/// The event the window gets when it is maximized, restored or made full screen:
/// `{ "maximized": bool, "fullscreen": bool }`.
pub const STATE_EVENT: &str = "frame-state";

/// The frame's page loads (`generation`) and the last one that claimed it (`claimed`).
#[derive(Default)]
pub struct Frame {
    generation: AtomicU64,
    claimed: AtomicU64,
}

impl Frame {
    /// A page began loading: a new generation, not claimed.
    fn load_started(&self) -> u64 {
        self.generation.fetch_add(1, Ordering::SeqCst) + 1
    }

    /// The page now loaded claims the frame.
    fn claim(&self) {
        self.claimed
            .store(self.generation.load(Ordering::SeqCst), Ordering::SeqCst);
    }

    /// Whether generation `gen`'s page never claimed the frame, and no later page has loaded.
    fn unclaimed(&self, gen: u64) -> bool {
        self.generation.load(Ordering::SeqCst) == gen && self.claimed.load(Ordering::SeqCst) != gen
    }
}

/// What the page needs to draw the frame (camelCase in JSON).
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct FrameInfo {
    /// "windows" (caption buttons), "gtk" (header-bar buttons), "mac" (traffic lights)
    pub controls: &'static str,
    /// Linux: gtk-decoration-layout ("appmenu:close", ":minimize,maximize,close"); else ""
    pub layout: String,
    /// The side the window's controls sit on: "end" (Windows and GNOME, as laid out by the
    /// page's direction), or on the Mac "left"/"right" (where AppKit draws them)
    pub side: &'static str,
    pub maximized: bool,
    /// Windows 11's Snap Layouts can be asked for (Win+Z)
    pub snap_layouts: bool,
}

/// The page draws the frame: the system's goes (above). Main window only.
#[tauri::command]
pub fn frame_claim<R: Runtime>(window: WebviewWindow<R>) -> Result<FrameInfo, String> {
    if window.label() != webcache::MAIN {
        return Err("only the main window has Maktaba's frame".into());
    }
    let app = window.app_handle();
    app.state::<Frame>().claim();
    take_frame(&window).map_err(|e| e.to_string())?;
    Ok(FrameInfo {
        controls: controls(),
        layout: decoration_layout(),
        side: controls_side(),
        maximized: window.is_maximized().unwrap_or(false),
        snap_layouts: snap_layouts_available(),
    })
}

/// A caption button, or Snap Layouts: "minimize", "maximize" (toggles), "close", "snap".
#[tauri::command]
pub fn frame_action<R: Runtime>(window: WebviewWindow<R>, action: String) -> Result<(), String> {
    match action.as_str() {
        "minimize" => window.minimize(),
        "maximize" => {
            if window.is_maximized().unwrap_or(false) {
                window.unmaximize()
            } else {
                window.maximize()
            }
        }
        // the window's CloseRequested decides (to the tray, or quit: lib.rs)
        "close" => window.close(),
        "snap" => {
            let _ = window.set_focus();
            show_snap_layouts();
            Ok(())
        }
        other => return Err(format!("no frame action {other}")),
    }
    .map_err(|e| e.to_string())
}

/// One item of the title bar's menu.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MenuEntry {
    /// the menu item's id ("" for a separator)
    pub id: String,
    pub label: String,
    pub enabled: bool,
    /// a check item's state; absent for a plain item
    #[serde(skip_serializing_if = "Option::is_none")]
    pub checked: Option<bool>,
    pub separator: bool,
}

/// One of the menu bar's menus, as the title bar's menu shows it.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MenuGroup {
    pub title: String,
    pub items: Vec<MenuEntry>,
}

/// The menu bar's items, as they are now (labels, enabled, checks), for the title bar's menu:
/// the custom items of each menu, in order, with its separators; the system's own items
/// (copy, paste, minimize, full screen…) are left out, the page's keys and buttons do them;
/// and Quit at the end.
#[tauri::command]
pub fn frame_menu_items<R: Runtime>(app: AppHandle<R>) -> Vec<MenuGroup> {
    let mut groups = Vec::new();
    if let Some(menu) = app.menu() {
        for kind in menu.items().unwrap_or_default() {
            if let MenuItemKind::Submenu(sub) = kind {
                let mut items = Vec::new();
                for item in sub.items().unwrap_or_default() {
                    if let Some(e) = entry(&item) {
                        items.push(e);
                    }
                }
                let items = tidy(items);
                if !items.is_empty() {
                    groups.push(MenuGroup {
                        title: sub.text().unwrap_or_default(),
                        items,
                    });
                }
            }
        }
    }
    let lang = lang::Lang::system();
    groups.push(MenuGroup {
        title: String::new(),
        items: vec![MenuEntry {
            id: QUIT_ID.into(),
            label: lang.menu().quit.into(),
            enabled: true,
            checked: None,
            separator: false,
        }],
    });
    groups
}

/// A title-bar menu item chosen: as the menu bar's (lib.rs menu_event); Quit quits.
#[tauri::command]
pub fn frame_menu(app: AppHandle, id: String) -> Result<(), String> {
    if id == QUIT_ID {
        app.exit(0);
        return Ok(());
    }
    let known = frame_menu_items(app.clone())
        .iter()
        .flat_map(|g| g.items.iter())
        .any(|e| e.id == id && e.enabled && !e.separator);
    if !known {
        return Err(format!("no enabled menu item {id}"));
    }
    crate::menu_event(&app, &id);
    Ok(())
}

fn entry<R: Runtime>(item: &MenuItemKind<R>) -> Option<MenuEntry> {
    match item {
        MenuItemKind::MenuItem(m) => Some(MenuEntry {
            id: m.id().as_ref().to_string(),
            label: m.text().unwrap_or_default(),
            enabled: m.is_enabled().unwrap_or(true),
            checked: None,
            separator: false,
        }),
        MenuItemKind::Check(c) => Some(MenuEntry {
            id: c.id().as_ref().to_string(),
            label: c.text().unwrap_or_default(),
            enabled: c.is_enabled().unwrap_or(true),
            checked: c.is_checked().ok(),
            separator: false,
        }),
        // a predefined item with no words is a separator; the others are the system's
        MenuItemKind::Predefined(p) if p.text().unwrap_or_default().is_empty() => Some(MenuEntry {
            id: String::new(),
            label: String::new(),
            enabled: false,
            checked: None,
            separator: true,
        }),
        _ => None,
    }
}

/// No separator first, last or twice in a row.
pub fn tidy(items: Vec<MenuEntry>) -> Vec<MenuEntry> {
    let mut out: Vec<MenuEntry> = Vec::with_capacity(items.len());
    for e in items {
        let after_separator = match out.last() {
            None => true,
            Some(l) => l.separator,
        };
        if e.separator && after_separator {
            continue;
        }
        out.push(e);
    }
    while out.last().map(|l| l.separator).unwrap_or(false) {
        out.pop();
    }
    out
}

/// A page began loading in the main window: unless it claims the frame within CLAIM_WAIT,
/// the system's frame comes back.
pub fn on_page_load<R: Runtime>(window: &WebviewWindow<R>) {
    let gen = window.app_handle().state::<Frame>().load_started();
    let window = window.clone();
    std::thread::spawn(move || {
        std::thread::sleep(CLAIM_WAIT);
        if window.app_handle().state::<Frame>().unclaimed(gen) {
            if let Err(e) = release(&window) {
                crate::applog::warn("the window's frame could not be given back", e);
            }
        }
    });
}

/// The window was resized: the page's maximize button follows.
pub fn on_resized<R: Runtime>(window: &WebviewWindow<R>) {
    let maximized = window.is_maximized().unwrap_or(false);
    // full screen (big screen): the page draws no frame
    let fullscreen = window.is_fullscreen().unwrap_or(false);
    let _ = window.emit(
        STATE_EVENT,
        serde_json::json!({ "maximized": maximized, "fullscreen": fullscreen }),
    );
}

#[cfg(windows)]
fn take_frame<R: Runtime>(w: &WebviewWindow<R>) -> tauri::Result<()> {
    w.set_decorations(false)?;
    // the 1 px edge, the shadow and Windows 11's rounded corners
    w.set_shadow(true)?;
    let _ = w.hide_menu();
    Ok(())
}

#[cfg(windows)]
fn release<R: Runtime>(w: &WebviewWindow<R>) -> tauri::Result<()> {
    w.set_decorations(true)?;
    let _ = w.show_menu();
    Ok(())
}

#[cfg(target_os = "linux")]
fn take_frame<R: Runtime>(w: &WebviewWindow<R>) -> tauri::Result<()> {
    w.set_decorations(false)?;
    let _ = w.hide_menu();
    Ok(())
}

#[cfg(target_os = "linux")]
fn release<R: Runtime>(w: &WebviewWindow<R>) -> tauri::Result<()> {
    w.set_decorations(true)?;
    let _ = w.show_menu();
    Ok(())
}

#[cfg(target_os = "macos")]
fn take_frame<R: Runtime>(w: &WebviewWindow<R>) -> tauri::Result<()> {
    w.set_title_bar_style(tauri::TitleBarStyle::Overlay)?;
    mac::title_hidden(w, true);
    Ok(())
}

#[cfg(target_os = "macos")]
fn release<R: Runtime>(w: &WebviewWindow<R>) -> tauri::Result<()> {
    w.set_title_bar_style(tauri::TitleBarStyle::Visible)?;
    mac::title_hidden(w, false);
    Ok(())
}

#[cfg(windows)]
fn controls() -> &'static str {
    "windows"
}
#[cfg(target_os = "linux")]
fn controls() -> &'static str {
    "gtk"
}
#[cfg(target_os = "macos")]
fn controls() -> &'static str {
    "mac"
}

#[cfg(target_os = "macos")]
fn controls_side() -> &'static str {
    mac::controls_side()
}
#[cfg(not(target_os = "macos"))]
fn controls_side() -> &'static str {
    "end"
}

/// GTK's button layout (GNOME's default "appmenu:close"); "" elsewhere or when unset.
#[cfg(target_os = "linux")]
fn decoration_layout() -> String {
    use gtk::prelude::GtkSettingsExt;
    gtk::Settings::default()
        .and_then(|s| s.gtk_decoration_layout())
        .map(|s| s.to_string())
        .unwrap_or_default()
}
#[cfg(not(target_os = "linux"))]
fn decoration_layout() -> String {
    String::new()
}

/// Windows 11 (build 22000 and later): Snap Layouts.
#[cfg(windows)]
fn snap_layouts_available() -> bool {
    windows_build().map(|b| b >= 22000).unwrap_or(false)
}
#[cfg(not(windows))]
fn snap_layouts_available() -> bool {
    false
}

/// The Windows build number (the registry's CurrentBuildNumber).
#[cfg(windows)]
fn windows_build() -> Option<u32> {
    use windows_sys::Win32::System::Registry::{RegGetValueW, HKEY_LOCAL_MACHINE, RRF_RT_REG_SZ};
    let key: Vec<u16> = "SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\0"
        .encode_utf16()
        .collect();
    let name: Vec<u16> = "CurrentBuildNumber\0".encode_utf16().collect();
    let mut buf = [0u16; 32];
    let mut len = (buf.len() * 2) as u32;
    // SAFETY: the key and value names are NUL-terminated UTF-16; `len` is the buffer's size in
    // bytes, and the call writes at most that many.
    let rc = unsafe {
        RegGetValueW(
            HKEY_LOCAL_MACHINE,
            key.as_ptr(),
            name.as_ptr(),
            RRF_RT_REG_SZ,
            std::ptr::null_mut(),
            buf.as_mut_ptr().cast(),
            &mut len,
        )
    };
    if rc != 0 {
        return None;
    }
    let chars = (len as usize / 2).min(buf.len());
    String::from_utf16_lossy(&buf[..chars])
        .trim_end_matches('\0')
        .trim()
        .parse()
        .ok()
}

/// Windows 11's Snap Layouts for the focused window: Win+Z.
#[cfg(windows)]
fn show_snap_layouts() {
    use windows_sys::Win32::UI::Input::KeyboardAndMouse::{
        SendInput, INPUT, INPUT_0, INPUT_KEYBOARD, KEYBDINPUT, KEYEVENTF_KEYUP, VK_LWIN, VK_Z,
    };
    if !snap_layouts_available() {
        return;
    }
    let key = |vk: u16, up: bool| INPUT {
        r#type: INPUT_KEYBOARD,
        Anonymous: INPUT_0 {
            ki: KEYBDINPUT {
                wVk: vk,
                wScan: 0,
                dwFlags: if up { KEYEVENTF_KEYUP } else { 0 },
                time: 0,
                dwExtraInfo: 0,
            },
        },
    };
    let inputs = [
        key(VK_LWIN, false),
        key(VK_Z, false),
        key(VK_Z, true),
        key(VK_LWIN, true),
    ];
    // SAFETY: four initialized keyboard inputs, with their count and size.
    unsafe {
        SendInput(
            inputs.len() as u32,
            inputs.as_ptr(),
            std::mem::size_of::<INPUT>() as i32,
        );
    }
}
#[cfg(not(windows))]
fn show_snap_layouts() {}

#[cfg(target_os = "macos")]
mod mac {
    use objc2::MainThreadMarker;
    use objc2_app_kit::{
        NSApplication, NSUserInterfaceLayoutDirection, NSWindow, NSWindowTitleVisibility,
    };
    use tauri::{Runtime, WebviewWindow};

    /// Where AppKit draws the traffic lights: the left, or the right in a right-to-left
    /// interface.
    pub fn controls_side() -> &'static str {
        let Some(mtm) = MainThreadMarker::new() else {
            return "left";
        };
        let app = NSApplication::sharedApplication(mtm);
        if app.userInterfaceLayoutDirection() == NSUserInterfaceLayoutDirection::RightToLeft {
            "right"
        } else {
            "left"
        }
    }

    /// The window's title hidden (the page draws its own) or shown.
    pub fn title_hidden<R: Runtime>(w: &WebviewWindow<R>, hidden: bool) {
        let Ok(ptr) = w.ns_window() else { return };
        let ptr = ptr as usize;
        let _ = w.run_on_main_thread(move || {
            // SAFETY: tauri's NSWindow for this window, used on the main thread.
            let window: &NSWindow = unsafe { &*(ptr as *const NSWindow) };
            window.setTitleVisibility(if hidden {
                NSWindowTitleVisibility::Hidden
            } else {
                NSWindowTitleVisibility::Visible
            });
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn item(id: &str) -> MenuEntry {
        MenuEntry {
            id: id.into(),
            label: id.into(),
            enabled: true,
            checked: None,
            separator: false,
        }
    }
    fn sep() -> MenuEntry {
        MenuEntry {
            id: String::new(),
            label: String::new(),
            enabled: false,
            checked: None,
            separator: true,
        }
    }

    #[test]
    fn separators_never_lead_trail_or_double() {
        let out = tidy(vec![sep(), item("a"), sep(), sep(), item("b"), sep()]);
        assert_eq!(out, vec![item("a"), sep(), item("b")]);
        assert!(tidy(vec![sep(), sep()]).is_empty());
    }

    #[test]
    fn a_page_that_does_not_claim_gets_the_system_frame_back() {
        let f = Frame::default();
        let first = f.load_started();
        f.claim();
        assert!(
            !f.unclaimed(first),
            "claimed: the page keeps Maktaba's frame"
        );
        let second = f.load_started();
        assert!(f.unclaimed(second), "the next page has not claimed it");
        let third = f.load_started();
        assert!(
            !f.unclaimed(second),
            "a later load decides, not the earlier one's timer"
        );
        f.claim();
        assert!(!f.unclaimed(third));
    }

    #[test]
    fn the_info_is_camel_case() {
        let info = FrameInfo {
            controls: "windows",
            layout: String::new(),
            side: "end",
            maximized: false,
            snap_layouts: true,
        };
        let v = serde_json::to_value(&info).unwrap();
        assert_eq!(v["snapLayouts"], true);
        assert_eq!(v["controls"], "windows");
    }
}
