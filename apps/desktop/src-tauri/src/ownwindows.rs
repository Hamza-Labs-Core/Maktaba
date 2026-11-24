// Setup and status as their own windows (Epic 13 story 13.13; the design:
// docs/design/desktop/README.md, "How each app gets there", and the mockups setup-welcome,
// setup-check and server-status).
//
// Both are 960 × 640 windows of the app's own bundled pages, marked before the page loads
// (window.__MAKTABA_PANEL__ = "setup" or "status": web/src/main.tsx), drawn with Maktaba's
// frame from the first frame (no system title bar on Windows and Linux; the overlay title bar
// with the traffic lights on the Mac), like the main window once a page claims it (frame.rs).
//
//   - Setup: while this machine is not set up, the setup runs in its own window and the main
//     window stays hidden (it would show nothing). Once the setup opens a server (this
//     machine's, or another Maktaba's), the main window loads it and shows, and the setup
//     window goes (setup_done). Closing it hides it to the tray, like the main window (Linux
//     without a tray: the app quits); Open Maktaba brings it back. If its window cannot be
//     made, the main window shows the setup as before.
//   - Status: the server's status, its parts and its update, with the tray's server actions
//     (Server status in the tray, and in the Server menu on Linux without a tray). Closing it
//     closes it; the server keeps running. It follows each status refresh (status_changed).
//
// Windows are built off the main thread: the tray's and the menu's handlers ask for them, and
// building a window there can deadlock on Windows.

use std::sync::atomic::{AtomicBool, Ordering};

use tauri::{
    AppHandle, Emitter, Manager, WebviewUrl, WebviewWindow, WebviewWindowBuilder, WindowEvent,
};

use crate::statusview::StatusWindowView;
use crate::webcache;

pub const SETUP: &str = "setup";
pub const STATUS: &str = "status";
/// Both windows' size (the mockups'), and their least.
pub const WIDTH: f64 = 960.0;
pub const HEIGHT: f64 = 640.0;
/// The status window's event with a new view (its window only).
pub const STATUS_EVENT: &str = "status-window";

/// One of the two windows.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Kind {
    Setup,
    Status,
}

impl Kind {
    pub fn label(self) -> &'static str {
        match self {
            Kind::Setup => SETUP,
            Kind::Status => STATUS,
        }
    }

    pub fn of(label: &str) -> Option<Kind> {
        match label {
            SETUP => Some(Kind::Setup),
            STATUS => Some(Kind::Status),
            _ => None,
        }
    }

    /// The mark the page reads before it draws (web/src/lib/ownWindow.ts).
    pub fn script(self) -> String {
        format!("window.__MAKTABA_PANEL__ = \"{}\";", self.label())
    }
}

/// Whether the page in window `label` draws Maktaba's frame: the main window (once its page
/// claims it), and the setup and status windows.
pub fn has_frame(label: &str) -> bool {
    label == webcache::MAIN || Kind::of(label).is_some()
}

/// The setup runs in its own window: set before that window is made, so the main window's
/// page, which asks first, shows nothing; cleared when it cannot be made or once the setup
/// is done.
#[derive(Default)]
pub struct SetupApart(AtomicBool);

impl SetupApart {
    pub fn get(&self) -> bool {
        self.0.load(Ordering::SeqCst)
    }
    fn set(&self, on: bool) {
        self.0.store(on, Ordering::SeqCst);
    }
}

/// Whether closing a window keeps the app in its tray (lib.rs): not on Linux without a tray,
/// where closing the window quits.
pub struct KeepInTray(pub bool);

/// The setup is to run in its own window: the main window's page shows nothing from now on.
pub fn claim_setup(app: &AppHandle) {
    app.state::<SetupApart>().set(true);
}

/// Whether window `label`'s setup screens run elsewhere (app_state's setup_window): the main
/// window's, while the setup window is (being) made.
pub fn setup_elsewhere(apart: bool, label: &str) -> bool {
    apart && label != SETUP
}

/// What a button of the status window does: a tray item's action, or a page of this
/// machine's web app in the main window.
#[derive(Debug, PartialEq, Eq)]
pub enum Act {
    Tray(&'static str),
    Page(&'static str),
}

/// The status window's buttons, and nothing else.
pub fn act(action: &str) -> Option<Act> {
    Some(match action {
        "open" => Act::Tray("show"),
        "pause" => Act::Tray("server-pause"),
        "resume" => Act::Tray("server-resume"),
        "logs" => Act::Tray("open-logs"),
        "report" => Act::Tray("report-problem"),
        "settings" => Act::Tray("tray-settings"),
        "update" => Act::Tray("update"),
        "libraries" => Act::Page("/admin/libraries"),
        "devices" => Act::Page("/account/devices"),
        _ => return None,
    })
}

/// Shows window `kind`, making it (off the main thread) when there is none.
pub fn show(app: &AppHandle, kind: Kind) {
    if kind == Kind::Setup {
        claim_setup(app);
    }
    if let Some(w) = app.get_webview_window(kind.label()) {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
        return;
    }
    let app = app.clone();
    std::thread::spawn(move || {
        if let Err(e) = build(&app, kind) {
            crate::applog::warn(&format!("the {} window could not open", kind.label()), e);
            if kind == Kind::Setup {
                // the main window shows the setup screens, as before this story
                app.state::<SetupApart>().set(false);
                crate::shell::setup_in_main(&app);
            }
        }
    });
}

fn build(app: &AppHandle, kind: Kind) -> tauri::Result<()> {
    let words = crate::shell::words(app);
    let title = match kind {
        Kind::Setup => words.setup_title,
        Kind::Status => words.status_title,
    };
    let builder =
        WebviewWindowBuilder::new(app, kind.label(), WebviewUrl::App("index.html".into()))
            .initialization_script(kind.script())
            .title(title)
            .inner_size(WIDTH, HEIGHT)
            .min_inner_size(WIDTH, HEIGHT)
            .center()
            .theme(Some(tauri::Theme::Dark))
            .background_color(tauri::window::Color(0x0b, 0x11, 0x1c, 0xff));
    // the Mac keeps its traffic lights over the page's title bar; Windows and Linux draw
    // none (Windows keeps the shadow, the 1 px edge and the rounded corners)
    #[cfg(target_os = "macos")]
    let builder = builder
        .title_bar_style(tauri::TitleBarStyle::Overlay)
        .hidden_title(true);
    #[cfg(not(target_os = "macos"))]
    let builder = builder.decorations(false).shadow(true);
    let window = builder.build()?;
    // the menu bar's items are in the title bar's menu of the main window, not here
    #[cfg(not(target_os = "macos"))]
    let _ = window.hide_menu();
    let w = window.clone();
    let handle = app.clone();
    window.on_window_event(move |event| match event {
        WindowEvent::CloseRequested { api, .. } if kind == Kind::Setup => {
            if handle.state::<KeepInTray>().0 {
                api.prevent_close();
                let _ = w.hide();
            } else {
                handle.exit(0);
            }
        }
        WindowEvent::Resized(_) => crate::frame::on_resized(&w),
        _ => {}
    });
    Ok(())
}

/// The setup opened a server: the main window, which loads it, shows, and the setup window
/// goes.
pub fn setup_done(app: &AppHandle) {
    app.state::<SetupApart>().set(false);
    let Some(setup) = app.get_webview_window(SETUP) else {
        return;
    };
    if let Some(main) = app.get_webview_window(webcache::MAIN) {
        let _ = main.show();
        let _ = main.unminimize();
        let _ = main.set_focus();
    }
    let _ = setup.destroy();
}

/// The server's status changed: an open status window shows it.
pub fn status_changed(app: &AppHandle) {
    if app.get_webview_window(STATUS).is_none() {
        return;
    }
    let view = crate::shell::status_window_view(app);
    let _ = app.emit_to(STATUS, STATUS_EVENT, &view);
}

/// The status window's page asks for its view.
#[tauri::command(async)]
pub fn status_window_state(window: WebviewWindow) -> Result<StatusWindowView, String> {
    if window.label() != STATUS {
        return Err("only the status window shows the status".into());
    }
    Ok(crate::shell::status_window_view(window.app_handle()))
}

/// A button of the status window (act).
#[tauri::command(async)]
pub fn status_window_action(window: WebviewWindow, action: String) -> Result<(), String> {
    if window.label() != STATUS {
        return Err("only the status window has its buttons".into());
    }
    let app = window.app_handle();
    match act(&action).ok_or_else(|| format!("no status window action {action}"))? {
        Act::Tray(id) => {
            crate::shell::act_as_tray(app, id);
            Ok(())
        }
        Act::Page(path) => crate::shell::open_local_page(app, path),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn each_window_has_its_label_and_its_page_s_mark() {
        for kind in [Kind::Setup, Kind::Status] {
            assert_eq!(Kind::of(kind.label()), Some(kind));
        }
        assert_eq!(Kind::of("main"), None);
        assert_eq!(
            Kind::Status.script(),
            r#"window.__MAKTABA_PANEL__ = "status";"#
        );
        assert_eq!(
            Kind::Setup.script(),
            r#"window.__MAKTABA_PANEL__ = "setup";"#
        );
        // the mockups' window, which is also its least
        assert_eq!((WIDTH, HEIGHT), (960.0, 640.0));
    }

    #[test]
    fn the_main_window_and_the_two_draw_maktaba_s_frame() {
        assert!(has_frame("main"));
        assert!(has_frame("setup"));
        assert!(has_frame("status"));
        // the tray's panel and the shell's dialog draw their own, or none
        assert!(!has_frame("tray-panel"));
        assert!(!has_frame("shell-dialog"));
    }

    #[test]
    fn the_main_window_shows_no_setup_while_its_own_window_has_it() {
        assert!(setup_elsewhere(true, "main"));
        assert!(!setup_elsewhere(true, "setup"));
        // its window could not be made: the main window shows it
        assert!(!setup_elsewhere(false, "main"));
        let apart = SetupApart::default();
        assert!(!apart.get());
        apart.set(true);
        assert!(apart.get());
    }

    #[test]
    fn the_status_window_s_buttons_are_the_tray_s_actions_and_two_pages() {
        assert_eq!(act("open"), Some(Act::Tray("show")));
        assert_eq!(act("pause"), Some(Act::Tray("server-pause")));
        assert_eq!(act("resume"), Some(Act::Tray("server-resume")));
        assert_eq!(act("logs"), Some(Act::Tray("open-logs")));
        assert_eq!(act("report"), Some(Act::Tray("report-problem")));
        assert_eq!(act("settings"), Some(Act::Tray("tray-settings")));
        assert_eq!(act("update"), Some(Act::Tray("update")));
        assert_eq!(act("libraries"), Some(Act::Page("/admin/libraries")));
        assert_eq!(act("devices"), Some(Act::Page("/account/devices")));
        // nothing else: not the tray's quit, not its stop
        assert_eq!(act("quit"), None);
        assert_eq!(act("stop-and-quit"), None);
        assert_eq!(act("server-pause"), None);
    }
}
