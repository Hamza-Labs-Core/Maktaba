// The Mac app around the server (Epic 36, stories 36.5 / 36.6): the
// menu-bar item, the status poller, and the commands the setup screens
// (web/src/pages/Setup/) call. Pure decisions live in status.rs,
// tray.rs, mode.rs and setup.rs; this file is the glue to tauri.
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use serde::Serialize;
use serde_json::Value;
use tauri::{
    ipc::Channel,
    menu::{Menu, MenuItem, PredefinedMenuItem},
    tray::TrayIconBuilder,
    AppHandle, Emitter, Manager, Wry,
};
use tauri_plugin_dialog::{DialogExt, MessageDialogKind};
use tauri_plugin_updater::UpdaterExt;

use crate::agent::{self, Action, BundlePaths, LoginItem};
use crate::mode::{self, AppMode, Target};
use crate::setup::{self, Progress, StepError};
use crate::status::{self, ServerStatus};
use crate::tray::{self, Entry};

const TRAY_ID: &str = "main";
const POLL_EVERY: Duration = Duration::from_secs(3);
/// How long "unreachable" reads as starting after a start or resume.
const START_GRACE: Duration = Duration::from_secs(90);
const UPDATE_CHECK_EVERY: Duration = Duration::from_secs(6 * 60 * 60);

pub struct Shell {
    mode_path: PathBuf,
    mode: Mutex<Option<AppMode>>,
    /// The bundled maktaba-server, when this build carries the server.
    server_bin: Option<PathBuf>,
    paused: AtomicBool,
    started_at: Mutex<Instant>,
    update_ready: AtomicBool,
    status: Mutex<ServerStatus>,
    menu: Mutex<Vec<Entry>>,
    /// "Connect to another Maktaba…" was chosen: show setup until a mode is saved.
    force_setup: AtomicBool,
    /// One refresh at a time, so tray menus are queued in the order computed.
    refresh_lock: Mutex<()>,
}

impl Shell {
    pub fn new(app: &AppHandle) -> Self {
        let mode_path = app
            .path()
            .app_config_dir()
            .unwrap_or_else(|_| PathBuf::from("."))
            .join("mode.json");
        // MAKTABA_SERVER_BIN points a dev build (`tauri dev`, outside
        // Maktaba.app) at a maktaba-server to run setup against.
        let server_bin = std::env::var_os("MAKTABA_SERVER_BIN")
            .map(PathBuf::from)
            .or_else(|| {
                let exe = std::env::current_exe().ok()?;
                let paths = BundlePaths::from_exe(&exe)?;
                paths.payload_present().then(|| paths.server_bin())
            });
        Self {
            mode: Mutex::new(mode::load(&mode_path)),
            mode_path,
            server_bin,
            paused: AtomicBool::new(false),
            started_at: Mutex::new(Instant::now()),
            update_ready: AtomicBool::new(false),
            status: Mutex::new(ServerStatus::Starting),
            menu: Mutex::new(Vec::new()),
            force_setup: AtomicBool::new(false),
            refresh_lock: Mutex::new(()),
        }
    }

    fn mode(&self) -> Option<AppMode> {
        self.mode.lock().unwrap().clone()
    }

    fn set_mode(&self, m: AppMode) -> Result<(), String> {
        mode::save(&self.mode_path, &m)
            .map_err(|e| format!("cannot save {}: {e}", self.mode_path.display()))?;
        *self.mode.lock().unwrap() = Some(m);
        self.force_setup.store(false, Ordering::SeqCst);
        Ok(())
    }

    /// Forget client mode and go back to the setup screens.
    fn clear_mode(&self) -> Result<(), String> {
        match std::fs::remove_file(&self.mode_path) {
            Ok(()) => {}
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => return Err(format!("cannot remove {}: {e}", self.mode_path.display())),
        }
        *self.mode.lock().unwrap() = None;
        self.force_setup.store(true, Ordering::SeqCst);
        Ok(())
    }

    /// The menu bar shows server items: this Mac runs (or is setting up) the server.
    fn server_side(&self) -> bool {
        match self.mode() {
            Some(AppMode::Server { .. }) => true,
            Some(AppMode::Client { .. }) => false,
            None => self.server_bin.is_some(),
        }
    }

    fn target(&self) -> Target {
        if self.force_setup.load(Ordering::SeqCst) {
            return Target::Setup;
        }
        mode::start_target(self.mode().as_ref(), self.server_bin.is_some())
    }

    fn mark_started(&self) {
        self.paused.store(false, Ordering::SeqCst);
        *self.started_at.lock().unwrap() = Instant::now();
    }

    fn compute_status(&self) -> ServerStatus {
        let Some(AppMode::Server { supervisor_url, .. }) = self.mode() else {
            return ServerStatus::SetupNeeded;
        };
        let addr = status::addr_of(&supervisor_url)
            .unwrap_or_else(|| status::DEFAULT_SUPERVISOR_ADDR.into());
        let just_started = self.started_at.lock().unwrap().elapsed() < START_GRACE;
        status::derive(
            true,
            self.paused.load(Ordering::SeqCst),
            just_started,
            &status::probe(&addr),
        )
    }
}

fn shell(app: &AppHandle) -> tauri::State<'_, Shell> {
    app.state::<Shell>()
}

pub fn focus_main(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
    }
}

fn navigate_main(app: &AppHandle, url: &str) -> Result<(), String> {
    let w = app.get_webview_window("main").ok_or("no main window")?;
    let url: tauri::Url = url.parse().map_err(|e| format!("bad address {url}: {e}"))?;
    w.navigate(url).map_err(|e| e.to_string())
}

/// The bundled web app's own address (where DesktopGate runs).
fn bundled_url(app: &AppHandle) -> String {
    #[cfg(dev)]
    if let Some(u) = &app.config().build.dev_url {
        return u.to_string();
    }
    let _ = app;
    if cfg!(windows) {
        "http://tauri.localhost/".into()
    } else {
        "tauri://localhost/".into()
    }
}

fn show_error(app: &AppHandle, msg: impl Into<String>) {
    app.dialog()
        .message(msg)
        .title("Maktaba")
        .kind(MessageDialogKind::Error)
        .show(|_| {});
}

fn build_tray_menu(app: &AppHandle, entries: &[Entry]) -> tauri::Result<Menu<Wry>> {
    let menu = Menu::new(app)?;
    for e in entries {
        match e {
            Entry::Item { id, label, enabled } => {
                menu.append(&MenuItem::with_id(app, *id, label, *enabled, None::<&str>)?)?
            }
            Entry::Separator => menu.append(&PredefinedMenuItem::separator(app)?)?,
        }
    }
    Ok(menu)
}

/// Recompute the status; rebuild the tray menu only when it changed.
fn refresh(app: &AppHandle) {
    let sh = shell(app);
    // Held until the menu update is queued: two refreshes (the poller and
    // a tray click) must reach the main thread in the order they computed.
    let _one_at_a_time = sh.refresh_lock.lock().unwrap();
    let server = sh.server_side();
    let status = if server {
        sh.compute_status()
    } else {
        ServerStatus::SetupNeeded
    };
    let entries = tray::entries(server, &status, sh.update_ready.load(Ordering::SeqCst));
    *sh.status.lock().unwrap() = status.clone();
    let mut last = sh.menu.lock().unwrap();
    if *last == entries {
        return;
    }
    *last = entries.clone();
    drop(last);
    let tooltip = if server {
        tray::status_line(&status)
    } else {
        "Maktaba".into()
    };
    let handle = app.clone();
    let _ = app.run_on_main_thread(move || {
        if let (Some(t), Ok(menu)) = (
            handle.tray_by_id(TRAY_ID),
            build_tray_menu(&handle, &entries),
        ) {
            let _ = t.set_menu(Some(menu));
            let _ = t.set_tooltip(Some(tooltip));
        }
    });
}

/// Tray clicks arrive on the main thread; the work (launchctl,
/// SMAppService, a status probe) runs on its own thread so the UI never
/// waits on it.
fn on_tray_event(app: &AppHandle, id: &str) {
    let (app, id) = (app.clone(), id.to_string());
    std::thread::spawn(move || handle_tray_event(&app, &id));
}

fn handle_tray_event(app: &AppHandle, id: &str) {
    let sh = shell(app);
    let result = match id {
        "show" => {
            focus_main(app);
            Ok(())
        }
        // Transport controls and Settings go to the web shell through the
        // same "menu" channel as the menu bar (web/src/lib/desktop.ts).
        "media-play" | "media-pause" => app.emit("menu", id.to_string()).map_err(|e| e.to_string()),
        "tray-settings" => {
            focus_main(app);
            app.emit("menu", "preferences").map_err(|e| e.to_string())
        }
        "server-pause" => {
            sh.paused.store(true, Ordering::SeqCst);
            agent::control(Action::Pause)
        }
        "server-resume" => {
            sh.mark_started();
            // unregistered (or turned off) login item: registering starts it
            if agent::login_item() == LoginItem::Enabled {
                agent::control(Action::Resume)
            } else {
                agent::register()
            }
        }
        "open-logs" => agent::open_logs(),
        "change-server" => sh.clear_mode().and_then(|()| {
            focus_main(app);
            navigate_main(app, &bundled_url(app))
        }),
        "update" => {
            install_update(app.clone());
            Ok(())
        }
        "quit" => {
            app.exit(0);
            Ok(())
        }
        "stop-and-quit" => {
            let r = agent::control(Action::Pause);
            if r.is_ok() {
                app.exit(0);
            }
            r
        }
        _ => Ok(()),
    };
    if let Err(e) = result {
        show_error(app, e);
    }
    refresh(app);
}

/// Story 36.8 replaces the whole app (shell + server payload): install,
/// restart the server so it runs the new payload, then relaunch.
fn install_update(app: AppHandle) {
    tauri::async_runtime::spawn(async move {
        let run = async {
            let update = app
                .updater()
                .map_err(|e| e.to_string())?
                .check()
                .await
                .map_err(|e| e.to_string())?;
            let Some(update) = update else {
                return Ok(false);
            };
            update
                .download_and_install(|_, _| {}, || {})
                .await
                .map_err(|e| e.to_string())?;
            Ok::<bool, String>(true)
        };
        match run.await {
            Ok(true) => {
                let sh = shell(&app);
                // a paused server stays paused; it runs the new payload on Resume
                if matches!(sh.mode(), Some(AppMode::Server { .. }))
                    && !sh.paused.load(Ordering::SeqCst)
                {
                    let _ = agent::control(Action::Restart);
                }
                app.restart();
            }
            Ok(false) => shell(&app).update_ready.store(false, Ordering::SeqCst),
            Err(e) => show_error(&app, format!("The update could not be installed: {e}")),
        }
    });
}

/// Build the tray, open the right page, start the status poller and the
/// update check. Called from lib.rs setup().
pub fn init(app: &AppHandle) -> tauri::Result<()> {
    app.manage(Shell::new(app));

    let sh = shell(app);
    let entries = tray::entries(sh.server_side(), &ServerStatus::Starting, false);
    let mut tray = TrayIconBuilder::with_id(TRAY_ID)
        .menu(&build_tray_menu(app, &entries)?)
        .tooltip("Maktaba")
        .on_menu_event(|app, event| on_tray_event(app, event.id().as_ref()));
    if let Some(icon) = app.default_window_icon() {
        tray = tray.icon(icon.clone());
    }
    tray.build(app)?;

    if let Target::Remote(url) = sh.target() {
        if let Err(e) = navigate_main(app, &url) {
            show_error(app, format!("Cannot open {url}: {e}"));
        }
    }

    let handle = app.clone();
    std::thread::spawn(move || loop {
        refresh(&handle);
        std::thread::sleep(POLL_EVERY);
    });

    let handle = app.clone();
    std::thread::spawn(move || loop {
        if let Ok(updater) = handle.updater() {
            if let Ok(Some(_)) = tauri::async_runtime::block_on(updater.check()) {
                shell(&handle).update_ready.store(true, Ordering::SeqCst);
            }
        }
        std::thread::sleep(UPDATE_CHECK_EVERY);
    });
    Ok(())
}

// ---------------------------------------------------------------------------
// Commands for the setup screens (bundled web app only; the remote
// capability does not grant them).
// ---------------------------------------------------------------------------

#[derive(Serialize)]
pub struct AppState {
    #[serde(flatten)]
    target: Target,
    server_available: bool,
    login_item: LoginItem,
    /// The user chose "Connect to another Maktaba…": setup must not adopt
    /// an already configured server on its own.
    reset: bool,
    /// The OS the app runs on ("macos", "windows", "linux"): the setup
    /// screens' words follow it ("this Mac" / "this PC", the menu bar / the
    /// tray; Epic 38 story 38.6).
    platform: &'static str,
}

#[tauri::command(async)]
pub fn app_state(app: AppHandle) -> AppState {
    let sh = shell(&app);
    AppState {
        target: sh.target(),
        server_available: sh.server_bin.is_some(),
        login_item: agent::login_item(),
        reset: sh.force_setup.load(Ordering::SeqCst),
        platform: std::env::consts::OS,
    }
}

/// Run one `maktaba-server setup --json` step. Progress lines go to
/// `on_progress`; the input (which may hold a password or token) is
/// passed on stdin and never logged.
#[tauri::command]
pub async fn setup_step(
    app: AppHandle,
    step: String,
    input: Value,
    on_progress: Channel<Progress>,
) -> Result<Value, StepError> {
    let bin = shell(&app).server_bin.clone().ok_or_else(|| StepError {
        code: "server_missing".into(),
        message: "This build of Maktaba does not include the server.".into(),
        field: None,
        details: Vec::new(),
    })?;
    tauri::async_runtime::spawn_blocking(move || {
        setup::run_step(&bin, &step, &input, |p| {
            let _ = on_progress.send(p);
        })
    })
    .await
    .unwrap_or_else(|e| {
        Err(StepError {
            code: "internal".into(),
            message: e.to_string(),
            field: None,
            details: Vec::new(),
        })
    })
}

/// After the `finish` step: register the login item (which starts the
/// server), then remember server mode. Async commands run off the main
/// thread (SMAppService and the status probe can take seconds).
#[tauri::command(async)]
pub fn finish_setup(
    app: AppHandle,
    web_url: String,
    supervisor_url: Option<String>,
) -> Result<LoginItem, String> {
    let web_url =
        mode::valid_local_url(&web_url).ok_or("the server's web address must be on this Mac")?;
    let supervisor_url = supervisor_url
        .and_then(|u| mode::valid_local_url(&u))
        .unwrap_or_else(|| format!("http://{}", status::DEFAULT_SUPERVISOR_ADDR));
    let sh = shell(&app);
    sh.mark_started();
    // Registered first: if it fails, no server mode is saved, and the next
    // launch shows setup again instead of waiting on a server that never starts.
    agent::register()?;
    sh.set_mode(AppMode::Server {
        web_url,
        supervisor_url,
    })?;
    refresh(&app);
    Ok(agent::login_item())
}

#[tauri::command(async)]
pub fn open_login_items() {
    agent::open_login_items_settings();
}

#[tauri::command(async)]
pub fn server_status(app: AppHandle) -> ServerStatus {
    shell(&app).status.lock().unwrap().clone()
}

/// The starting screen calls this once the server is running.
#[tauri::command(async)]
pub fn open_local_server(app: AppHandle) -> Result<(), String> {
    match shell(&app).mode() {
        Some(AppMode::Server { web_url, .. }) => navigate_main(&app, &web_url),
        _ => Err("this Mac does not run the server".into()),
    }
}

/// Client mode: connect the window to another Maktaba.
#[tauri::command(async)]
pub fn use_remote_server(app: AppHandle, url: String) -> Result<(), String> {
    let url = mode::valid_server_url(&url)
        .ok_or("enter an address that starts with http:// or https://")?;
    // this Mac no longer runs a server: take the login item out
    if agent::login_item() == LoginItem::Enabled {
        agent::unregister()?;
    }
    shell(&app).set_mode(AppMode::Client {
        server_url: url.clone(),
    })?;
    refresh(&app);
    navigate_main(&app, &url)
}

/// Pages the setup screens may open in the browser (Plex link, the
/// Hugging Face token and model pages).
pub fn allowed_external(url: &str) -> bool {
    [
        "https://plex.tv/",
        "https://app.plex.tv/",
        "https://huggingface.co/",
    ]
    .iter()
    .any(|p| url.starts_with(p))
}

#[tauri::command(async)]
pub fn open_external(url: String) -> Result<(), String> {
    if !allowed_external(&url) {
        return Err(format!("not opening {url}"));
    }
    std::process::Command::new("/usr/bin/open")
        .arg(&url)
        .spawn()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_the_setup_sites_open_externally() {
        assert!(allowed_external("https://plex.tv/link"));
        assert!(allowed_external("https://huggingface.co/settings/tokens"));
        assert!(!allowed_external("https://plex.tv.evil.com/"));
        assert!(!allowed_external("http://plex.tv/link"));
        assert!(!allowed_external("file:///Applications"));
    }
}
