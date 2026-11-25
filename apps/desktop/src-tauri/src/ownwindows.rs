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
///
/// Once its window could not be made, the setup stays in the main window for this run: no
/// window is tried again, so the main window's setup is never reloaded under the user.
/// `told`: the main window's page was told the setup is elsewhere (it shows nothing), so it
/// must ask again before it is shown.
#[derive(Default)]
pub struct SetupApart {
    apart: AtomicBool,
    failed: AtomicBool,
    told: AtomicBool,
}

impl SetupApart {
    pub fn get(&self) -> bool {
        self.apart.load(Ordering::SeqCst)
    }
    /// The setup is to run in its own window, unless its window could not be made.
    fn claim(&self) {
        if !self.failed() {
            self.apart.store(true, Ordering::SeqCst);
        }
    }
    /// The setup is done, or back in the main window.
    fn release(&self) {
        self.apart.store(false, Ordering::SeqCst);
    }
    /// Its window could not be made: the main window has the setup from now on.
    fn fail(&self) {
        self.failed.store(true, Ordering::SeqCst);
        self.release();
    }
    pub fn failed(&self) -> bool {
        self.failed.load(Ordering::SeqCst)
    }
    /// The main window's page was told the setup is elsewhere.
    pub fn tell_main(&self) {
        self.told.store(true, Ordering::SeqCst);
    }
    /// Whether the main window's page must ask again what to show (once).
    pub fn take_told(&self) -> bool {
        self.told.swap(false, Ordering::SeqCst)
    }
}

/// The windows being made, one at a time per kind: a request while one is made (a second
/// launch, a tray click, a double click) makes no second window, and asks the one being made
/// to show once it is.
#[derive(Default)]
pub struct Pending {
    setup: Slot,
    status: Slot,
}

#[derive(Default)]
struct Slot {
    building: AtomicBool,
    show: AtomicBool,
}

impl Pending {
    fn slot(&self, kind: Kind) -> &Slot {
        match kind {
            Kind::Setup => &self.setup,
            Kind::Status => &self.status,
        }
    }
    /// A request for window `kind` (to show it, or only to make it): whether this request is
    /// the one to make it.
    fn begin(&self, kind: Kind, show: bool) -> bool {
        let s = self.slot(kind);
        if show {
            s.show.store(true, Ordering::SeqCst);
        }
        !s.building.swap(true, Ordering::SeqCst)
    }
    /// Made, or not: whether any request asked for it to show.
    fn end(&self, kind: Kind) -> bool {
        let s = self.slot(kind);
        s.building.store(false, Ordering::SeqCst);
        s.show.swap(false, Ordering::SeqCst)
    }
}

/// Whether closing a window keeps the app in its tray (lib.rs): not on Linux without a tray,
/// where closing the window quits.
pub struct KeepInTray(pub bool);

/// What closing a window does.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Closing {
    /// hides to the tray
    Hide,
    /// the app quits: there is no tray to come back from
    Quit,
    /// the window goes, the app stays
    Close,
}

/// What closing window `label` does. The main window's and the setup's is the app's: without a
/// tray the app quits with it, whatever other window is open. Left to the last window's close,
/// an open status window would keep an app no one can open again (no main window, no tray).
pub fn on_close(label: &str, keep_in_tray: bool) -> Closing {
    match label {
        webcache::MAIN | SETUP if keep_in_tray => Closing::Hide,
        webcache::MAIN | SETUP => Closing::Quit,
        _ => Closing::Close,
    }
}

/// The setup is to run in its own window: the main window's page shows nothing from now on
/// (unless that window could not be made this run).
pub fn claim_setup(app: &AppHandle) {
    app.state::<SetupApart>().claim();
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
    open(app, kind, true);
}

/// Makes the setup window without showing it (the app started in its tray at sign-in): its
/// page still adopts a server this machine already runs, as the main window's did.
pub fn prepare_setup(app: &AppHandle) {
    open(app, Kind::Setup, false);
}

fn focus(w: &WebviewWindow) {
    let _ = w.show();
    let _ = w.unminimize();
    let _ = w.set_focus();
}

fn open(app: &AppHandle, kind: Kind, show: bool) {
    if kind == Kind::Setup {
        if app.state::<SetupApart>().failed() {
            // its window could not be made this run: the main window has the setup, which is
            // shown as it is (never reloaded: that would lose the screens' progress)
            if show {
                if let Some(main) = app.get_webview_window(webcache::MAIN) {
                    focus(&main);
                }
            }
            return;
        }
        claim_setup(app);
    }
    if let Some(w) = app.get_webview_window(kind.label()) {
        if show {
            focus(&w);
        }
        return;
    }
    let pending = app.state::<Pending>();
    if !pending.begin(kind, show) {
        // being made: it shows once it is, when this asked for that
        return;
    }
    let app = app.clone();
    std::thread::spawn(move || {
        let built = build(&app, kind);
        let show = app.state::<Pending>().end(kind);
        match built {
            Ok(w) => {
                if show {
                    focus(&w);
                }
            }
            Err(e) => {
                crate::applog::warn(&format!("the {} window could not open", kind.label()), e);
                // a window of that name that exists after all (another request made it) is
                // the one; else the main window shows the setup screens, as before this story
                if kind == Kind::Setup && app.get_webview_window(SETUP).is_none() {
                    app.state::<SetupApart>().fail();
                    crate::shell::setup_in_main(&app, show);
                }
            }
        }
    });
}

/// Makes window `kind`, hidden (open shows it when asked).
fn build(app: &AppHandle, kind: Kind) -> tauri::Result<WebviewWindow> {
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
            .background_color(tauri::window::Color(0x0b, 0x11, 0x1c, 0xff))
            .visible(false);
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
        WindowEvent::CloseRequested { api, .. } => {
            match on_close(kind.label(), handle.state::<KeepInTray>().0) {
                Closing::Hide => {
                    api.prevent_close();
                    let _ = w.hide();
                }
                Closing::Quit => handle.exit(0),
                Closing::Close => {}
            }
        }
        WindowEvent::Resized(_) => crate::frame::on_resized(&w),
        _ => {}
    });
    Ok(window)
}

/// The setup opened a server: the main window loads it, and shows when the setup window was
/// on show (one made hidden at sign-in, which adopted this machine's server, leaves the app in
/// its tray); the setup window goes.
pub fn setup_done(app: &AppHandle) {
    let apart = app.state::<SetupApart>();
    apart.release();
    // the main window loads the server's page now: it asks nothing again
    apart.take_told();
    let Some(setup) = app.get_webview_window(SETUP) else {
        return;
    };
    if setup.is_visible().unwrap_or(true) {
        if let Some(main) = app.get_webview_window(webcache::MAIN) {
            focus(&main);
        }
        // Start in Big Screen, held back while the setup ran (lib.rs)
        if app.state::<crate::bigscreen::BigScreen>().start() {
            if let Err(e) = crate::bigscreen::set_full_screen(app, true) {
                crate::applog::warn("Start in Big Screen could not go full screen", e);
            }
        }
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

    /// Linux without a tray: the main window's close quits, an open status window or not.
    #[test]
    fn closing_the_app_s_window_hides_it_to_the_tray_or_quits() {
        for label in ["main", "setup"] {
            assert_eq!(on_close(label, true), Closing::Hide, "{label}");
            assert_eq!(on_close(label, false), Closing::Quit, "{label}");
        }
        for keep in [true, false] {
            assert_eq!(on_close("status", keep), Closing::Close);
        }
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
        apart.claim();
        assert!(apart.get());
        apart.release();
        assert!(!apart.get());
    }

    #[test]
    fn a_setup_window_that_could_not_be_made_is_not_tried_again_this_run() {
        let apart = SetupApart::default();
        apart.claim();
        apart.fail();
        assert!(apart.failed() && !apart.get());
        // a later Open Maktaba, or Connect to another Maktaba: the main window keeps it
        apart.claim();
        assert!(!apart.get());
    }

    #[test]
    fn a_main_window_told_the_setup_is_elsewhere_asks_again_once() {
        let apart = SetupApart::default();
        assert!(!apart.take_told());
        apart.tell_main();
        assert!(apart.take_told());
        assert!(!apart.take_told());
    }

    #[test]
    fn one_window_is_made_per_kind_however_many_ask() {
        let p = Pending::default();
        // the first request makes it; a second (a second launch, a double click) does not
        assert!(p.begin(Kind::Setup, false));
        assert!(!p.begin(Kind::Setup, true));
        // the other kind is made apart
        assert!(p.begin(Kind::Status, true));
        // made: shown, since a request asked for that while it was made
        assert!(p.end(Kind::Setup));
        assert!(p.end(Kind::Status));
        // made hidden (the sign-in start) and asked for by no one: it stays hidden
        assert!(p.begin(Kind::Setup, false));
        assert!(!p.end(Kind::Setup));
        // and the next request makes one again (the first failed, or was closed)
        assert!(p.begin(Kind::Setup, true));
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
