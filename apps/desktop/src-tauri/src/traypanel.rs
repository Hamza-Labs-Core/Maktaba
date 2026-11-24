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
//
// On Linux the window is never built, so the building and placing below go unused there; they
// stay compiled (and the placement's tests run) on every system.
#![cfg_attr(target_os = "linux", allow(dead_code))]

use std::sync::Mutex;
use std::time::{Duration, Instant};

use serde::Serialize;
use tauri::{
    AppHandle, Emitter, Manager, Position, Rect, WebviewUrl, WebviewWindow, WebviewWindowBuilder,
    WindowEvent,
};

use crate::{applog, shell, tray::Entry};

pub const LABEL: &str = "tray-panel";
/// The event the panel gets when the tray's entries change.
pub const STATE_EVENT: &str = "tray-panel-state";
/// The event the panel gets each time it is shown again (its first action takes the focus).
pub const OPENED_EVENT: &str = "tray-panel-opened";
/// A left click this soon after the panel hid on losing the focus was the click that took the
/// focus away (Windows gives it to the taskbar on the press): it closes the panel, as a
/// second click on an open flyout does, instead of opening it again.
const CLOSING_CLICK: Duration = Duration::from_millis(400);

/// When the panel last hid on losing the focus.
#[derive(Default)]
pub struct Panel {
    blurred: Mutex<Option<Instant>>,
}
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

/// The tray's entries changed: an open panel shows them (the panel's window only).
pub fn emit_state(app: &AppHandle) {
    if app.get_webview_window(LABEL).is_some() {
        let _ = app.emit_to(LABEL, STATE_EVENT, shell::tray_panel_state(app));
    }
}

/// The panel's window, made hidden when the tray is (at start, not in an event handler: a
/// window built in one can deadlock on Windows), so a click only shows it.
pub fn prepare(app: &AppHandle) {
    app.manage(Panel::default());
    if let Err(e) = build(app) {
        // no panel: a left click opens the window, as it did before
        applog::warn("the tray's panel could not be made", e);
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
    let Some(window) = app.get_webview_window(LABEL) else {
        // no panel (it could not be made): the window, as a left click opened it before
        shell::focus_main(app);
        return;
    };
    if window.is_visible().unwrap_or(false) {
        let _ = window.hide();
        return;
    }
    let blurred = app
        .try_state::<Panel>()
        .and_then(|p| p.blurred.lock().ok().and_then(|mut b| b.take()));
    if blurred.is_some_and(|at| at.elapsed() < CLOSING_CLICK) {
        return;
    }
    if let Some(pos) = position_for(app, icon) {
        let _ = window.set_position(pos);
    }
    emit_state(app);
    let _ = window.show();
    let _ = window.set_focus();
    let _ = app.emit_to(LABEL, OPENED_EVENT, ());
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
        .visible_on_all_workspaces(true)
        .skip_taskbar(true)
        .visible(false)
        .focused(true)
        .background_color(tauri::window::Color(0x18, 0x24, 0x3a, 0xff))
        .build()?;
    let w = window.clone();
    window.on_window_event(move |event| {
        // a click elsewhere closes it, as the system's flyouts close
        if let WindowEvent::Focused(false) = event {
            if w.is_visible().unwrap_or(false) {
                if let Some(p) = w.app_handle().try_state::<Panel>() {
                    if let Ok(mut b) = p.blurred.lock() {
                        *b = Some(Instant::now());
                    }
                }
                let _ = w.hide();
            }
        }
    });
    Ok(window)
}

/// Where the panel goes for an icon at `icon`, on the screen that shows it. The tray gives the
/// icon's rectangle in physical pixels, and each screen's rectangle is in the same pixels (its
/// own scale): the screen is the one whose rectangle holds the icon. Windows takes the panel's
/// place in physical pixels; the Mac takes it in points (the screen's scale divided out).
fn position_for(app: &AppHandle, icon: Rect) -> Option<Position> {
    let pos = icon.position.to_physical::<f64>(1.0);
    let size = icon.size.to_physical::<f64>(1.0);
    let centre = (pos.x + size.width / 2.0, pos.y + size.height / 2.0);
    let monitors = app.available_monitors().ok()?;
    let screens: Vec<Area> = monitors
        .iter()
        .map(|m| Area {
            x: m.position().x as f64,
            y: m.position().y as f64,
            w: m.size().width as f64,
            h: m.size().height as f64,
        })
        .collect();
    let monitor = &monitors[screen_of(&screens, centre)?];
    let scale = monitor.scale_factor();
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
    if cfg!(target_os = "macos") {
        Some(Position::Logical(tauri::LogicalPosition::new(
            x / scale,
            y / scale,
        )))
    } else {
        Some(Position::Physical(tauri::PhysicalPosition::new(
            x.round() as i32,
            y.round() as i32,
        )))
    }
}

/// The screen (by index) whose rectangle holds `point`.
pub fn screen_of(screens: &[Area], point: (f64, f64)) -> Option<usize> {
    screens.iter().position(|s| {
        point.0 >= s.x && point.0 < s.x + s.w && point.1 >= s.y && point.1 < s.y + s.h
    })
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
    fn the_icon_s_screen_is_the_one_that_holds_it_in_physical_pixels() {
        // a 2× Retina screen (1512 points: 3024 pixels) and a 1× screen to its right
        let screens = [
            Area {
                x: 0.0,
                y: 0.0,
                w: 3024.0,
                h: 1964.0,
            },
            Area {
                x: 3024.0,
                y: 0.0,
                w: 1920.0,
                h: 1080.0,
            },
        ];
        // an icon at 1200 points on the Retina screen is at 2400 pixels: still that screen
        assert_eq!(screen_of(&screens, (2414.0, 24.0)), Some(0));
        assert_eq!(screen_of(&screens, (3500.0, 12.0)), Some(1));
        assert_eq!(screen_of(&screens, (-5.0, 12.0)), None);
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
