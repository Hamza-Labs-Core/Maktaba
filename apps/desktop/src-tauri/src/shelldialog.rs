// Maktaba's own dialog for the shell's errors (Epic 13 story 13.11; the design:
// docs/design/desktop/mockups/dialog.html), in place of the system's message box.
//
// An error the shell reports (the server could not start, an update failed, a page could not
// open) opens a small window of the app's own bundled pages, marked before it loads
// (window.__MAKTABA_PANEL__ = 'dialog'): the web app's ShellDialog. The window is made hidden;
// its page asks for the newest error's words (shell_dialog_content), which shows it with the
// focus. It shows the sentence, the programs' own errors as they are, a way to the logs when
// this machine runs the server, and Close (Enter); Escape closes it. Errors that come while it
// is being made or shown go to the same window, the newest words winning.
//
// The system's message box says an error only when the dialog cannot: its window could not be
// made (no WebView: WebView2 missing on Windows), or its page never asked for its words within
// READY_WAIT (it failed to load), when the window is closed. So no error is lost, and none is
// shown twice.
//
// The window is built off the main thread: shell.rs reports errors from its event handlers,
// where building a window can deadlock on Windows.

use std::sync::Mutex;
use std::time::Duration;

use serde::Serialize;
use tauri::{AppHandle, Emitter, Manager, WebviewUrl, WebviewWindowBuilder, WindowEvent};

use crate::lang::Lang;

pub const LABEL: &str = "shell-dialog";
/// The event a shown dialog gets when there are new words: none in it (an event of any window
/// reaches every page that listens for all of them), the page asks for them.
pub const EVENT: &str = "shell-dialog";
pub const WIDTH: f64 = 560.0;
pub const HEIGHT: f64 = 300.0;
/// How long a new dialog's page has to ask for its words (and so show it).
const READY_WAIT: Duration = Duration::from_secs(5);

/// What the dialog says.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DialogContent {
    /// "en" or "ar": the shell's language (the system's)
    pub lang: &'static str,
    pub brand: &'static str,
    /// the sentence: what happened
    pub title: String,
    /// the programs' own errors, as they are
    pub errors: Vec<String>,
    /// whether "See the logs" is offered (the tray's Open logs, when this machine has a server)
    pub logs: bool,
    pub close: &'static str,
    pub see_logs: &'static str,
    /// counts the errors: a new one is a new dialog, for a screen reader
    pub revision: u64,
}

impl DialogContent {
    pub fn new(lang: Lang, sentence: &str, errors: &[&str], logs: bool) -> Self {
        let w = lang.dialog();
        DialogContent {
            lang: match lang {
                Lang::En => "en",
                Lang::Ar => "ar",
            },
            brand: lang.words().brand,
            title: sentence.trim().to_string(),
            errors: errors
                .iter()
                .map(|e| e.trim().to_string())
                .filter(|e| !e.is_empty())
                .collect(),
            logs,
            close: w.close,
            see_logs: w.see_logs,
            revision: 0,
        }
    }
}

/// The dialog's window: none, being made (its page has not asked for its words), or shown.
#[derive(Debug, Default, Clone, Copy, PartialEq)]
enum Phase {
    #[default]
    None,
    Building,
    Shown,
}

/// What `show` does with a new error.
#[derive(Debug, PartialEq)]
pub enum Step {
    /// make the window: the number of this one
    Build(u64),
    /// the window being made asks for the newest words itself
    Wait,
    /// the dialog is shown: it asks for the new words
    Update,
}

/// The dialog's state: its phase and the newest error (its words, and what the system's box
/// would say).
#[derive(Default)]
pub struct Dialog {
    inner: Mutex<Inner>,
}

#[derive(Default)]
struct Inner {
    phase: Phase,
    content: Option<DialogContent>,
    message: String,
    revision: u64,
    /// counts the windows made: what a window says of its end is that window's only
    build: u64,
}

impl Dialog {
    fn lock(&self) -> std::sync::MutexGuard<'_, Inner> {
        self.inner.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// A new error, kept as the newest: what to do with it.
    fn report(&self, mut content: DialogContent, message: String) -> (Step, DialogContent) {
        let mut i = self.lock();
        i.revision += 1;
        content.revision = i.revision;
        i.content = Some(content.clone());
        i.message = message;
        let step = match i.phase {
            Phase::None => {
                i.phase = Phase::Building;
                i.build += 1;
                Step::Build(i.build)
            }
            Phase::Building => Step::Wait,
            Phase::Shown => Step::Update,
        };
        (step, content)
    }

    /// The page asks for its words: the dialog is shown from now on.
    fn words(&self) -> Option<DialogContent> {
        let mut i = self.lock();
        if i.phase == Phase::Building {
            i.phase = Phase::Shown;
        }
        i.content.clone()
    }

    /// Window `build` is gone (closed, or never made): the next error makes a new one. Returns
    /// what the system's box must say when the page never showed the error. Nothing when a newer
    /// one was made since: an old window's late end is no end of the newer error's.
    fn gone(&self, build: u64) -> Option<String> {
        let mut i = self.lock();
        if i.build != build {
            return None;
        }
        let unshown = i.phase == Phase::Building;
        i.phase = Phase::None;
        i.content = None;
        let message = std::mem::take(&mut i.message);
        unshown.then_some(message).filter(|m| !m.is_empty())
    }

    /// The window now is closed by its own button.
    fn closed(&self) {
        let build = self.lock().build;
        self.gone(build);
    }

    /// Whether window `build` still waits for its page to ask for its words.
    fn waits(&self, build: u64) -> bool {
        let i = self.lock();
        i.build == build && i.phase == Phase::Building
    }
}

/// Opens the dialog with `content`, or gives the shown one these words. `message` is what the
/// system's box (`fallback`) says when the dialog cannot be made or its page never shows it.
pub fn show(
    app: &AppHandle,
    content: DialogContent,
    message: String,
    fallback: fn(&AppHandle, String),
) {
    let (step, content) = app.state::<Dialog>().report(content, message);
    match step {
        Step::Wait => {}
        Step::Update => {
            let _ = app.emit_to(LABEL, EVENT, ());
            if let Some(w) = app.get_webview_window(LABEL) {
                let _ = w.show();
                let _ = w.set_focus();
            }
        }
        Step::Build(n) => {
            let app = app.clone();
            std::thread::spawn(move || build(&app, n, content.brand, fallback));
        }
    }
}

/// Makes the dialog's window `n`, hidden; gives the error to the system's box if it cannot be made
/// or its page never asks for its words.
fn build(app: &AppHandle, n: u64, title: &str, fallback: fn(&AppHandle, String)) {
    let built = WebviewWindowBuilder::new(app, LABEL, WebviewUrl::App("index.html".into()))
        .initialization_script("window.__MAKTABA_PANEL__ = 'dialog';")
        .title(title)
        .inner_size(WIDTH, HEIGHT)
        .resizable(false)
        .maximizable(false)
        .minimizable(false)
        .decorations(false)
        .shadow(true)
        .always_on_top(true)
        .visible_on_all_workspaces(true)
        .center()
        .visible(false)
        .background_color(tauri::window::Color(0x18, 0x24, 0x3a, 0xff))
        .build();
    let window = match built {
        Ok(w) => w,
        Err(e) => {
            crate::applog::warn("Maktaba's dialog could not open: the system's says it", e);
            if let Some(m) = app.state::<Dialog>().gone(n) {
                fallback(app, m);
            }
            return;
        }
    };
    // a window is made with the app's menu bar, which Linux draws inside it: the dialog has none
    #[cfg(not(target_os = "macos"))]
    let _ = window.remove_menu();
    let handle = app.clone();
    window.on_window_event(move |event| {
        if let WindowEvent::Destroyed = event {
            handle.state::<Dialog>().gone(n);
        }
    });
    // a page that never asks for its words failed to load: the system's box says them
    std::thread::sleep(READY_WAIT);
    let state = app.state::<Dialog>();
    if !state.waits(n) {
        return;
    }
    let message = state.gone(n);
    if let Some(w) = app.get_webview_window(LABEL) {
        let _ = w.destroy();
    }
    if let Some(m) = message {
        fallback(app, m);
    }
}

/// The page asks for its words: the newest error's. The dialog shows, with the focus.
#[tauri::command]
pub fn shell_dialog_content(app: AppHandle) -> Option<DialogContent> {
    // no error: nothing to show
    let content = app.state::<Dialog>().words()?;
    if let Some(w) = app.get_webview_window(LABEL) {
        let _ = w.show();
        let _ = w.set_focus();
    }
    Some(content)
}

/// A button of the dialog: "close", or "logs" (the dialog closes, then the tray's Open logs).
#[tauri::command]
pub fn shell_dialog_choose(app: AppHandle, action: String) -> Result<(), String> {
    let logs = match action.as_str() {
        "close" => false,
        "logs" => {
            if !crate::traypanel::choosable(&crate::shell::tray_entries(&app), "open-logs") {
                return Err("no logs to open here".into());
            }
            true
        }
        other => return Err(format!("no dialog action {other}")),
    };
    // closed first: an error opening the logs gets a dialog of its own, not this one closing
    app.state::<Dialog>().closed();
    if let Some(w) = app.get_webview_window(LABEL) {
        let _ = w.destroy();
    }
    if logs {
        crate::shell::choose_tray_item(&app, "open-logs");
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn words() -> DialogContent {
        DialogContent::new(Lang::En, "The update could not be installed.", &[], true)
    }

    #[test]
    fn the_words_are_the_sentence_and_the_programs_errors_as_they_are() {
        let c = DialogContent::new(
            Lang::En,
            " The Maktaba server could not start. ",
            &["maktaba-api: bind: address already in use", "  "],
            true,
        );
        assert_eq!(c.title, "The Maktaba server could not start.");
        assert_eq!(c.errors, vec!["maktaba-api: bind: address already in use"]);
        assert_eq!(c.close, "Close");
        let ar = DialogContent::new(Lang::Ar, "تعذّر تشغيل خادم مكتبة.", &[], false);
        assert_eq!((ar.lang, ar.close, ar.brand), ("ar", "إغلاق", "مكتبة"));
    }

    #[test]
    fn one_window_for_errors_that_come_together_with_the_newest_words() {
        let d = Dialog::default();
        let (step, first) = d.report(words(), "first".into());
        assert_eq!(step, Step::Build(1));
        // a second error while the window is being made: no second window
        let (step, second) = d.report(
            DialogContent::new(Lang::En, "The server could not be paused.", &[], true),
            "second".into(),
        );
        assert_eq!(step, Step::Wait);
        assert!(
            second.revision > first.revision,
            "a new error is a new dialog"
        );
        // the page asks: the newest words, and the dialog is shown
        assert_eq!(d.words().unwrap().title, "The server could not be paused.");
        assert!(!d.waits(1));
        assert_eq!(d.report(words(), "third".into()).0, Step::Update);
        // closed: nothing for the system's box, and the next error makes a new window
        assert_eq!(d.gone(1), None);
        assert_eq!(d.report(words(), "fourth".into()).0, Step::Build(2));
    }

    /// See the logs closes the window, and opening them fails at once: the newer error's window
    /// is made before the old one says it is gone, and before the old one's wait for its page
    /// is over.
    #[test]
    fn an_old_window_s_late_end_loses_no_newer_error() {
        let d = Dialog::default();
        d.report(words(), "first".into());
        d.words();
        d.closed();
        let (step, _) = d.report(
            DialogContent::new(Lang::En, "The logs could not be opened.", &[], false),
            "the logs".into(),
        );
        assert_eq!(step, Step::Build(2));
        // the first window's Destroyed, and the end of its wait
        assert_eq!(d.gone(1), None);
        assert!(!d.waits(1), "the first window's wait ends nothing");
        assert!(d.waits(2));
        assert_eq!(d.words().unwrap().title, "The logs could not be opened.");
        // its own end is its own
        d.report(words(), "third".into());
        assert_eq!(d.gone(2), None, "it was shown");
        assert_eq!(d.words(), None);
    }

    #[test]
    fn a_page_that_never_shows_its_words_gives_them_to_the_system_s_box_once() {
        let d = Dialog::default();
        d.report(words(), "The update could not be installed.".into());
        assert!(d.waits(1));
        assert_eq!(
            d.gone(1).as_deref(),
            Some("The update could not be installed.")
        );
        assert_eq!(d.gone(1), None);
    }
}
