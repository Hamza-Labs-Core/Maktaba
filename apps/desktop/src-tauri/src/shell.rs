// The app around the server (Epic 36 stories 36.5 / 36.6 on a Mac, Epic 38
// story 38.3 on Windows, Epic 39 story 39.8 on Linux): the menu-bar / tray
// item, the status poller, and the commands the setup screens
// (web/src/pages/Setup/) call. Pure decisions live in status.rs, tray.rs,
// mode.rs and setup.rs; this file is the glue to tauri. The OS side
// (launchd / the sign-in task / the systemd user unit) is agent.rs.
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
use crate::status::{self, Probe, ServerStatus};
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
            // a pause from before this window started (an update relaunched it)
            paused: AtomicBool::new(agent::paused_flag()),
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
        agent::set_paused_flag(false);
        self.restart_grace();
    }

    /// "Unreachable" reads as starting again for a while (the server was
    /// switched to a new runtime), without touching a pause.
    fn restart_grace(&self) {
        *self.started_at.lock().unwrap() = Instant::now();
    }

    fn mark_paused(&self) {
        self.paused.store(true, Ordering::SeqCst);
        agent::set_paused_flag(true);
    }

    /// The server's status, and the language model's line when the
    /// supervisor runs a model server.
    fn compute_status(&self) -> (ServerStatus, Option<String>) {
        let Some(AppMode::Server { supervisor_url, .. }) = self.mode() else {
            return (ServerStatus::SetupNeeded, None);
        };
        let addr = status::addr_of(&supervisor_url)
            .unwrap_or_else(|| status::DEFAULT_SUPERVISOR_ADDR.into());
        // a live agent (Windows) whose supervisor does not answer yet is starting
        let just_started = self.started_at.lock().unwrap().elapsed() < START_GRACE
            || agent::agent_alive() == Some(true);
        let probe = status::probe(&addr);
        let model = match &probe {
            Probe::Snapshot(s) => status::model_line(s.llm.as_ref()),
            Probe::Unreachable => None,
        };
        let paused = self.paused.load(Ordering::SeqCst);
        let st = status::derive(true, paused, just_started, &probe);
        (st, model)
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

#[cfg(target_os = "linux")]
fn show_info(app: &AppHandle, msg: impl Into<String>) {
    app.dialog()
        .message(msg)
        .title("Maktaba")
        .kind(MessageDialogKind::Info)
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
    let (status, model) = if server {
        sh.compute_status()
    } else {
        (ServerStatus::SetupNeeded, None)
    };
    let update_ready = sh.update_ready.load(Ordering::SeqCst);
    let entries = tray::entries_with_model(server, &status, update_ready, model.as_deref());
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
        #[cfg(target_os = "linux")]
        sync_window_menu(&handle, &entries, &tooltip);
        if let (Some(t), Ok(menu)) = (
            handle.tray_by_id(TRAY_ID),
            build_tray_menu(&handle, &entries),
        ) {
            let _ = t.set_menu(Some(menu));
            let _ = t.set_tooltip(Some(tooltip));
        }
    });
}

/// The window menu's Server items (lib.rs adds them on Linux when no tray
/// shows) follow the tray's: the status line, and each item enabled when the
/// tray would offer it.
#[cfg(target_os = "linux")]
fn sync_window_menu(app: &AppHandle, entries: &[Entry], status_line: &str) {
    let Some(sub) = app
        .menu()
        .and_then(|m| m.get(tray::WINDOW_MENU_ID))
        .and_then(|k| k.as_submenu().cloned())
    else {
        return;
    };
    if let Some(item) = sub
        .get(tray::WINDOW_STATUS_ID)
        .and_then(|k| k.as_menuitem().cloned())
    {
        let _ = item.set_text(status_line);
    }
    for (id, _) in tray::WINDOW_ITEMS.iter().flatten() {
        let window_id = format!("{}{id}", tray::WINDOW_PREFIX);
        if let Some(item) = sub
            .get(window_id.as_str())
            .and_then(|k| k.as_menuitem().cloned())
        {
            let _ = item.set_enabled(tray::window_enabled(entries, id));
        }
    }
}

/// A click on one of the window menu's Server items: the tray item's action.
#[cfg(target_os = "linux")]
pub fn on_window_menu(app: &AppHandle, tray_id: &str) {
    on_tray_event(app, tray_id);
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
            sh.mark_paused();
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
            sh.mark_paused();
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
/// restart the server so it runs the new payload, then relaunch. On Windows
/// (Epic 38 contract-update) the payload's files are in use while the server
/// runs, so the server stops first; the installer replaces them, its
/// POSTINSTALL hook starts the server again and it relaunches this window.
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
            let bytes = update
                .download(|_, _| {}, || {})
                .await
                .map_err(|e| e.to_string())?;
            #[cfg(windows)]
            if matches!(shell(&app).mode(), Some(AppMode::Server { .. })) {
                // stop the server and wait for it; it keeps running (and the
                // update is not installed) when it does not stop in time
                tauri::async_runtime::spawn_blocking(agent::stop_and_wait)
                    .await
                    .map_err(|e| e.to_string())?
                    .map_err(|e| format!("the server must stop first: {e}"))?;
            }
            // Windows: this runs the installer and exits the app
            update.install(bytes).map_err(|e| e.to_string())?;
            Ok::<bool, String>(true)
        };
        match run.await {
            Ok(true) => {
                let sh = shell(&app);
                // a paused server stays paused; it runs the new payload on Resume.
                // Linux: the server runs a copy of the payload, which the new
                // app switches to when it starts (agent::linux::sync_runtime).
                if cfg!(not(target_os = "linux"))
                    && matches!(sh.mode(), Some(AppMode::Server { .. }))
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

    // Windows: the server runs whenever this PC is set up and not paused.
    // The sign-in task starts it, and restarts its agent; one ended by hand
    // (Task Scheduler's End) starts again when the window opens.
    // (Off the main thread: schtasks takes a moment.)
    #[cfg(windows)]
    {
        let handle = app.clone();
        std::thread::spawn(move || {
            let sh = shell(&handle);
            if matches!(sh.mode(), Some(AppMode::Server { .. }))
                && !sh.paused.load(Ordering::SeqCst)
                && agent::agent_alive() == Some(false)
                && agent::login_item() == LoginItem::Enabled
            {
                sh.mark_started();
                if let Err(e) = agent::control(Action::Resume) {
                    show_error(&handle, format!("The Maktaba server could not start: {e}"));
                }
            }
        });
    }

    // Linux: the server runs a copy of the payload (the runtime). After an app
    // update this app's payload is newer: copy it in and switch the unit to it
    // (stopped while `current` moves). A paused server stays paused across
    // the switch: only Resume (or the next boot) starts it. An app older than
    // the server installs nothing and says so. Then a unit that is not running
    // starts again, unless paused; and the tray's autostart entry follows the
    // AppImage if it moved (never to an older app, never back after the user
    // removed it).
    #[cfg(target_os = "linux")]
    {
        use crate::agent::linux::{self, Synced};
        use crate::lang::t;

        let handle = app.clone();
        std::thread::spawn(move || {
            let sh = shell(&handle);
            if !matches!(sh.mode(), Some(AppMode::Server { .. })) {
                return;
            }
            let alive = agent::agent_alive();
            // the unit runs although the app paused it: started at boot (linger)
            // or by hand, which ends the pause
            if sh.paused.load(Ordering::SeqCst) && alive == Some(true) {
                sh.mark_started();
            }
            let mut older = false;
            match linux::sync_runtime() {
                Ok(Synced::Switched) => sh.restart_grace(),
                Ok(Synced::Current) => {}
                Ok(Synced::Older { app, server }) => {
                    older = true;
                    show_info(&handle, linux::older_message(&app, &server));
                }
                Err(e) => show_error(
                    &handle,
                    t(
                        &format!("Maktaba could not switch the server to this version: {e}"),
                        &format!("تعذّر على مكتبة نقل الخادم إلى هذا الإصدار: {e}"),
                    ),
                ),
            }
            if !sh.paused.load(Ordering::SeqCst)
                && agent::agent_alive() == Some(false)
                && agent::login_item() != LoginItem::NotRegistered
            {
                sh.mark_started();
                if let Err(e) = agent::control(Action::Resume) {
                    show_error(
                        &handle,
                        t(
                            &format!("The Maktaba server could not start: {e}"),
                            &format!("تعذّر تشغيل خادم مكتبة: {e}"),
                        ),
                    );
                }
            }
            if linux::tray_available() {
                let _ = linux::sync_autostart(older);
            }
        });
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
}

#[tauri::command(async)]
pub fn app_state(app: AppHandle) -> AppState {
    let sh = shell(&app);
    AppState {
        target: sh.target(),
        server_available: sh.server_bin.is_some(),
        login_item: agent::login_item(),
        reset: sh.force_setup.load(Ordering::SeqCst),
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
        let send = |p: Progress| {
            let _ = on_progress.send(p);
        };
        // Linux: from the runtime's copy, installed first (the venv must not
        // be built against this app's AppImage mount or package files)
        #[cfg(target_os = "linux")]
        let (bin, envs) = agent::linux::setup_server(&step, &bin, send)?;
        #[cfg(not(target_os = "linux"))]
        let envs: Vec<(String, String)> = Vec::new();
        setup::run_step(&bin, &envs, &step, &input, send)
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
    if matches!(
        agent::login_item(),
        LoginItem::Enabled | LoginItem::RequiresApproval
    ) {
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
    agent::open_path(std::ffi::OsStr::new(&url))
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
