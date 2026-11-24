// Maktaba's own dialog for the shell's errors (Epic 13 story 13.11; the design:
// docs/design/desktop/mockups/dialog.html), in place of the system's message box.
//
// An error the shell reports (the server could not start, an update failed, a page could not
// open) opens a small window of the app's own bundled pages, marked before it loads
// (window.__MAKTABA_PANEL__ = 'dialog', with the words in window.__MAKTABA_DIALOG__): the
// web app's ShellDialog. It shows the sentence, the programs' own errors as they are, a way to
// the logs when this machine runs the server, and Close (Enter); Escape closes it. A second
// error while it is open replaces its words. The system's message box stays only for when the
// window cannot be made (no WebView: WebView2 missing on Windows), so an error is never lost.
//
// The window is built off the main thread: shell.rs reports errors from its event handlers,
// where building a window can deadlock on Windows.

use serde::Serialize;
use tauri::{AppHandle, Emitter, Manager, WebviewUrl, WebviewWindowBuilder};

use crate::lang::Lang;

pub const LABEL: &str = "shell-dialog";
/// The event an open dialog gets with new words.
pub const EVENT: &str = "shell-dialog";
pub const WIDTH: f64 = 560.0;
pub const HEIGHT: f64 = 300.0;

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
        }
    }
}

/// Opens the dialog with `content` (or gives an open one these words); `fallback` runs when
/// the window cannot be made.
pub fn show(app: &AppHandle, content: DialogContent, fallback: impl FnOnce() + Send + 'static) {
    if let Some(w) = app.get_webview_window(LABEL) {
        let _ = w.emit(EVENT, &content);
        let _ = w.show();
        let _ = w.set_focus();
        return;
    }
    let app = app.clone();
    std::thread::spawn(move || {
        let script = match init_script(&content) {
            Some(s) => s,
            None => return fallback(),
        };
        let built = WebviewWindowBuilder::new(&app, LABEL, WebviewUrl::App("index.html".into()))
            .initialization_script(&script)
            .title(content.brand)
            .inner_size(WIDTH, HEIGHT)
            .resizable(false)
            .maximizable(false)
            .minimizable(false)
            .decorations(false)
            .shadow(true)
            .always_on_top(true)
            .center()
            .focused(true)
            .background_color(tauri::window::Color(0x18, 0x24, 0x3a, 0xff))
            .build();
        if let Err(e) = built {
            crate::applog::warn("Maktaba's dialog could not open: the system's says it", e);
            fallback();
        }
    });
}

/// The script that marks the dialog's page and gives it its words, before the page loads.
pub fn init_script(content: &DialogContent) -> Option<String> {
    // JSON is a JavaScript expression; `</` and the line separators are escaped so the words
    // cannot end the script or break its line
    let json = serde_json::to_string(content)
        .ok()?
        .replace("</", "<\\/")
        .replace('\u{2028}', "\\u2028")
        .replace('\u{2029}', "\\u2029");
    Some(format!(
        "window.__MAKTABA_PANEL__ = 'dialog'; window.__MAKTABA_DIALOG__ = {json};"
    ))
}

/// A button of the dialog: "close", or "logs" (the tray's Open logs, then close).
#[tauri::command]
pub fn shell_dialog_choose(app: AppHandle, action: String) -> Result<(), String> {
    match action.as_str() {
        "close" => {}
        "logs" => {
            if !crate::traypanel::choosable(&crate::shell::tray_entries(&app), "open-logs") {
                return Err("no logs to open here".into());
            }
            crate::shell::choose_tray_item(&app, "open-logs");
        }
        other => return Err(format!("no dialog action {other}")),
    }
    if let Some(w) = app.get_webview_window(LABEL) {
        let _ = w.close();
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

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
    fn the_words_cannot_end_the_script_that_carries_them() {
        let c = DialogContent::new(
            Lang::En,
            "</script><script>alert(1)</script>",
            &["a\u{2028}b"],
            false,
        );
        let s = init_script(&c).unwrap();
        assert!(!s.contains("</script>"), "{s}");
        assert!(!s.contains('\u{2028}'));
        assert!(s.starts_with("window.__MAKTABA_PANEL__ = 'dialog';"));
        // still the same words, as JSON reads them
        let json = s.split(" = ").nth(2).unwrap().trim_end_matches(';');
        let back: serde_json::Value = serde_json::from_str(json).unwrap();
        assert_eq!(back["title"], "</script><script>alert(1)</script>");
    }
}
