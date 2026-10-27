// Big-screen mode in the desktop shell (Epic 46).
//
// The web app's big-screen mode lives at /big. The shell adds:
//
// - View → Big Screen (`big-screen`, CmdOrCtrl+Shift+B): the window goes full
//   screen and the page opens /big. The page hears the click as every menu
//   item (the `menu` event); a page the shell cannot talk to (another
//   machine's server in client mode: no capability covers it) is loaded at its
//   /big instead (menu_target).
// - View → Start in Big Screen (`big-screen-start`, a check item): saved in
//   big-screen.json beside mode.json, in this machine's state folder (the app's
//   config folder on a Mac and Linux, %LOCALAPPDATA% on Windows, so a roaming
//   profile does not carry it to another PC). With it on, the window opens full
//   screen and the server's page loads at /big (entry_url).
// - Commands for the page (capabilities/default.json and local-server.json):
//   big_screen_state, set_big_screen (the window full screen or not, as the
//   page enters and leaves big screen) and set_big_screen_start. They touch the
//   window and that one file, nothing else.
use std::path::{Path, PathBuf};
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use tauri::{menu::CheckMenuItem, AppHandle, Manager, Url, Wry};

/// The settings file, beside mode.json.
pub const FILE: &str = "big-screen.json";
/// View → Big Screen.
pub const MENU_ID: &str = "big-screen";
/// View → Start in Big Screen.
pub const START_MENU_ID: &str = "big-screen-start";
/// Where the web app's big-screen mode lives.
pub const PATH: &str = "/big";
/// The menu item's accelerator.
pub const ACCELERATOR: &str = "CmdOrCtrl+Shift+B";

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Prefs {
    /// Open the app full screen in big screen.
    #[serde(default)]
    pub start: bool,
}

/// The saved settings; the defaults when there are none or they cannot be read.
pub fn load(path: &Path) -> Prefs {
    std::fs::read(path)
        .ok()
        .and_then(|raw| serde_json::from_slice(&raw).ok())
        .unwrap_or_default()
}

/// Saves the settings (whole: written beside, then moved over the old file).
pub fn save(path: &Path, prefs: &Prefs) -> std::io::Result<()> {
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir)?;
    }
    let tmp = path.with_extension("json.tmp");
    std::fs::write(
        &tmp,
        serde_json::to_vec_pretty(prefs).map_err(std::io::Error::other)?,
    )?;
    std::fs::rename(tmp, path)
}

/// The address a start loads: a server's page opens at /big when `start` is on.
/// Only the page's root is moved (an address that names a page keeps it), and
/// anything that is not an http(s) address is left as it is.
pub fn entry_url(url: &str, start: bool) -> String {
    if !start {
        return url.to_string();
    }
    let Ok(mut u) = Url::parse(url) else {
        return url.to_string();
    };
    if !matches!(u.scheme(), "http" | "https") || !(u.path().is_empty() || u.path() == "/") {
        return url.to_string();
    }
    u.set_path(PATH);
    u.to_string()
}

/// Whether the page at `url` can hear the shell: the bundled pages and this
/// machine's own server (capabilities/local-server.json) can; another machine's
/// cannot.
pub fn page_hears_shell(url: &Url) -> bool {
    match url.scheme() {
        "tauri" => true,
        "http" | "https" => matches!(
            url.host_str(),
            Some("127.0.0.1" | "localhost" | "[::1]" | "::1" | "tauri.localhost")
        ),
        _ => false,
    }
}

/// View → Big Screen on a page that cannot hear the shell: its server's /big to
/// load. None when the page hears the menu event and opens /big itself.
pub fn menu_target(current: &Url) -> Option<Url> {
    if page_hears_shell(current) || !matches!(current.scheme(), "http" | "https") {
        return None;
    }
    if current.path() == PATH || current.path().starts_with(&format!("{PATH}/")) {
        return None;
    }
    let mut u = current.clone();
    u.set_path(PATH);
    u.set_query(None);
    u.set_fragment(None);
    Some(u)
}

/// The shell's big-screen setting (the settings file). Pure file state: the
/// unit tests build it on every OS, so the menu's check item lives apart
/// (StartCheck), out of what they link (a menu item pulls in Windows menu
/// entry points a test binary cannot load).
pub struct BigScreen {
    path: PathBuf,
    prefs: Mutex<Prefs>,
}

impl BigScreen {
    pub fn new(dir: &Path) -> Self {
        let path = dir.join(FILE);
        Self {
            prefs: Mutex::new(load(&path)),
            path,
        }
    }

    pub fn start(&self) -> bool {
        self.prefs.lock().unwrap().start
    }

    /// Saves "Start in big screen".
    pub fn set_start(&self, on: bool) -> Result<(), String> {
        let mut prefs = self.prefs.lock().unwrap();
        let next = Prefs { start: on };
        save(&self.path, &next).map_err(|e| format!("{}: {e}", self.path.display()))?;
        *prefs = next;
        Ok(())
    }
}

/// View → Start in Big Screen, the menu's check item (lib.rs manages it).
pub struct StartCheck(pub CheckMenuItem<Wry>);

fn state(app: &AppHandle) -> tauri::State<'_, BigScreen> {
    app.state::<BigScreen>()
}

/// Saves the setting and keeps the menu's check mark in step.
fn set_start(app: &AppHandle, on: bool) -> Result<(), String> {
    state(app).set_start(on)?;
    if let Some(check) = app.try_state::<StartCheck>() {
        let _ = check.0.set_checked(on);
    }
    Ok(())
}

/// The main window full screen, or not.
pub fn set_full_screen(app: &AppHandle, on: bool) -> Result<(), String> {
    let w = app
        .get_webview_window(crate::webcache::MAIN)
        .ok_or("no main window")?;
    w.set_fullscreen(on).map_err(|e| e.to_string())
}

/// View → Big Screen: full screen, and the page opens /big (the `menu` event,
/// sent by lib.rs as for every item; a page that cannot hear it is loaded at /big).
pub fn on_menu(app: &AppHandle) {
    let _ = set_full_screen(app, true);
    let Some(w) = app.get_webview_window(crate::webcache::MAIN) else {
        return;
    };
    crate::shell::focus_main(app);
    if let Some(url) = w.url().ok().as_ref().and_then(menu_target) {
        let _ = w.navigate(url);
    }
}

/// View → Start in Big Screen was clicked: the item has toggled its own mark.
pub fn on_start_menu(app: &AppHandle) {
    let on = app
        .try_state::<StartCheck>()
        .and_then(|c| c.0.is_checked().ok())
        .unwrap_or(!state(app).start());
    if let Err(e) = set_start(app, on) {
        crate::attempts::log(&format!("big screen: the setting was not saved: {e}"));
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct State {
    pub start: bool,
    pub fullscreen: bool,
}

#[tauri::command]
pub fn big_screen_state(app: AppHandle) -> State {
    let fullscreen = app
        .get_webview_window(crate::webcache::MAIN)
        .and_then(|w| w.is_fullscreen().ok())
        .unwrap_or(false);
    State {
        start: state(&app).start(),
        fullscreen,
    }
}

#[tauri::command]
pub fn set_big_screen(app: AppHandle, on: bool) -> Result<(), String> {
    set_full_screen(&app, on)
}

#[tauri::command]
pub fn set_big_screen_start(app: AppHandle, on: bool) -> Result<(), String> {
    set_start(&app, on)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_setting_round_trips_and_defaults_off() {
        let tmp = tempfile::tempdir().unwrap();
        let p = tmp.path().join("cfg").join(FILE);
        assert_eq!(load(&p), Prefs { start: false });
        save(&p, &Prefs { start: true }).unwrap();
        assert_eq!(load(&p), Prefs { start: true });
        assert!(!p.with_extension("json.tmp").exists());
        // an older or a broken file is the default, never an error
        std::fs::write(&p, "{}").unwrap();
        assert_eq!(load(&p), Prefs { start: false });
        std::fs::write(&p, "garbage").unwrap();
        assert_eq!(load(&p), Prefs { start: false });
        // keys it does not know are ignored
        std::fs::write(&p, r#"{"start":true,"later":1}"#).unwrap();
        assert_eq!(load(&p), Prefs { start: true });
    }

    #[test]
    fn the_shell_state_saves_on_this_machine() {
        let tmp = tempfile::tempdir().unwrap();
        let bs = BigScreen::new(tmp.path());
        assert!(!bs.start());
        bs.set_start(true).unwrap();
        assert!(bs.start());
        // a new start of the app reads it back
        assert!(BigScreen::new(tmp.path()).start());
        bs.set_start(false).unwrap();
        assert!(!BigScreen::new(tmp.path()).start());
        assert!(tmp.path().join(FILE).exists());
    }

    #[test]
    fn a_start_in_big_screen_opens_the_servers_page_at_big() {
        assert_eq!(
            entry_url("http://127.0.0.1:8088", true),
            "http://127.0.0.1:8088/big"
        );
        assert_eq!(
            entry_url("https://maktaba.home/", true),
            "https://maktaba.home/big"
        );
        // off: unchanged
        assert_eq!(
            entry_url("http://127.0.0.1:8088", false),
            "http://127.0.0.1:8088"
        );
        // a page of its own (the logs) keeps its path
        assert_eq!(
            entry_url("http://127.0.0.1:8088/admin/logs?at=1", true),
            "http://127.0.0.1:8088/admin/logs?at=1"
        );
        // not an http(s) address: left alone
        assert_eq!(entry_url("tauri://localhost/", true), "tauri://localhost/");
        assert_eq!(entry_url("not a url", true), "not a url");
    }

    #[test]
    fn the_menu_loads_big_only_on_a_page_that_cannot_hear_it() {
        let u = |s: &str| Url::parse(s).unwrap();
        // the bundled pages and this machine's server hear the menu event
        assert_eq!(menu_target(&u("tauri://localhost/")), None);
        assert_eq!(menu_target(&u("http://tauri.localhost/")), None);
        assert_eq!(menu_target(&u("http://127.0.0.1:8088/library")), None);
        assert_eq!(menu_target(&u("http://localhost:5173/")), None);
        assert_eq!(menu_target(&u("http://[::1]:8088/")), None);
        // another machine's page is loaded at its /big
        assert_eq!(
            menu_target(&u("https://maktaba.home/videos/1?t=3#x")),
            Some(u("https://maktaba.home/big"))
        );
        assert_eq!(
            menu_target(&u("http://10.0.0.5:8088/")),
            Some(u("http://10.0.0.5:8088/big"))
        );
        // already there
        assert_eq!(menu_target(&u("https://maktaba.home/big/search")), None);
        assert_eq!(menu_target(&u("about:blank")), None);
    }
}
