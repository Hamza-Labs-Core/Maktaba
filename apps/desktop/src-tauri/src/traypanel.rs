// The tray's panel (Epic 13 story 13.12, designed in 13.9 and approved as D2: a designed
// panel on a left click, the system's menu on a right click; docs/design/desktop/mockups/
// tray.html).
//
// On Windows and the Mac a left click on the tray icon opens a small window under (or over)
// the icon: the web app's TrayPanel, from the app's own bundled pages. It shows exactly the
// tray menu's entries (shell.rs keeps them current: the status line, the language model, the
// update, the actions, the two ways to quit) and chooses them through the tray's own handler,
// so the panel and the menu can never say different things. It hides when it loses the focus,
// on Escape, and after a choice. The right click keeps the system's menu: the keyboard's and
// the screen reader's way in, and the only one on Linux, where the panel draws the menu itself
// (StatusNotifier).

use serde::Serialize;
use tauri::{
    AppHandle, Emitter, Manager, PhysicalPosition, Rect, WebviewUrl, WebviewWindow,
    WebviewWindowBuilder, WindowEvent,
};

use crate::{applog, shell, tray::Entry};

pub const LABEL: &str = "tray-panel";
/// The event the panel gets when the tray's entries change.
pub const STATE_EVENT: &str = "tray-panel-state";
/// The panel's size, in the page's (logical) pixels.
pub const WIDTH: f64 = 360.0;
pub const HEIGHT: f64 = 560.0;
/// Between the icon (or the screen's edge) and the panel, in logical pixels.
const GAP: f64 = 8.0;

/// What the panel draws.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct PanelState {
    /// "en" or "ar": the tray's language (the system's), which the panel speaks
    pub lang: &'static str,
    /// the status line's colour: "ok", "busy", "idle", "error", or "none" (no server here)
    pub tone: &'static str,
    pub entries: Vec<PanelEntry>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct PanelEntry {
    pub id: String,
    pub label: String,
    pub enabled: bool,
    pub separator: bool,
}

/// The tray's entries as the panel shows them.
pub fn panel_entries(entries: &[Entry]) -> Vec<PanelEntry> {
    entries
        .iter()
        .map(|e| match e {
            Entry::Item { id, label, enabled } => PanelEntry {
                id: (*id).to_string(),
                label: label.clone(),
                enabled: *enabled,
                separator: false,
            },
            Entry::Separator => PanelEntry {
                id: String::new(),
                label: String::new(),
                enabled: false,
                separator: true,
            },
        })
        .collect()
}

/// Whether `id` is an entry the panel may choose now: an enabled item, not a line of text.
pub fn choosable(entries: &[Entry], id: &str) -> bool {
    !matches!(id, "status" | "model-status")
        && entries
            .iter()
            .any(|e| matches!(e, Entry::Item { id: i, enabled: true, .. } if *i == id))
}

/// The tray panel's state, for the page.
#[tauri::command]
pub fn tray_panel_state(app: AppHandle) -> PanelState {
    shell::tray_panel_state(&app)
}

/// An entry chosen in the panel: the panel hides, then the tray's handler acts.
#[tauri::command]
pub fn tray_panel_choose(app: AppHandle, id: String) -> Result<(), String> {
    if !choosable(&shell::tray_entries(&app), &id) {
        return Err(format!("no enabled tray item {id}"));
    }
    hide(&app);
    shell::choose_tray_item(&app, &id);
    Ok(())
}

/// The panel hides (Escape).
#[tauri::command]
pub fn tray_panel_hide(app: AppHandle) {
    hide(&app);
}

/// The tray's entries changed: an open panel shows them.
pub fn emit_state(app: &AppHandle) {
    if let Some(w) = app.get_webview_window(LABEL) {
        let _ = w.emit(STATE_EVENT, shell::tray_panel_state(app));
    }
}

fn hide(app: &AppHandle) {
    if let Some(w) = app.get_webview_window(LABEL) {
        let _ = w.hide();
    }
}

/// A left click on the tray icon, at `icon`: the panel opens beside it, or closes when it is
/// open.
pub fn toggle(app: &AppHandle, icon: Rect) {
    let window = match app.get_webview_window(LABEL) {
        Some(w) => w,
        None => match build(app) {
            Ok(w) => w,
            Err(e) => {
                // no panel: the window, as a left click opened it before
                applog::warn("the tray's panel could not open", e);
                shell::focus_main(app);
                return;
            }
        },
    };
    if window.is_visible().unwrap_or(false) {
        let _ = window.hide();
        return;
    }
    if let Some(pos) = position_for(app, icon) {
        let _ = window.set_position(pos);
    }
    let _ = window.emit(STATE_EVENT, shell::tray_panel_state(app));
    let _ = window.show();
    let _ = window.set_focus();
}

fn build(app: &AppHandle) -> tauri::Result<WebviewWindow> {
    let window = WebviewWindowBuilder::new(app, LABEL, WebviewUrl::App("index.html".into()))
        // the web app draws the panel instead of itself (web/src/main.tsx)
        .initialization_script("window.__MAKTABA_PANEL__ = 'tray';")
        .title(shell::brand(app))
        .inner_size(WIDTH, HEIGHT)
        .resizable(false)
        .maximizable(false)
        .minimizable(false)
        .decorations(false)
        .shadow(true)
        .always_on_top(true)
        .skip_taskbar(true)
        .visible(false)
        .focused(true)
        .background_color(tauri::window::Color(0x18, 0x24, 0x3a, 0xff))
        .build()?;
    let w = window.clone();
    window.on_window_event(move |event| {
        // a click elsewhere closes it, as the system's flyouts close
        if let WindowEvent::Focused(false) = event {
            let _ = w.hide();
        }
    });
    Ok(window)
}

/// Where the panel goes for an icon at `icon`, on the screen that shows it.
fn position_for(app: &AppHandle, icon: Rect) -> Option<PhysicalPosition<i32>> {
    let probe = icon.position.to_physical::<f64>(1.0);
    let monitor = app.monitor_from_point(probe.x, probe.y).ok().flatten()?;
    let scale = monitor.scale_factor();
    let pos = icon.position.to_physical::<f64>(scale);
    let size = icon.size.to_physical::<f64>(scale);
    let work = monitor.work_area();
    let (x, y) = place(
        Area {
            x: pos.x,
            y: pos.y,
            w: size.width,
            h: size.height,
        },
        Area {
            x: work.position.x as f64,
            y: work.position.y as f64,
            w: work.size.width as f64,
            h: work.size.height as f64,
        },
        (WIDTH * scale, HEIGHT * scale),
        GAP * scale,
    );
    Some(PhysicalPosition::new(x.round() as i32, y.round() as i32))
}

/// A rectangle in physical pixels.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Area {
    pub x: f64,
    pub y: f64,
    pub w: f64,
    pub h: f64,
}

/// The panel's top-left corner for an icon at `icon` on a screen whose usable area is `work`
/// (the taskbar left out): over an icon in the screen's lower half (a taskbar at the bottom),
/// under one in its upper half (the Mac's menu bar, a taskbar at the top), beside one at the
/// screen's left or right edge (a taskbar there); always inside the work area.
pub fn place(icon: Area, work: Area, size: (f64, f64), gap: f64) -> (f64, f64) {
    let (w, h) = size;
    let icon_cx = icon.x + icon.w / 2.0;
    let icon_cy = icon.y + icon.h / 2.0;
    let left_bar = icon.x + icon.w <= work.x + gap;
    let right_bar = icon.x >= work.x + work.w - gap;
    let (x, y) = if left_bar {
        (work.x + gap, icon_cy - h / 2.0)
    } else if right_bar {
        (work.x + work.w - w - gap, icon_cy - h / 2.0)
    } else if icon_cy > work.y + work.h / 2.0 {
        (icon_cx - w / 2.0, icon.y - h - gap)
    } else {
        (icon_cx - w / 2.0, icon.y + icon.h + gap)
    };
    let x = x.clamp(work.x + gap, (work.x + work.w - w - gap).max(work.x + gap));
    let y = y.clamp(work.y + gap, (work.y + work.h - h - gap).max(work.y + gap));
    (x, y)
}

#[cfg(test)]
mod tests {
    use super::*;

    const SIZE: (f64, f64) = (360.0, 560.0);
    // a 1920×1080 screen whose taskbar takes 48 px at the bottom
    const WORK: Area = Area {
        x: 0.0,
        y: 0.0,
        w: 1920.0,
        h: 1032.0,
    };

    #[test]
    fn over_an_icon_in_the_bottom_taskbar_and_inside_the_screen() {
        let icon = Area {
            x: 1800.0,
            y: 1040.0,
            w: 32.0,
            h: 40.0,
        };
        let (x, y) = place(icon, WORK, SIZE, 8.0);
        assert_eq!(
            y,
            1032.0 - 560.0 - 8.0,
            "above the taskbar, inside the work area"
        );
        assert_eq!(
            x,
            1920.0 - 360.0 - 8.0,
            "kept on the screen at its right edge"
        );
    }

    #[test]
    fn under_the_mac_s_menu_bar() {
        let work = Area {
            x: 0.0,
            y: 25.0,
            w: 1512.0,
            h: 957.0,
        };
        let icon = Area {
            x: 1200.0,
            y: 0.0,
            w: 28.0,
            h: 24.0,
        };
        let (x, y) = place(icon, work, SIZE, 8.0);
        assert_eq!(
            y,
            25.0 + 8.0,
            "under the menu bar, a gap inside the work area"
        );
        assert_eq!(x, 1214.0 - 180.0, "centred on the icon");
    }

    #[test]
    fn beside_a_taskbar_at_the_left_edge() {
        let work = Area {
            x: 48.0,
            y: 0.0,
            w: 1872.0,
            h: 1080.0,
        };
        let icon = Area {
            x: 8.0,
            y: 980.0,
            w: 32.0,
            h: 32.0,
        };
        let (x, y) = place(icon, work, SIZE, 8.0);
        assert_eq!(x, 56.0, "to the right of the taskbar");
        assert_eq!(y, 1080.0 - 560.0 - 8.0, "kept on the screen");
    }

    #[test]
    fn a_line_of_text_is_never_chosen_and_a_turned_off_item_neither() {
        let entries = vec![
            Entry::Item {
                id: "status",
                label: "Maktaba is running".into(),
                enabled: false,
            },
            Entry::Item {
                id: "update",
                label: "Maktaba will update at 03:00".into(),
                enabled: false,
            },
            Entry::Separator,
            Entry::Item {
                id: "show",
                label: "Open Maktaba".into(),
                enabled: true,
            },
        ];
        assert!(choosable(&entries, "show"));
        assert!(!choosable(&entries, "status"));
        assert!(!choosable(&entries, "update"));
        assert!(!choosable(&entries, "stop-and-quit"), "not in the menu now");
        let shown = panel_entries(&entries);
        assert_eq!(shown.len(), 4);
        assert!(shown[2].separator);
        assert_eq!(shown[3].id, "show");
    }
}
