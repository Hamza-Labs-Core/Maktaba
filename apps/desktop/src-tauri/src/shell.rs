// The app around the server (Epic 36 stories 36.5 / 36.6 on a Mac, Epic 38
// story 38.3 on Windows, Epic 39 story 39.8 on Linux): the menu-bar / tray
// item, the status poller, and the commands the setup screens
// (web/src/pages/Setup/) call. Pure decisions live in status.rs, tray.rs,
// mode.rs and setup.rs; this file is the glue to tauri. The OS side
// (launchd / the sign-in task / the systemd user unit) is agent.rs.
use std::collections::HashSet;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use serde::Serialize;
use serde_json::Value;
use tauri::{
    image::Image,
    ipc::Channel,
    menu::{Menu, MenuItem, PredefinedMenuItem},
    tray::TrayIconBuilder,
    AppHandle, Emitter, Manager, Wry,
};
use tauri_plugin_dialog::{DialogExt, MessageDialogKind};
use tauri_plugin_notification::{Action as NoticeAction, ActionType, NotificationExt};
use tauri_plugin_updater::{Updater, UpdaterExt};

use crate::agent::{self, Action, BundlePaths, LoginItem};
use crate::attempts::{self, Failure};
use crate::autoinstall;
use crate::lang::{self, Lang};
use crate::mode::{self, AppMode, Target};
use crate::setup::{self, Progress, StepError};
use crate::status::{self, Probe, Quiet, ServerStatus, StatusView};
use crate::tray::{self, Entry};
use crate::webcache;

const TRAY_ID: &str = "main";
const POLL_EVERY: Duration = Duration::from_secs(3);
const UPDATE_CHECK_EVERY: Duration = Duration::from_secs(6 * 60 * 60);
/// The longest an update's check or download may take (the updater's request timeout).
const DOWNLOAD_TIMEOUT: Duration = Duration::from_secs(45 * 60);
/// A download that gets no byte for this long has stalled: it stops with the reason.
const DOWNLOAD_STALL: Duration = Duration::from_secs(120);
/// How often the Linux app asks whether its server runs a newer release than it (catch-up).
#[cfg(target_os = "linux")]
const CATCH_UP_EVERY: Duration = Duration::from_secs(10 * 60);

/// Where the app keeps its state (mode.json, the window's cache record): the
/// app's config folder; on Windows its local data folder
/// (%LOCALAPPDATA%\com.hamzalabs.maktaba), not the roaming one.
fn state_dir(app: &AppHandle) -> PathBuf {
    #[cfg(windows)]
    if let Ok(local) = app.path().app_local_data_dir() {
        return local;
    }
    app.path()
        .app_config_dir()
        .unwrap_or_else(|_| PathBuf::from("."))
}

/// Where mode.json is kept (state_dir); on Windows it was in the roaming
/// config folder before, and is moved from there once (mode::adopt).
fn mode_path(app: &AppHandle) -> PathBuf {
    let path = state_dir(app).join("mode.json");
    #[cfg(windows)]
    if let Ok(roaming) = app.path().app_config_dir() {
        let _ = mode::adopt(&roaming.join("mode.json"), &path, agent::registered_here());
    }
    path
}

/// Make the window (lib.rs setup, before init): its caches are cleared first on
/// a release's first start (webcache.rs); hidden when the app starts in its tray.
pub fn open_window(app: &AppHandle, hidden: bool) -> tauri::Result<()> {
    let stamp = webcache::Stamp::new(state_dir(app).join(webcache::STAMP_FILE));
    webcache::open_main(app, &bundled_url(app), stamp, &own_version(app), hidden)
}

pub struct Shell {
    mode_path: PathBuf,
    mode: Mutex<Option<AppMode>>,
    /// The bundled maktaba-server, when this build carries the server.
    server_bin: Option<PathBuf>,
    paused: AtomicBool,
    started_at: Mutex<Instant>,
    /// Since when the server has not answered (status::Quiet).
    quiet: Mutex<Quiet>,
    update_ready: AtomicBool,
    /// An install the home ordered is under way, or its staging (autoinstall.rs).
    installing: AtomicBool,
    staging: AtomicBool,
    /// The home's order as last read (the tray's item for it).
    order: Mutex<Option<autoinstall::Request>>,
    /// The orders an OS notification was shown for (one per order, by run).
    notified: Mutex<HashSet<String>>,
    /// The release the Linux app older than its server catches up with
    /// (autoinstall::catch_up): the only update an app that runs the server
    /// installs of its own.
    catch_up: Mutex<Option<String>>,
    /// The app's own repository (its updater endpoint's): only its release feeds are
    /// trusted (autoinstall::own_repo).
    repo: String,
    /// The release the app's own feed offers (an app that runs no server): its update
    /// item's words and its one notification.
    update_version: Mutex<Option<String>>,
    /// The download under way: its release, the bytes so far, and the size when known
    /// (the update item shows its progress).
    downloading: Mutex<Option<(String, u64, Option<u64>)>>,
    /// The Windows portable app (no installer of its own): its update opens the release
    /// page; the setup it would run installs another Maktaba beside it.
    portable: bool,
    /// When the Linux app last asked whether its server runs a newer release (catch-up).
    #[cfg_attr(not(target_os = "linux"), allow(dead_code))]
    catch_up_at: Mutex<Option<Instant>>,
    status: Mutex<StatusView>,
    menu: Mutex<Vec<Entry>>,
    /// The badge the tray icon shows now (tray::badge_color), and the plain
    /// icon it is drawn on (RGBA, width, height). Not on a Mac.
    badge: Mutex<Option<[u8; 3]>>,
    plain_icon: Mutex<Option<(Vec<u8>, u32, u32)>>,
    /// "Connect to another Maktaba…" was chosen: show setup until a mode is saved.
    force_setup: AtomicBool,
    /// The tray's language: the system's UI language when the app started.
    lang: Lang,
    /// One refresh at a time, so tray menus are queued in the order computed.
    refresh_lock: Mutex<()>,
}

impl Shell {
    pub fn new(app: &AppHandle) -> Self {
        let mode_path = mode_path(app);
        // MAKTABA_SERVER_BIN points a dev build (`tauri dev`, outside
        // Maktaba.app) at a maktaba-server to run setup against.
        // a locally built app (the placeholder updater key) can verify no release: every
        // update it is offered or ordered says so (attempts::LOCAL_BUILD)
        if attempts::set_local_build_from(updater_pubkey(app).as_deref()) {
            attempts::log("this app was built locally (no release key): it updates nothing itself");
        }
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
            quiet: Mutex::new(Quiet::new(Instant::now())),
            update_ready: AtomicBool::new(false),
            installing: AtomicBool::new(false),
            staging: AtomicBool::new(false),
            order: Mutex::new(None),
            notified: Mutex::new(HashSet::new()),
            catch_up: Mutex::new(None),
            repo: own_repo(app),
            update_version: Mutex::new(None),
            downloading: Mutex::new(None),
            portable: portable_app(),
            catch_up_at: Mutex::new(None),
            status: Mutex::new(StatusView {
                status: ServerStatus::Starting,
                detail: None,
            }),
            menu: Mutex::new(Vec::new()),
            badge: Mutex::new(None),
            plain_icon: Mutex::new(None),
            force_setup: AtomicBool::new(false),
            refresh_lock: Mutex::new(()),
            lang: Lang::system(),
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

    /// The server's status with what it waits on, and the language model's
    /// line when the supervisor runs a model server.
    fn compute_status(&self) -> (StatusView, Option<String>) {
        let Some(AppMode::Server { supervisor_url, .. }) = self.mode() else {
            let view = StatusView {
                status: ServerStatus::SetupNeeded,
                detail: None,
            };
            return (view, None);
        };
        let addr = status::addr_of(&supervisor_url)
            .unwrap_or_else(|| status::DEFAULT_SUPERVISOR_ADDR.into());
        let probe = status::probe(&addr);
        let model = match &probe {
            Probe::Snapshot(s) => status::model_line(s.llm.as_ref(), self.lang.words()),
            Probe::Unreachable => None,
        };
        // a live agent (Windows, Linux) whose supervisor does not answer is
        // starting, until it has been quiet too long: then the log says why
        let alive = agent::agent_alive();
        let since_start = self.started_at.lock().unwrap().elapsed();
        let answered = matches!(probe, Probe::Snapshot(_));
        let quiet_for = self
            .quiet
            .lock()
            .unwrap()
            .poll(Instant::now(), answered, alive)
            .min(since_start);
        let waiting = status::waiting(since_start, alive, quiet_for, || {
            agent::server_log_tail().and_then(|t| status::failure_in_log(&t))
        });
        let paused = self.paused.load(Ordering::SeqCst);
        let status = status::derive(true, paused, &waiting, &probe);
        // what a starting or failing process waits on; a server that does not
        // answer carries its reason in the status itself
        let detail = match status {
            ServerStatus::Starting | ServerStatus::Problem { .. } => status::waiting_on(&probe),
            _ => None,
        };
        (StatusView { status, detail }, model)
    }
}

fn shell(app: &AppHandle) -> tauri::State<'_, Shell> {
    app.state::<Shell>()
}

/// The app's own repository: the one its updater endpoints (tauri.conf.json, as built)
/// point at, else autoinstall::OWN_REPO.
fn own_repo(app: &AppHandle) -> String {
    let endpoints: Vec<String> = app
        .config()
        .plugins
        .0
        .get("updater")
        .and_then(|u| u.get("endpoints"))
        .and_then(|e| e.as_array())
        .map(|a| {
            a.iter()
                .filter_map(|v| v.as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default();
    autoinstall::own_repo(endpoints.iter().map(String::as_str))
}

/// The updater's public key (tauri.conf.json plugins.updater.pubkey; the release build
/// stamps the real one, a local build keeps the placeholder).
fn updater_pubkey(app: &AppHandle) -> Option<String> {
    app.config()
        .plugins
        .0
        .get("updater")
        .and_then(|u| u.get("pubkey"))
        .and_then(|k| k.as_str())
        .map(String::from)
}

/// The Windows portable app: run from a folder no installer made (no uninstall.exe beside
/// it, which tauri's NSIS installer writes). Never on the Mac or Linux.
fn portable_app() -> bool {
    if !cfg!(windows) {
        return false;
    }
    std::env::current_exe()
        .ok()
        .and_then(|exe| exe.parent().map(|d| !d.join("uninstall.exe").exists()))
        .unwrap_or(false)
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
    // while the window's caches are cleared (a release's first start) the load
    // waits for the clear's end
    let Some(url) = webcache::admit(app, url) else {
        return Ok(());
    };
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

/// An error dialog in the tray's language: `sentence`, then the programs' own
/// errors as they are (lang::error_message).
fn show_error(app: &AppHandle, sentence: &str, errors: &[&str]) {
    let lang = shell(app).lang;
    app.dialog()
        .message(lang::error_message(sentence, errors))
        .title(lang.words().brand)
        .kind(MessageDialogKind::Error)
        .show(|_| {});
}

/// The record of this app's update attempts (attempts.rs: app-attempts.json beside the
/// home's order), where the server reads them for the Updates page.
fn attempts_path() -> Option<PathBuf> {
    agent::state_root().map(|r| attempts::path(&r))
}

/// The release this app is.
fn own_version(app: &AppHandle) -> String {
    app.package_info().version.to_string()
}

/// An OS notification that the update to `version` failed, and why (the menu says it too,
/// from the record, until the next attempt). Best effort: an error is only logged.
fn notify_failed(app: &AppHandle, version: &str, code: &str) {
    let sh = shell(app);
    // a click on it, or its button, sees the logs (notice_action)
    let shown = app
        .notification()
        .builder()
        .id(FAILED_NOTICE_ID)
        .action_type_id(FAILED_NOTICE_ACTIONS)
        .title(sh.lang.words().brand)
        .body(tray::failed_notice(sh.lang, version, code))
        .show();
    if let Err(e) = shown {
        attempts::log(&format!(
            "no notification for the failed update to {version}: {e}"
        ));
    }
}

/// The error the home's order says for a failure (autoinstall::order_error: a failure a
/// person must mend starts with its code, which the server reads).
fn order_error(f: &Failure) -> String {
    autoinstall::order_error(f.code, &f.detail)
}

/// The failed update's notification: its id, its action type and its button ("See the
/// logs"), which a click on the notification itself does too.
const FAILED_NOTICE_ID: i32 = 3501;
const FAILED_NOTICE_ACTIONS: &str = "update-failed";
const SEE_LOGS_ACTION: &str = "logs";

/// The local server's web address, when this app runs the server (None in client mode:
/// no server here to show its logs).
fn local_web_url(app: &AppHandle) -> Option<String> {
    match shell(app).mode() {
        Some(AppMode::Server { web_url, .. }) => Some(web_url),
        _ => None,
    }
}

/// "See the logs": the web logs page of this machine's server at the newest attempt's
/// time, in the app's window (where it shows the server's pages); the log folder in
/// client mode, which has no server here, and when the server does not answer (its page
/// could not open: app.log and server.log say why).
fn see_logs(app: &AppHandle) -> Result<(), String> {
    let Some(web_url) = local_web_url(app) else {
        return agent::open_logs();
    };
    if status::server_version(&web_url).is_none() {
        attempts::log("see the logs: the server does not answer; the log folder");
        return agent::open_logs();
    }
    let last = attempts_path()
        .and_then(|p| attempts::newest(&p))
        .unwrap_or_default();
    let url = attempts::logs_url(&web_url, &last);
    attempts::log(&format!("see the logs: {url}"));
    focus_main(app);
    navigate_main(app, &url)
}

/// What the local server shows (attempts::wait_for_server): the release its api answers
/// on, its supervisor answering while its api does not yet (starting), or nothing.
fn server_seen(web_url: &str, supervisor_url: &str) -> attempts::Seen {
    if let Some(v) = status::server_version(web_url) {
        return attempts::Seen::On(v);
    }
    let busy = status::addr_of(supervisor_url)
        .is_some_and(|a| !matches!(status::probe(&a), status::Probe::Unreachable));
    if busy {
        attempts::Seen::Busy
    } else {
        attempts::Seen::Silent
    }
}

/// Why the server did not come back on `to` after the app's own update: what it showed
/// last; on a Mac where nothing answered, what its login item says (macOS refuses it for an
/// app without its code signature: login_item_unsigned); else the line of server.log that
/// says why, when one does.
fn server_not_back(to: &str, last: &attempts::Seen) -> Failure {
    let said = match last {
        attempts::Seen::On(v) => format!("the server still runs {v}, not {to}"),
        attempts::Seen::Busy => format!(
            "the server did not finish starting on {to} in {} minutes",
            attempts::SERVER_BUSY.as_secs() / 60
        ),
        attempts::Seen::Silent => format!("the server did not answer after the update to {to}"),
    };
    #[cfg(target_os = "macos")]
    if *last == attempts::Seen::Silent {
        if let Some(f) = login_item_failure(&said) {
            return f;
        }
    }
    let detail = match agent::server_log_tail().and_then(|t| status::failure_in_log(&t)) {
        Some(why) => format!("{said}: {why}"),
        None => said,
    };
    Failure::new(attempts::STEP_RELAUNCH, attempts::RELAUNCH_FAILED, detail)
}

/// The server's login item, when the server stays silent after the app's update (macOS):
/// registering it again says why macOS will not load it; a login item turned off in System
/// Settings says that.
#[cfg(target_os = "macos")]
fn login_item_failure(said: &str) -> Option<Failure> {
    let before = agent::login_item();
    match agent::register() {
        Err(e) => {
            let code = if e.starts_with(attempts::LOGIN_ITEM_UNSIGNED) {
                attempts::LOGIN_ITEM_UNSIGNED
            } else {
                attempts::RELAUNCH_FAILED
            };
            Some(Failure::new(
                attempts::STEP_RELAUNCH,
                code,
                format!("{said}; the server's login item ({before:?}): {e}"),
            ))
        }
        Ok(()) if agent::login_item() == LoginItem::RequiresApproval => Some(Failure::new(
            attempts::STEP_RELAUNCH,
            attempts::RELAUNCH_FAILED,
            format!(
                "{said}; the server's login item is turned off in System Settings, Login Items"
            ),
        )),
        Ok(()) => None,
    }
}

/// A click on one of this app's notifications (the plugin reports desktop clicks once a
/// handler exists): on the failed update's, or its "See the logs" button, the logs. Where
/// the platform does not say which notification was clicked, a click sees the logs while
/// the newest attempt is failed.
fn notice_action(app: &AppHandle, action: &str, notice: Option<i32>) {
    let failed_notice = notice == Some(FAILED_NOTICE_ID)
        || action == SEE_LOGS_ACTION
        || (notice.is_none()
            && attempts_path()
                .and_then(|p| attempts::newest(&p))
                .is_some_and(|a| a.outcome == attempts::FAILED));
    let result = if failed_notice {
        see_logs(app)
    } else {
        focus_main(app);
        Ok(())
    };
    if let Err(e) = result {
        attempts::log(&format!("the notification's logs could not be opened: {e}"));
    }
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

/// The home's order to install a release (Epic 35, autoinstall.rs; owner decision D1:
/// ask first): staged and shown when it comes, installed at the home's go once someone
/// clicked here. Read on every status poll and when the server opens the app with
/// maktaba://install-update.
pub fn install_requested(app: &AppHandle) {
    let sh = shell(app);
    let Some(root) = agent::state_root() else {
        return;
    };
    let path = autoinstall::request_path(&root);
    let req = autoinstall::read(&path);
    *sh.order.lock().unwrap() = req.clone();
    let Some(r) = req else {
        // no order (withdrawn, or installed): what was staged goes
        autoinstall::clean_staged(&root, None);
        return;
    };
    let staged = autoinstall::staged_path(&root, &r.version);
    let server_side = matches!(sh.mode(), Some(AppMode::Server { .. }));
    // a locally built app can take no release: the order says so at once (a machine that
    // needs a person: its release installed by hand once), recorded once per order
    if server_side
        && attempts::local_build()
        && r.by_app()
        && matches!(
            r.state.as_str(),
            autoinstall::ORDERED | autoinstall::STAGED | autoinstall::GO
        )
    {
        let f = attempts::local_build_failure(attempts::STEP_CHECK, &own_version(app));
        let _ = autoinstall::write_state(&path, &r, autoinstall::FAILED, &order_error(&f));
        if sh
            .notified
            .lock()
            .unwrap()
            .insert(format!("local:{}", r.run_id))
        {
            if let Some(rec) = attempts_path() {
                attempts::record_failed(
                    &rec,
                    attempts::TRIGGER_ORDER,
                    &own_version(app),
                    &r.version,
                    &f,
                );
            }
            notify_failed(app, &r.version, f.code);
        }
        return;
    }
    if server_side {
        notify_ready(app, &r);
    }
    let busy = sh.staging.load(Ordering::SeqCst) || sh.installing.load(Ordering::SeqCst);
    match autoinstall::decide(
        Some(&r),
        server_side,
        sh.paused.load(Ordering::SeqCst),
        staged.exists(),
        busy,
        &sh.repo,
    ) {
        autoinstall::Action::Nothing => {}
        autoinstall::Action::Refuse(why) => {
            let _ = autoinstall::write_state(&path, &r, autoinstall::FAILED, &why);
        }
        autoinstall::Action::Show => {
            let _ = autoinstall::write_state(&path, &r, autoinstall::STAGED, "");
        }
        autoinstall::Action::Stage { .. } => {
            if sh.staging.swap(true, Ordering::SeqCst) {
                return;
            }
            autoinstall::clean_staged(&root, Some(&staged));
            stage_order(app.clone(), path, r, staged);
        }
        autoinstall::Action::Install { version, feed } => {
            if sh.installing.swap(true, Ordering::SeqCst) {
                return;
            }
            let _ = autoinstall::write_state(&path, &r, autoinstall::INSTALLING, "");
            install_update_from(
                app.clone(),
                Source::Ordered(Box::new(Ordered {
                    path,
                    req: r,
                    version,
                    feed,
                    staged,
                })),
            );
        }
    }
}

/// One OS notification per order once it is ready here (staged): the Mac's menu-bar item
/// has no badge, and no tray says it unasked. Best effort: an error is only logged.
fn notify_ready(app: &AppHandle, req: &autoinstall::Request) {
    let sh = shell(app);
    let ready = autoinstall::to_notify(Some(req), &mut sh.notified.lock().unwrap());
    let Some(autoinstall::Shown::Ready { version, home }) = ready else {
        return;
    };
    let shown = app
        .notification()
        .builder()
        .title(sh.lang.words().brand)
        .body(tray::order_notice(sh.lang, &version, home))
        .show();
    if let Err(e) = shown {
        eprintln!("maktaba: no notification for Maktaba {version}: {e}");
    }
}

/// An updater that takes exactly `version` from `feed` (the release's own), whatever
/// this app's own version is: the same one (an app rolled back that holds it already) or
/// a newer one; any other version the feed offers is no update (autoinstall::exactly).
fn exact_updater(app: &AppHandle, feed: &str, version: &str) -> Result<Updater, String> {
    let url: tauri::Url = feed.parse().map_err(|e| format!("{e}"))?;
    let wanted = version.to_string();
    app.updater_builder()
        .endpoints(vec![url])
        .map_err(|e| e.to_string())?
        .timeout(DOWNLOAD_TIMEOUT)
        .version_comparator(move |_, release| {
            autoinstall::exactly(&wanted, &release.version.to_string())
        })
        .build()
        .map_err(|e| e.to_string())
}

/// stage_order downloads the ordered release's app update from its own feed (checked
/// with this app's key), keeps it, and says staged (the tray shows it); or failed.
/// A failure of the staging is recorded as a failed attempt of the order (attempts.rs),
/// told in an OS notification and in the menu, and said in the order for the server.
fn stage_order(app: AppHandle, path: PathBuf, req: autoinstall::Request, staged: PathBuf) {
    tauri::async_runtime::spawn(async move {
        let ctx = attempts::Ctx::here();
        let run = async {
            // what would keep the install from going in without a person, said now: the
            // updater's password prompt never comes at the home's go
            #[cfg(target_os = "macos")]
            if let Some(f) = swap_blocker() {
                return Err(f);
            }
            let update = exact_updater(&app, &req.feed, &req.version)
                .map_err(|e| attempts::of_text(attempts::STEP_CHECK, e, &ctx))?
                .check()
                .await
                .map_err(|e| attempts::of_updater(attempts::STEP_CHECK, &e, &ctx))?
                .ok_or_else(|| {
                    Failure::new(
                        attempts::STEP_CHECK,
                        attempts::FEED_INVALID,
                        format!("no app update for {} in its feed", req.version),
                    )
                })?;
            if !autoinstall::same_version(&update.version, &req.version) {
                return Err(Failure::new(
                    attempts::STEP_CHECK,
                    attempts::FEED_INVALID,
                    format!("the feed offers {}, not {}", update.version, req.version),
                ));
            }
            let bytes = download_watched(&app, &update)
                .await
                .map_err(|e| attempts::of_updater(attempts::STEP_DOWNLOAD, &e, &ctx))?;
            let io = |e: std::io::Error| {
                let mut f = attempts::of_text(attempts::STEP_DOWNLOAD, e.to_string(), &ctx);
                f.code = attempts::classify_io(attempts::STEP_DOWNLOAD, &e, &ctx);
                f.fix = attempts::fix_for(f.code);
                f
            };
            let part = staged.with_extension("part");
            std::fs::write(&part, &bytes).map_err(io)?;
            std::fs::rename(&part, &staged).map_err(io)?;
            Ok::<(), Failure>(())
        };
        match run.await {
            Ok(()) => {
                let _ = autoinstall::write_state(&path, &req, autoinstall::STAGED, "");
            }
            Err(f) => {
                let _ =
                    autoinstall::write_state(&path, &req, autoinstall::FAILED, &order_error(&f));
                if let Some(rec) = attempts_path() {
                    attempts::record_failed(
                        &rec,
                        attempts::TRIGGER_ORDER,
                        &own_version(&app),
                        &req.version,
                        &f,
                    );
                }
                notify_failed(&app, &req.version, f.code);
            }
        }
        shell(&app).staging.store(false, Ordering::SeqCst);
        refresh(&app);
    });
}

/// macOS: what keeps this Maktaba.app from being replaced without a person
/// (autoinstall::swap_blocker: translocated, a read-only volume, a folder that needs an
/// administrator, App Management or a lock), as the install's failure; None when this user
/// can replace it, or outside a bundle.
#[cfg(target_os = "macos")]
fn swap_blocker() -> Option<Failure> {
    let exe = std::env::current_exe().ok()?;
    let bundle = autoinstall::app_bundle_of(&exe)?;
    let (code, why) = autoinstall::swap_blocker(&bundle, &autoinstall::Probe::here())?;
    Some(Failure::new(attempts::STEP_INSTALL, code, why))
}

/// Downloads `update` with its progress in the tray's update item. A download that gets no
/// byte for DOWNLOAD_STALL stops, with words the reasons read as no network (the updater's
/// own timeout, DOWNLOAD_TIMEOUT, bounds the whole of it).
async fn download_watched(
    app: &AppHandle,
    update: &tauri_plugin_updater::Update,
) -> Result<Vec<u8>, tauri_plugin_updater::Error> {
    use std::sync::atomic::AtomicU64;
    use std::sync::Arc;
    let now = || {
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_secs())
            .unwrap_or(0)
    };
    let last = Arc::new(AtomicU64::new(now()));
    let version = update.version.clone();
    *shell(app).downloading.lock().unwrap() = Some((version.clone(), 0, None));
    let (task_app, task_last, task_update) = (app.clone(), last.clone(), update.clone());
    let task = tauri::async_runtime::spawn(async move {
        let mut got = 0u64;
        task_update
            .download(
                move |n, total| {
                    got += n as u64;
                    task_last.store(now(), Ordering::SeqCst);
                    *shell(&task_app).downloading.lock().unwrap() =
                        Some((version.clone(), got, total));
                },
                || {},
            )
            .await
    });
    let stalled = loop {
        if task.inner().is_finished() {
            break false;
        }
        if now().saturating_sub(last.load(Ordering::SeqCst)) >= DOWNLOAD_STALL.as_secs() {
            task.abort();
            break true;
        }
        // a second, off the async runtime's threads
        let _ = tauri::async_runtime::spawn_blocking(|| std::thread::sleep(Duration::from_secs(1)))
            .await;
    };
    *shell(app).downloading.lock().unwrap() = None;
    if stalled {
        return Err(tauri_plugin_updater::Error::Network(format!(
            "the download stalled: no data for {} s (timed out)",
            DOWNLOAD_STALL.as_secs()
        )));
    }
    task.await
        .map_err(|e| tauri_plugin_updater::Error::Network(e.to_string()))?
}

/// An install the home ordered: where to say how it went, what to install, and where
/// it was staged.
struct Ordered {
    path: PathBuf,
    req: autoinstall::Request,
    version: String,
    feed: String,
    staged: PathBuf,
}

/// What an install installs.
enum Source {
    /// The newest release the app's own feed offers (an app in client mode).
    Own,
    /// Exactly this release, from its own feed (the Linux app older than its server).
    Release { version: String, feed: String },
    /// The release the home ordered (no dialog: the server's run reports how it went).
    Ordered(Box<Ordered>),
}

impl Source {
    /// The one version this install takes (None: the newest its feed offers).
    fn version(&self) -> Option<&str> {
        match self {
            Source::Own => None,
            Source::Release { version, .. } => Some(version),
            Source::Ordered(o) => Some(&o.version),
        }
    }

    fn ordered(&self) -> Option<&Ordered> {
        match self {
            Source::Ordered(o) => Some(o),
            _ => None,
        }
    }

    /// The install may go on: the home's order still says go for its run (read again
    /// now: a cancel, a newer run or a skip stops it); any other install may.
    fn still_go(&self) -> bool {
        self.ordered()
            .map_or(true, |o| autoinstall::still_go(&o.path, &o.req.run_id))
    }
}

/// How an install ended without an error.
enum Done {
    /// Installed: the app restarts on it.
    Installed,
    /// The app's own feed offers nothing newer.
    NoUpdate,
    /// The home's order no longer says go: nothing was installed.
    Withdrawn,
}

/// Recompute the status; rebuild the tray menu only when it changed.
fn refresh(app: &AppHandle) {
    let sh = shell(app);
    // Held until the menu update is queued: two refreshes (the poller and
    // a tray click) must reach the main thread in the order they computed.
    let _one_at_a_time = sh.refresh_lock.lock().unwrap();
    let server = sh.server_side();
    let (view, model) = if server {
        sh.compute_status()
    } else {
        let view = StatusView {
            status: ServerStatus::SetupNeeded,
            detail: None,
        };
        (view, None)
    };
    if server {
        install_requested(app);
    }
    let order = if server {
        autoinstall::shown(sh.order.lock().unwrap().as_ref())
    } else {
        None
    };
    let update_ready = sh.update_ready.load(Ordering::SeqCst);
    // the server's English detail, for a tray that shows it (lang.rs)
    let detail = view
        .detail
        .as_ref()
        .map(|d| d.text.as_str())
        .filter(|t| !t.is_empty());
    let (status, lang) = (&view.status, sh.lang);
    let entries = tray::entries_with_order(
        lang,
        server,
        status,
        update_ready,
        order.as_ref(),
        model.as_deref(),
        detail,
    );
    // the last update attempt, when it failed: its reason until the next attempt
    let last = attempts_path().and_then(|p| attempts::newest(&p));
    let entries = tray::with_failed_update(
        entries,
        tray::failed_line(lang, last.as_ref(), &own_version(app)),
        lang,
        server && matches!(sh.mode(), Some(AppMode::Server { .. })),
    );
    // the portable app's update is a new zip, not the setup beside it
    let entries = match sh.update_version.lock().unwrap().clone() {
        Some(v) if !server && sh.portable => tray::with_label(
            entries,
            "update",
            &lang::fill(lang.words().update_out_portable, &[("version", &v)]),
            true,
        ),
        _ => entries,
    };
    // a download under way: its progress in the update item
    let entries = match sh.downloading.lock().unwrap().clone() {
        Some((v, got, total)) => tray::with_label(
            entries,
            "update",
            &tray::download_label(lang, &v, got, total),
            false,
        ),
        None => entries,
    };
    // the Mac's menu-bar item keeps its plain icon; an app that runs no server shows only
    // its own update
    let badge = if cfg!(target_os = "macos") {
        None
    } else if server {
        tray::badge_color(status, update_ready || order.is_some())
    } else {
        tray::client_badge(update_ready)
    };
    let tooltip = if server {
        tray::tooltip(&tray::status_line(lang, status, detail))
    } else {
        lang.words().brand.into()
    };
    *sh.status.lock().unwrap() = view.clone();
    let mut last = sh.menu.lock().unwrap();
    let mut last_badge = sh.badge.lock().unwrap();
    let new_badge = *last_badge != badge;
    if *last == entries && !new_badge {
        return;
    }
    *last = entries.clone();
    *last_badge = badge;
    drop((last, last_badge));
    // the icon with this badge (the plain icon without one)
    let icon = if new_badge {
        let plain = sh.plain_icon.lock().unwrap();
        plain.as_ref().map(|(rgba, w, h)| match badge {
            Some(c) => Image::new_owned(tray::badge(rgba, *w, *h, c), *w, *h),
            None => Image::new_owned(rgba.clone(), *w, *h),
        })
    } else {
        None
    };
    // the window's Server menu says what the tray says (Linux)
    #[cfg(target_os = "linux")]
    let window_line = if server {
        tray::status_line(lang, status, detail)
    } else {
        lang.words().brand.to_string()
    };
    #[cfg(target_os = "linux")]
    let window_update = tray::window_update_label(lang, order.as_ref());
    let handle = app.clone();
    let _ = app.run_on_main_thread(move || {
        #[cfg(target_os = "linux")]
        sync_window_menu(&handle, &entries, &window_line, &window_update);
        if let (Some(t), Ok(menu)) = (
            handle.tray_by_id(TRAY_ID),
            build_tray_menu(&handle, &entries),
        ) {
            let _ = t.set_menu(Some(menu));
            let _ = t.set_tooltip(Some(tooltip));
            if let Some(icon) = icon {
                let _ = t.set_icon(Some(icon));
            }
        }
    });
}

/// The window menu's Server items (lib.rs adds them on Linux when no tray
/// shows) follow the tray's: the status line, the update item's words (the
/// home's order as the tray says it), and each item enabled when the tray
/// would offer it.
#[cfg(target_os = "linux")]
fn sync_window_menu(app: &AppHandle, entries: &[Entry], status_line: &str, update_label: &str) {
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
    for id in tray::WINDOW_ITEMS.iter().flatten() {
        let window_id = format!("{}{id}", tray::WINDOW_PREFIX);
        if let Some(item) = sub
            .get(window_id.as_str())
            .and_then(|k| k.as_menuitem().cloned())
        {
            if *id == "update" {
                let _ = item.set_text(update_label);
            }
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
        "open-logs" | "update-log-folder" => agent::open_logs(),
        "update-logs" => see_logs(app),
        "change-server" => sh.clear_mode().and_then(|()| {
            focus_main(app);
            navigate_main(app, &bundled_url(app))
        }),
        "update" => update_clicked(app),
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
        show_error(app, tray::action_failed(sh.lang, id), &[&e]);
    }
    refresh(app);
}

/// The tray's update item (autoinstall::click): the home's order, staged here, is
/// clicked (it installs at the home's go). An app that runs the server installs
/// nothing else of its own, but the Linux app older than its server, which catches up
/// with exactly the server's release; a client installs its own feed's update.
fn update_clicked(app: &AppHandle) -> Result<(), String> {
    let sh = shell(app);
    let order = sh.order.lock().unwrap().clone();
    let catch_up = sh.catch_up.lock().unwrap().clone();
    match autoinstall::click(order.as_ref(), sh.server_side(), catch_up.as_deref()) {
        autoinstall::Click::Order(r) => {
            let root = agent::state_root().ok_or("no state folder")?;
            autoinstall::write_state(
                &autoinstall::request_path(&root),
                &r,
                autoinstall::CLICKED,
                "",
            )
            .map_err(|e| e.to_string())
        }
        // the portable app installs nothing: the release page has its new zip
        autoinstall::Click::OwnFeed if sh.portable => agent::open_path(std::ffi::OsStr::new(
            &format!("{}/releases/latest", sh.repo),
        )),
        // one install at a time (a second click while one runs does nothing)
        _ if sh.installing.load(Ordering::SeqCst) || sh.staging.load(Ordering::SeqCst) => Ok(()),
        autoinstall::Click::OwnFeed => {
            if !sh.installing.swap(true, Ordering::SeqCst) {
                install_update_from(app.clone(), Source::Own);
            }
            Ok(())
        }
        autoinstall::Click::Release(version) => {
            if !sh.installing.swap(true, Ordering::SeqCst) {
                let feed = autoinstall::release_feed(&sh.repo, &version);
                install_update_from(app.clone(), Source::Release { version, feed });
            }
            Ok(())
        }
        // the order moved on since the menu was drawn: the next refresh redraws it
        autoinstall::Click::Nothing => Ok(()),
    }
}

/// Story 36.8 replaces the whole app (shell + server payload): install,
/// restart the server so it runs the new payload, then relaunch. On Windows
/// (Epic 38 contract-update) the payload's files are in use while the server
/// runs, so the server stops first; the installer replaces them, its
/// POSTINSTALL hook starts the server again and it relaunches this window.
/// When the stop times out or the install fails, the server is started again
/// (unless it was paused): asked to stop, it would stay down. Of the release
/// the home ordered (Source::Ordered: from that release's own feed, only while
/// the order still says go, and with no dialog: the server's run reports how it
/// went), exactly a release (Source::Release), or the newest of the app's own
/// feed (Source::Own).
fn install_update_from(app: AppHandle, source: Source) {
    tauri::async_runtime::spawn(async move {
        // the server was asked to stop for the install
        #[cfg(any(windows, target_os = "macos"))]
        let stopped = AtomicBool::new(false);
        let ctx = attempts::Ctx::here();
        // the attempt's record (attempts.rs): every step, and why it failed
        let rec = attempts_path();
        let trigger = if source.ordered().is_some() {
            attempts::TRIGGER_ORDER
        } else {
            attempts::TRIGGER_BADGE
        };
        let id = rec.as_deref().map(|p| {
            attempts::begin(
                p,
                trigger,
                &own_version(&app),
                source.version().unwrap_or(""),
            )
        });
        let step = |name: &str| {
            if let (Some(p), Some(id)) = (rec.as_deref(), id.as_deref()) {
                attempts::set_step(p, id, name);
            }
        };
        let failed = |f: Failure| (false, f);
        let mut to = source.version().unwrap_or("").to_string();
        let run = async {
            // a locally built app can verify no release
            if attempts::local_build() {
                return Err(failed(attempts::local_build_failure(
                    attempts::STEP_CHECK,
                    &own_version(&app),
                )));
            }
            let updater = match &source {
                Source::Own => app
                    .updater_builder()
                    .timeout(DOWNLOAD_TIMEOUT)
                    .build()
                    .map_err(|e| attempts::of_updater(attempts::STEP_CHECK, &e, &ctx)),
                Source::Release { version, feed } => exact_updater(&app, feed, version)
                    .map_err(|e| attempts::of_text(attempts::STEP_CHECK, e, &ctx)),
                Source::Ordered(o) => exact_updater(&app, &o.feed, &o.version)
                    .map_err(|e| attempts::of_text(attempts::STEP_CHECK, e, &ctx)),
            }
            .map_err(failed)?;
            let update = updater
                .check()
                .await
                .map_err(|e| failed(attempts::of_updater(attempts::STEP_CHECK, &e, &ctx)))?;
            let Some(update) = update else {
                return match source.version() {
                    Some(v) => Err(failed(Failure::new(
                        attempts::STEP_CHECK,
                        attempts::FEED_INVALID,
                        format!("no app update for {v} in its feed"),
                    ))),
                    None => Ok(Done::NoUpdate),
                };
            };
            if let Some(v) = source.version() {
                if !autoinstall::same_version(&update.version, v) {
                    return Err(failed(Failure::new(
                        attempts::STEP_CHECK,
                        attempts::FEED_INVALID,
                        format!("the feed offers {}, not {v}", update.version),
                    )));
                }
            }
            if to.is_empty() {
                to = update.version.clone();
                if let (Some(p), Some(id)) = (rec.as_deref(), id.as_deref()) {
                    attempts::set_to(p, id, &to);
                }
            }
            // what was staged at the order (install checks it again), else downloaded now
            let bytes = match source.ordered().and_then(|o| std::fs::read(&o.staged).ok()) {
                Some(b) => b,
                None => {
                    step(attempts::STEP_DOWNLOAD);
                    download_watched(&app, &update).await.map_err(|e| {
                        failed(attempts::of_updater(attempts::STEP_DOWNLOAD, &e, &ctx))
                    })?
                }
            };
            // checked before anything stops: a staged file that does not verify is no
            // reason to take the server down (install checks it again)
            step(attempts::STEP_VERIFY);
            update
                .verify(&bytes)
                .map_err(|e| failed(attempts::of_updater(attempts::STEP_VERIFY, &e, &ctx)))?;
            #[cfg(windows)]
            if matches!(shell(&app).mode(), Some(AppMode::Server { .. })) {
                if !source.still_go() {
                    return Ok(Done::Withdrawn);
                }
                // stop the server and wait for it (the install hold keeps the
                // task's watchdog from starting it meanwhile); the update is
                // not installed when it does not stop in time
                step(attempts::STEP_INSTALL);
                stopped.store(true, Ordering::SeqCst);
                let stop_failed = |e: String| {
                    (
                        true,
                        Failure::new(attempts::STEP_INSTALL, attempts::SERVER_STOP_FAILED, e),
                    )
                };
                tauri::async_runtime::spawn_blocking(agent::stop_for_install)
                    .await
                    .map_err(|e| stop_failed(e.to_string()))?
                    .map_err(stop_failed)?;
            }
            if !source.still_go() {
                return Ok(Done::Withdrawn);
            }
            // macOS: the updater's password prompt and its replacing of the app are one
            // step, so an ordered install that would prompt is not tried (checked at the
            // staging, and again now): answered after the server stopped waiting, it would
            // install a release no run watches
            #[cfg(target_os = "macos")]
            if source.ordered().is_some() {
                if let Some(f) = swap_blocker() {
                    return Err(failed(f));
                }
            }
            // macOS: the server stops before the bundle is swapped (stop_before_swap); the
            // restart after the install starts it on the new one, a failure starts it again
            #[cfg(target_os = "macos")]
            if let Some(AppMode::Server { supervisor_url, .. }) = shell(&app).mode() {
                if !shell(&app).paused.load(Ordering::SeqCst) {
                    step(attempts::STEP_INSTALL);
                    stopped.store(true, Ordering::SeqCst);
                    let stop_failed = |e: String| {
                        (
                            true,
                            Failure::new(attempts::STEP_INSTALL, attempts::SERVER_STOP_FAILED, e),
                        )
                    };
                    tauri::async_runtime::spawn_blocking(move || stop_before_swap(&supervisor_url))
                        .await
                        .map_err(|e| stop_failed(e.to_string()))?
                        .map_err(stop_failed)?;
                }
            }
            // recorded before: on Windows the installer ends this app; the next start
            // settles the attempt (installed, or relaunch_failed)
            step(attempts::STEP_INSTALL);
            update
                .install(bytes)
                .map_err(|e| failed(attempts::of_updater(attempts::STEP_INSTALL, &e, &ctx)))?;
            // the error: whether the server would not stop for it, and why
            Ok::<Done, (bool, Failure)>(Done::Installed)
        };
        let outcome = run.await;
        match outcome {
            Ok(Done::Installed) => {
                if let Some(o) = source.ordered() {
                    let _ = std::fs::remove_file(&o.staged);
                }
                let sh = shell(&app);
                // a paused server stays paused; it runs the new payload on Resume.
                // Linux: the server runs a copy of the payload, which the new
                // app switches to when it starts (agent::linux::sync_runtime).
                // A restart that fails is noted in the attempt: the new app waits for its
                // server on the release, and says it when the server does not come back.
                if cfg!(not(target_os = "linux"))
                    && matches!(sh.mode(), Some(AppMode::Server { .. }))
                    && !sh.paused.load(Ordering::SeqCst)
                {
                    if let (Err(e), Some(p), Some(id)) = (
                        agent::control(Action::Restart),
                        rec.as_deref(),
                        id.as_deref(),
                    ) {
                        attempts::note(
                            p,
                            id,
                            &format!("the server's restart after the install failed: {e}"),
                        );
                    }
                }
                // the new app settles the attempt when it starts (attempts::settle_newest)
                step(attempts::STEP_RELAUNCH);
                app.restart();
            }
            Ok(Done::NoUpdate) => {
                if let (Some(p), Some(id)) = (rec.as_deref(), id.as_deref()) {
                    attempts::discard(p, id);
                }
                let sh = shell(&app);
                sh.update_ready.store(false, Ordering::SeqCst);
                sh.installing.store(false, Ordering::SeqCst);
            }
            Ok(Done::Withdrawn) => {
                if let (Some(p), Some(id)) = (rec.as_deref(), id.as_deref()) {
                    attempts::cancel(p, id);
                }
                // cancelled, replaced or skipped before the install: nothing to say in an
                // order that is not this run's any more; a server stopped for it starts
                shell(&app).installing.store(false, Ordering::SeqCst);
                #[cfg(any(windows, target_os = "macos"))]
                if stopped.load(Ordering::SeqCst) {
                    let handle = app.clone();
                    let _ = tauri::async_runtime::spawn_blocking(move || {
                        start_after_failed_update(&handle)
                    })
                    .await;
                }
            }
            Err((at_stop, f)) => {
                if let (Some(p), Some(id)) = (rec.as_deref(), id.as_deref()) {
                    attempts::fail(p, id, &f);
                }
                notify_failed(&app, &to, f.code);
                if let Some(o) = source.ordered() {
                    // the server's run says it; no dialog at the update's hour (the
                    // notification and the menu say why)
                    let _ = autoinstall::write_state(
                        &o.path,
                        &o.req,
                        autoinstall::FAILED,
                        &order_error(&f),
                    );
                    shell(&app).installing.store(false, Ordering::SeqCst);
                    #[cfg(any(windows, target_os = "macos"))]
                    if stopped.load(Ordering::SeqCst) {
                        let handle = app.clone();
                        let _ = tauri::async_runtime::spawn_blocking(move || {
                            start_after_failed_update(&handle)
                        })
                        .await;
                    }
                    let _ = at_stop;
                    refresh(&app);
                    return;
                }
                shell(&app).installing.store(false, Ordering::SeqCst);
                let lang = shell(&app).lang;
                // what happened and why, in the tray's language, then the updater's own
                // words and the fix
                let head = if at_stop {
                    lang.words().update_stop_failed
                } else {
                    lang.words().update_failed
                };
                let why = tray::failed_line(
                    lang,
                    Some(&attempts::Attempt {
                        to: to.clone(),
                        outcome: attempts::FAILED.into(),
                        code: f.code.into(),
                        ..attempts::Attempt::default()
                    }),
                    &own_version(&app),
                )
                .unwrap_or_default();
                let sentence = format!(
                    "{head}
{why}"
                );
                #[cfg(any(windows, target_os = "macos"))]
                if stopped.load(Ordering::SeqCst) {
                    let w = lang.words();
                    let handle = app.clone();
                    let (again, why) = tauri::async_runtime::spawn_blocking(move || {
                        start_after_failed_update(&handle)
                    })
                    .await
                    .unwrap_or_else(|e| (w.update_start_failed, Some(e.to_string())));
                    let sentence = format!(
                        "{sentence}
{again}"
                    );
                    let why = why.unwrap_or_default();
                    refresh(&app);
                    return show_error(&app, &sentence, &[&f.detail, &f.fix, &why]);
                }
                refresh(&app);
                show_error(&app, &sentence, &[&f.detail, &f.fix])
            }
        }
    });
}

/// The client's own feed offers `version`: the update item and badge show it, and the Mac
/// (whose menu-bar item has no badge) says it once per release in an OS notification.
fn client_update_found(app: &AppHandle, version: &str) {
    let sh = shell(app);
    sh.update_ready.store(true, Ordering::SeqCst);
    let new = sh
        .update_version
        .lock()
        .unwrap()
        .replace(version.to_string())
        != Some(version.to_string());
    if new && cfg!(target_os = "macos") {
        let shown = app
            .notification()
            .builder()
            .title(sh.lang.words().brand)
            .body(lang::fill(
                sh.lang.words().update_ready_notice,
                &[("version", version)],
            ))
            .show();
        if let Err(e) = shown {
            attempts::log(&format!("no notification for Maktaba {version}: {e}"));
        }
    }
}

/// An update the home ordered that failed after the app's relaunch (its server did not come
/// back on the release): the order says so now, so the home's run fails at once with the
/// reason instead of waiting for it.
fn tell_the_order(failed: &attempts::Attempt) {
    if failed.trigger != attempts::TRIGGER_ORDER {
        return;
    }
    let Some(root) = agent::state_root() else {
        return;
    };
    let path = autoinstall::request_path(&root);
    let Some(r) = autoinstall::read(&path) else {
        return;
    };
    if autoinstall::same_version(&r.version, &failed.to)
        && matches!(r.state.as_str(), autoinstall::GO | autoinstall::INSTALLING)
    {
        let _ = autoinstall::write_state(
            &path,
            &r,
            autoinstall::FAILED,
            &autoinstall::order_error(&failed.code, &failed.detail),
        );
    }
}

/// macOS: how long the server may take to stop for an install (the launcher's 60 s grace to
/// drain its roles, and a little more).
#[cfg(target_os = "macos")]
const SWAP_STOP_WAIT: Duration = Duration::from_secs(75);

/// macOS: the server stopped before the app's bundle is swapped (B6). Its processes open the
/// payload's files by path, so a swap under them would mix two payloads: a Python minor or a
/// Postgres major of another release loaded half from each. Stopping costs only the swap's
/// seconds (the server restarts after any update). A server that does not stop in time
/// leaves the app as it is (server_stop_failed).
#[cfg(target_os = "macos")]
fn stop_before_swap(supervisor_url: &str) -> Result<(), String> {
    agent::control(Action::Pause)?;
    let Some(addr) = status::addr_of(supervisor_url) else {
        return Ok(());
    };
    let began = Instant::now();
    while began.elapsed() < SWAP_STOP_WAIT {
        if matches!(status::probe(&addr), Probe::Unreachable) {
            return Ok(());
        }
        std::thread::sleep(Duration::from_millis(500));
    }
    Err(format!(
        "the server did not stop within {} s for the install",
        SWAP_STOP_WAIT.as_secs()
    ))
}

/// macOS: how long the server may stay silent after the app's update before its login item
/// is registered again (the plist or the signature it was registered with changed).
#[cfg(target_os = "macos")]
const LOGIN_ITEM_RECHECK: Duration = Duration::from_secs(30);

/// macOS: the server's login item registered for the app that is now `to` (the contract's
/// "plist changed: unregister + register", agent::register_login_item): for this build when
/// another build registered it, or again with `force`; why not, as the attempt's failure
/// (login_item_unsigned when macOS refuses an app without its signature).
#[cfg(target_os = "macos")]
fn register_again(to: &str, force: bool) -> Result<(), Failure> {
    let before = agent::login_item();
    match agent::register_login_item(force) {
        Err(e) => {
            let code = if e.starts_with(attempts::LOGIN_ITEM_UNSIGNED) {
                attempts::LOGIN_ITEM_UNSIGNED
            } else {
                attempts::RELAUNCH_FAILED
            };
            Err(Failure::new(
                attempts::STEP_RELAUNCH,
                code,
                format!(
                    "after the update to {to}, macOS would not register the server's login item \
                     again (it was {before:?}): {e}"
                ),
            ))
        }
        // this build registered it already (at the app's start)
        Ok(false) => Ok(()),
        Ok(true) if agent::login_item() == LoginItem::RequiresApproval => Err(Failure::new(
            attempts::STEP_RELAUNCH,
            attempts::RELAUNCH_FAILED,
            format!(
                "after the update to {to}, the server's login item is turned off in System \
                 Settings › Login Items"
            ),
        )),
        Ok(true) => {
            attempts::log(&format!(
                "update to {to}: the server's login item registered again (it was {before:?})"
            ));
            Ok(())
        }
    }
}

/// Linux: every CATCH_UP_EVERY, whether the server runs a newer release than this app (the
/// server updates its own runtime, owner decision D3): the tray offers the catch-up, or
/// stops offering it once this app is that release. Never at start (sync_runtime does it).
#[cfg(target_os = "linux")]
fn recheck_catch_up(app: &AppHandle) {
    let sh = shell(app);
    if !matches!(sh.mode(), Some(AppMode::Server { .. })) {
        return;
    }
    {
        let mut at = sh.catch_up_at.lock().unwrap();
        match *at {
            None => {
                *at = Some(Instant::now());
                return;
            }
            Some(t) if t.elapsed() < CATCH_UP_EVERY => return,
            Some(_) => *at = Some(Instant::now()),
        }
    }
    if sh.installing.load(Ordering::SeqCst) {
        return;
    }
    let release = match crate::agent::linux::runtime_older() {
        Ok(Some((app_v, server, running))) => autoinstall::catch_up(&app_v, &running, &server),
        Ok(None) => None,
        Err(_) => return,
    };
    sh.update_ready.store(release.is_some(), Ordering::SeqCst);
    *sh.catch_up.lock().unwrap() = release;
}

/// After an update that failed once the server was asked to stop (Windows):
/// start it again unless it was paused. The sentence for the message, and the
/// error when it could not start.
#[cfg(any(windows, target_os = "macos"))]
fn start_after_failed_update(app: &AppHandle) -> (&'static str, Option<String>) {
    let sh = shell(app);
    let w = sh.lang.words();
    if sh.paused.load(Ordering::SeqCst) {
        return (w.update_stays_paused, None);
    }
    sh.mark_started();
    match agent::control(Action::Resume) {
        Ok(()) => (w.update_starting_again, None),
        Err(e) => (w.update_start_failed, Some(e)),
    }
}

/// Build the tray, open the right page, start the status poller and the
/// update check. Called from lib.rs setup().
pub fn init(app: &AppHandle) -> tauri::Result<()> {
    app.manage(Shell::new(app));

    let sh = shell(app);
    // the failed update's notification: its button and a click on it see the logs (the
    // plugin reports desktop clicks once a handler exists, before any notification shows)
    let notices = app.notification();
    let _ = notices.register_action_types(vec![ActionType::builder(FAILED_NOTICE_ACTIONS)
        .actions(vec![NoticeAction::builder(
            SEE_LOGS_ACTION,
            sh.lang.words().see_logs,
        )
        .build()])
        .build()]);
    let handle = app.clone();
    let _ = notices.on_action(move |performed| {
        let notice = performed.notification().map(|n| n.id());
        notice_action(&handle, performed.action_id(), notice);
    });
    let entries = tray::entries(sh.lang, sh.server_side(), &ServerStatus::Starting, false);
    let mut tray = TrayIconBuilder::with_id(TRAY_ID)
        .menu(&build_tray_menu(app, &entries)?)
        .tooltip(sh.lang.words().brand)
        .on_menu_event(|app, event| on_tray_event(app, event.id().as_ref()));
    if let Some(icon) = app.default_window_icon() {
        *sh.plain_icon.lock().unwrap() = Some((icon.rgba().to_vec(), icon.width(), icon.height()));
        tray = tray.icon(icon.clone());
    }
    // Windows: a left click opens the window (the menu is on the right
    // button), as Windows tray icons do
    #[cfg(windows)]
    {
        use tauri::tray::{MouseButton, MouseButtonState, TrayIconEvent};
        tray = tray
            .show_menu_on_left_click(false)
            .on_tray_icon_event(|tray, event| {
                if let TrayIconEvent::Click {
                    button: MouseButton::Left,
                    button_state: MouseButtonState::Up,
                    ..
                } = event
                {
                    focus_main(tray.app_handle());
                }
            });
    }
    tray.build(app)?;

    // macOS: after an update or a reinstall the server's login item is still the old
    // build's, which launchd refuses to start (agent::register_for_this_build); this
    // build registers it. Not while paused: registering starts the server.
    #[cfg(target_os = "macos")]
    if matches!(sh.mode(), Some(AppMode::Server { .. })) && !agent::paused_flag() {
        let handle = app.clone();
        std::thread::spawn(move || {
            if let Err(e) = agent::register_for_this_build() {
                let sh = shell(&handle);
                show_error(&handle, sh.lang.words().server_start_failed, &[&e]);
            }
        });
    }

    if let Target::Remote(url) = sh.target() {
        if let Err(e) = navigate_main(app, &url) {
            let sentence = lang::fill(sh.lang.words().open_failed, &[("url", &url)]);
            show_error(app, &sentence, &[&e]);
        }
    }

    // Windows: the server runs whenever this PC is set up and not paused.
    // The sign-in task starts it, and its watchdog trigger starts an agent
    // that died; one ended by hand (Task Scheduler's End) also starts again
    // when the window opens, as does one an install left stopped (Resume ends
    // the install hold). A task of the old shared name becomes this user's.
    // The Run value starts the app in its tray at sign-in (run_entry.rs).
    // (Off the main thread: schtasks takes a moment.)
    #[cfg(windows)]
    {
        let handle = app.clone();
        std::thread::spawn(move || {
            let sh = shell(&handle);
            if !matches!(sh.mode(), Some(AppMode::Server { .. })) {
                return;
            }
            if let Err(e) = agent::replace_old_task() {
                show_error(&handle, sh.lang.words().task_move_failed, &[&e]);
            }
            // the tray at sign-in: the Run value, for a PC set up before it (or an
            // install that moved); written once, never back after the user removed it
            if let Err(e) = agent::sync_autostart() {
                attempts::log(&format!("the tray at sign-in: {e}"));
            }
            if !sh.paused.load(Ordering::SeqCst)
                && agent::agent_alive() == Some(false)
                && agent::login_item() == LoginItem::Enabled
            {
                sh.mark_started();
                if let Err(e) = agent::control(Action::Resume) {
                    show_error(&handle, sh.lang.words().server_start_failed, &[&e]);
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
            // Some(older) once the runtime is known to hold this app's payload
            // (or a newer one); None when that could not be told, and then the
            // autostart entry is left as it is
            // the server's own address: whether its automatic updates are on (an updater
            // then switches it to a newer payload, not this app)
            let web_url = match sh.mode() {
                Some(AppMode::Server { web_url, .. }) => Some(web_url),
                _ => None,
            };
            let known: Option<bool> = match linux::sync_runtime(web_url.as_deref()) {
                Ok(Synced::Switched) => {
                    sh.restart_grace();
                    Some(false)
                }
                Ok(Synced::Current) => Some(false),
                Ok(Synced::Older {
                    app,
                    server,
                    running,
                }) => {
                    // the server updates its own runtime (Epic 35, owner decision D3):
                    // this app catches up with exactly the release the server runs,
                    // offered in the tray (never the channel's newest, which can be
                    // newer than the server)
                    eprintln!("maktaba: the server ran {server}, newer than this app ({app})");
                    let release = autoinstall::catch_up(&app, &running, &server);
                    sh.update_ready.store(release.is_some(), Ordering::SeqCst);
                    *sh.catch_up.lock().unwrap() = release;
                    Some(true)
                }
                Err(e) => {
                    show_error(&handle, sh.lang.words().switch_failed, &[&e]);
                    None
                }
            };
            if !sh.paused.load(Ordering::SeqCst)
                && agent::agent_alive() == Some(false)
                && agent::login_item() != LoginItem::NotRegistered
            {
                sh.mark_started();
                if let Err(e) = agent::control(Action::Resume) {
                    show_error(&handle, sh.lang.words().server_start_failed, &[&e]);
                }
            }
            if let Some(older) = known {
                if linux::tray_available() {
                    let _ = linux::sync_autostart(older);
                }
            }
        });
    }

    // the update attempt this app's last run ended with its restart (or left when it
    // quit): installed when the app now is its release (and, for an app that runs the
    // server, once its server is back on it), failed otherwise (attempts.rs)
    let handle = app.clone();
    std::thread::spawn(move || {
        let Some(rec) = attempts_path() else {
            return;
        };
        let sh = shell(&handle);
        let server = match sh.mode() {
            Some(AppMode::Server {
                web_url,
                supervisor_url,
            }) if !sh.paused.load(Ordering::SeqCst) => Some((web_url, supervisor_url)),
            _ => None,
        };
        let mut back = |a: &attempts::Attempt| -> Result<(), Failure> {
            let Some((web_url, supervisor_url)) = server.as_ref() else {
                return Ok(());
            };
            // macOS: the login item is bound to the build that registered it (an app signed
            // ad hoc is its code hash): this build's is made at once when another build's is
            // there (agent::register_login_item, as the app's start does), and once more if
            // the server stays silent LOGIN_ITEM_RECHECK into the wait; what macOS says then
            // is the failure
            #[cfg(target_os = "macos")]
            let login: std::cell::RefCell<(bool, Option<Failure>)> =
                std::cell::RefCell::new((false, register_again(&a.to, false).err()));
            #[cfg(target_os = "macos")]
            if let Some(f) = login.borrow().1.clone() {
                return Err(f);
            }
            let began = std::time::Instant::now();
            #[cfg_attr(not(target_os = "macos"), allow(unused_mut))]
            let mut silent_since: Option<Instant> = None;
            let res = attempts::wait_for_server(
                &a.to,
                || {
                    let seen = server_seen(web_url, supervisor_url);
                    #[cfg(target_os = "macos")]
                    {
                        let since = *silent_since.get_or_insert_with(Instant::now);
                        if seen != attempts::Seen::Silent {
                            silent_since = None;
                        } else if since.elapsed() >= LOGIN_ITEM_RECHECK && !login.borrow().0 {
                            let out = register_again(&a.to, true).err();
                            *login.borrow_mut() = (true, out);
                        }
                    }
                    #[cfg(not(target_os = "macos"))]
                    let _ = &mut silent_since;
                    seen
                },
                || began.elapsed(),
                std::thread::sleep,
            );
            #[cfg(target_os = "macos")]
            if let (Err(_), Some(f)) = (&res, login.borrow().1.clone()) {
                return Err(f);
            }
            res.map_err(|last| server_not_back(&a.to, &last))
        };
        let back: Option<attempts::ServerBack<'_>> = if server.is_some() {
            Some(&mut back)
        } else {
            None
        };
        if let Some(failed) =
            attempts::settle_newest(&rec, &own_version(&handle), &attempts::Ctx::here(), back)
        {
            tell_the_order(&failed);
            notify_failed(&handle, &failed.to, &failed.code);
            refresh(&handle);
        }
    });

    let handle = app.clone();
    std::thread::spawn(move || loop {
        refresh(&handle);
        #[cfg(target_os = "linux")]
        recheck_catch_up(&handle);
        std::thread::sleep(POLL_EVERY);
    });

    // an app that runs the server installs what its home orders (owner decision D1),
    // not what its own feed offers: its own check is a client's (and the Linux app's when
    // older than the server, set above)
    let handle = app.clone();
    std::thread::spawn(move || loop {
        // a locally built app can verify no release: it offers none
        if !shell(&handle).server_side() && !attempts::local_build() {
            let updater = handle.updater_builder().timeout(DOWNLOAD_TIMEOUT).build();
            if let Ok(updater) = updater {
                if let Ok(Some(u)) = tauri::async_runtime::block_on(updater.check()) {
                    client_update_found(&handle, &u.version);
                }
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

/// The main window's handle, the owner of a consent prompt a step raises
/// (Windows; setup::OWNER_WINDOW_ENV). None elsewhere, or with no window.
#[cfg(windows)]
fn owner_window(app: &AppHandle) -> Option<isize> {
    let hwnd = app.get_webview_window("main")?.hwnd().ok()?;
    Some(hwnd.0 as isize)
}

#[cfg(not(windows))]
fn owner_window(_: &AppHandle) -> Option<isize> {
    None
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
        params: None,
        details: Vec::new(),
    })?;
    let owner = setup::owner_env(owner_window(&app));
    tauri::async_runtime::spawn_blocking(move || {
        let send = |p: Progress| {
            let _ = on_progress.send(p);
        };
        // Linux: status and check from this app's payload; a check that
        // passed installs the runtime, and every later step runs from its
        // copy (the venv must not be built against this app's AppImage mount
        // or package files)
        #[cfg(target_os = "linux")]
        let (bin, mut envs) = agent::linux::setup_server(&step, &bin, send)?;
        #[cfg(not(target_os = "linux"))]
        let mut envs: Vec<(String, String)> = Vec::new();
        // the owner of a consent prompt the step raises (Windows)
        envs.extend(owner);
        let result = setup::run_step(&bin, &envs, &step, &input, send)?;
        #[cfg(target_os = "linux")]
        agent::linux::after_step(&step, &result, send)?;
        Ok(result)
    })
    .await
    .unwrap_or_else(|e| {
        Err(StepError {
            code: "internal".into(),
            message: e.to_string(),
            field: None,
            params: None,
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
pub fn open_login_items(app: AppHandle) {
    // Windows: this turns the task on and starts the server, a Resume
    #[cfg(windows)]
    shell(&app).mark_started();
    #[cfg(not(windows))]
    let _ = app;
    agent::open_login_items_settings();
}

#[tauri::command(async)]
pub fn server_status(app: AppHandle) -> StatusView {
    shell(&app).status.lock().unwrap().clone()
}

/// What open_local_server says while the server's processes run but its web app does not
/// answer yet (the starting screen keeps waiting on it, then says so with a retry).
const PAGE_NOT_ANSWERING: &str = "page_not_answering";

/// The starting screen calls this once the server is running: the window opens the local
/// web app once its page and its api answer (status::web_answers); until then it stays on
/// the starting screen, never on a page that cannot load.
#[tauri::command(async)]
pub fn open_local_server(app: AppHandle) -> Result<(), String> {
    match shell(&app).mode() {
        Some(AppMode::Server { web_url, .. }) => {
            if !status::web_answers(&web_url) {
                return Err(format!(
                    "{PAGE_NOT_ANSWERING}: the web app at {web_url} does not answer yet"
                ));
            }
            navigate_main(&app, &web_url)
        }
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
