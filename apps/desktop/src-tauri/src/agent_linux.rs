// The server on Linux (Epic 39 story 39.8, specs/epics/39-linux-app/
// contract-runtime.md).
//
// The server never runs from this app's files: an AppImage is mounted at a new
// /tmp/.mount_* path on every run, and a .deb/.rpm upgrade replaces
// <prefix>/share/maktaba/server under a running server. It runs from a copy
// in ~/.local/share/maktaba/runtime/<version> (`current`), as a systemd user
// unit started at boot by linger. The app never writes the unit or the copy
// itself: it runs `maktaba-server runtime …` from its payload and
// `maktaba-server service …` from the copy the unit runs (`--json`, one
// result or error line), the same commands the terminal setup of a machine
// without a desktop uses. Setup's `status` and `check` run from the payload;
// a `check` that passes installs the copy, and the steps after it run from
// the copy too, so the venv is built against runtime/current/python, which
// outlives this app's mount. A desktop app that starts records itself as the
// runtime's owner: one updater, the app's.
//
// An app older than the newest Maktaba this machine ran never installs its
// payload (the server's database may be migrated past it): the newer runtime
// keeps running, and the user is told.
//
// Where no StatusNotifier host is running (GNOME without the AppIndicator
// extension), there is no tray to return to: closing the window quits the
// app (the server keeps running), the window's menu carries the server's
// items, and there is no tray autostart entry.
#![cfg_attr(not(target_os = "linux"), allow(dead_code))]

use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::Mutex;

use serde::Deserialize;
use serde_json::Value;

use super::{Action, BundlePaths, LoginItem};
use crate::lang::{fill, Lang};
use crate::setup::{self, Progress, StepError};

/// `maktaba-server service status --json` (service.AppStatus in
/// cmd/maktaba-server/internal/service/appunit.go).
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct UnitStatus {
    #[serde(default)]
    pub installed: bool,
    /// another install's maktaba-server.service holds the name: what it runs
    #[serde(default)]
    pub taken: String,
    #[serde(default)]
    pub enabled: bool,
    #[serde(default)]
    pub active: String,
    #[serde(default)]
    pub linger: bool,
    #[serde(default)]
    pub manager: String,
    #[serde(default)]
    pub reason: String,
}

impl UnitStatus {
    /// The unit runs, or is on its way up.
    pub fn running(&self) -> bool {
        matches!(self.active.as_str(), "active" | "activating" | "reloading")
    }
}

/// The login item as the setup screens and the tray see it. Installed but
/// disabled, or without linger (it would run only while the user is logged
/// in), is "requires approval": the setup's button turns both on.
pub fn login_item_of(st: &UnitStatus) -> LoginItem {
    if st.manager != "ok" {
        return LoginItem::Unsupported;
    }
    if !st.installed {
        return LoginItem::NotRegistered;
    }
    if !st.enabled || !st.linger {
        return LoginItem::RequiresApproval;
    }
    LoginItem::Enabled
}

/// $XDG_DATA_HOME/maktaba (maktaba-server's data directory on Linux).
pub fn data_dir(getenv: impl Fn(&str) -> Option<String>, home: &Path) -> PathBuf {
    xdg(&getenv, "XDG_DATA_HOME", home, &[".local", "share"]).join("maktaba")
}

/// The server's unit file, in the user's systemd folder: $XDG_CONFIG_HOME/systemd/user, or
/// ~/.config/systemd/user (where `maktaba-server service install` writes it).
const UNIT_FILE: &str = "maktaba-server.service";

/// The value of `name` the server's unit was written with: `service install` pins the XDG
/// directories in its Environment= lines, so the server's state folder (where the home's
/// order and the attempts are) is where that environment says, not where this app's own
/// environment would put it. None when there is no unit, or it pins none.
pub fn unit_env(home: &Path, name: &str) -> Option<String> {
    unit_env_with(env, home, name)
}

/// unit_env with the environment `getenv`: the unit in $XDG_CONFIG_HOME/systemd/user first
/// (when it is set and absolute), else in ~/.config/systemd/user.
pub fn unit_env_with(
    getenv: impl Fn(&str) -> Option<String>,
    home: &Path,
    name: &str,
) -> Option<String> {
    let mut dirs = Vec::new();
    if let Some(v) = getenv("XDG_CONFIG_HOME").filter(|v| Path::new(v).is_absolute()) {
        dirs.push(PathBuf::from(v));
    }
    dirs.push(home.join(".config"));
    dirs.iter()
        .find_map(|d| std::fs::read_to_string(d.join("systemd").join("user").join(UNIT_FILE)).ok())
        .and_then(|unit| unit_env_in(&unit, name))
}

/// unit_env of a unit's text: its `Environment="NAME=value"` lines (backslash and quote
/// escaped, % doubled, as service/appunit.go writes them), the last one that sets name.
pub fn unit_env_in(unit: &str, name: &str) -> Option<String> {
    let mut out = None;
    for line in unit.lines() {
        let Some(rest) = line.trim().strip_prefix("Environment=") else {
            continue;
        };
        let quoted = rest.strip_prefix('"').and_then(|r| r.strip_suffix('"'));
        let kv = match quoted {
            Some(q) => {
                let mut s = String::with_capacity(q.len());
                let mut chars = q.chars().peekable();
                while let Some(c) = chars.next() {
                    match c {
                        '\\' => s.extend(chars.next()),
                        '%' if chars.peek() == Some(&'%') => {
                            chars.next();
                            s.push('%');
                        }
                        c => s.push(c),
                    }
                }
                s
            }
            None => rest.to_string(),
        };
        if let Some(v) = kv.strip_prefix(name).and_then(|r| r.strip_prefix('=')) {
            out = Some(v.to_string());
        }
    }
    out
}

/// $XDG_STATE_HOME/maktaba/logs (maktaba-server's logs directory on Linux).
pub fn logs_dir(getenv: impl Fn(&str) -> Option<String>, home: &Path) -> PathBuf {
    xdg(&getenv, "XDG_STATE_HOME", home, &[".local", "state"])
        .join("maktaba")
        .join("logs")
}

fn xdg(getenv: &impl Fn(&str) -> Option<String>, var: &str, home: &Path, def: &[&str]) -> PathBuf {
    match getenv(var) {
        Some(v) if Path::new(&v).is_absolute() => PathBuf::from(v),
        _ => def.iter().fold(home.to_path_buf(), |p, d| p.join(d)),
    }
}

fn env(var: &str) -> Option<String> {
    std::env::var(var).ok().filter(|v| !v.is_empty())
}

fn home() -> Option<PathBuf> {
    env("HOME").map(PathBuf::from)
}

/// <data>/runtime/current: the copy the unit runs (unresolved, as the unit
/// and the launcher use it).
fn runtime_current() -> Option<PathBuf> {
    home().map(|h| data_dir(env, &h).join("runtime").join("current"))
}

/// This app's payload, when the build carries one.
fn payload() -> Option<BundlePaths> {
    std::env::current_exe()
        .ok()
        .and_then(|e| BundlePaths::from_linux_exe(&e))
        .filter(BundlePaths::payload_present)
}

/// The maktaba-server a command runs. `service …` runs from the runtime's
/// copy, the server the unit runs, so an app older than that copy never
/// rewrites its unit with an older template; this app's payload runs it only
/// before there is a runtime. Everything else (`runtime …` installs this
/// app's payload) runs from the payload, else from the runtime's copy (a
/// client-only build still finds a server installed by another).
fn server_for(command: &str) -> Result<PathBuf, String> {
    let runtime = runtime_current()
        .map(|c| c.join("bin").join("maktaba-server"))
        .filter(|p| p.is_file());
    let own = payload().map(|p| p.server_bin());
    let pick = if command == "service" {
        runtime.or(own)
    } else {
        own.or(runtime)
    };
    pick.ok_or_else(|| "this build of Maktaba does not include the server".into())
}

/// The result of one `--json` command: its last `result` line's data, or its
/// `error` line's message.
pub fn parse_result(stdout: &str) -> Result<Value, String> {
    let mut out = Err("the server gave no result".to_string());
    for line in stdout.lines() {
        let Ok(ev) = serde_json::from_str::<Value>(line.trim()) else {
            continue;
        };
        match ev.get("type").and_then(Value::as_str) {
            Some("result") => out = Ok(ev.get("data").cloned().unwrap_or(Value::Null)),
            Some("error") => {
                out = Err(ev
                    .get("message")
                    .and_then(Value::as_str)
                    .unwrap_or("the server failed")
                    .to_string())
            }
            _ => {}
        }
    }
    out
}

fn run_json(args: &[&str]) -> Result<Value, String> {
    let bin = server_for(args.first().copied().unwrap_or_default())?;
    let out = Command::new(&bin)
        .args(args)
        .arg("--json")
        .output()
        .map_err(|e| format!("cannot run {}: {e}", bin.display()))?;
    let res = parse_result(&String::from_utf8_lossy(&out.stdout));
    if res.is_err() && out.stdout.is_empty() {
        let err = String::from_utf8_lossy(&out.stderr);
        return Err(format!(
            "{} {}: {}",
            bin.display(),
            args.join(" "),
            err.trim()
        ));
    }
    res
}

pub fn status() -> Result<UnitStatus, String> {
    serde_json::from_value(run_json(&["service", "status"])?).map_err(|e| e.to_string())
}

pub fn login_item() -> LoginItem {
    status()
        .map(|s| login_item_of(&s))
        .unwrap_or(LoginItem::Unsupported)
}

/// Make sure the runtime runs this app's payload (or a newer one), write and
/// start the unit, and ask for linger. Linger refused (no polkit agent)
/// leaves a server that runs while the user is logged in: login_item says
/// RequiresApproval, and setup offers it again.
pub fn register() -> Result<(), String> {
    ensure_runtime(|_| {}).map_err(|e| e.message)?;
    run_json(&["service", "install"])?;
    let _ = run_json(&["service", "linger"]);
    if tray_available() {
        let _ = sync_autostart(false);
    }
    Ok(())
}

/// Stop and remove the unit, and the tray's autostart entry. The runtime copy
/// and the data stay.
pub fn unregister() -> Result<(), String> {
    remove_autostart();
    run_json(&["service", "uninstall"]).map(|_| ())
}

/// Pause, resume or restart the unit. A unit stopped this way stays stopped
/// until Resume or the next boot.
pub fn control(action: Action) -> Result<(), String> {
    let verb = match action {
        Action::Pause => "stop",
        Action::Resume => "start",
        Action::Restart => "restart",
    };
    run_json(&["service", verb]).map(|_| ())
}

/// What "Open Login Items" means here: enable the unit and linger, so the
/// server starts at boot (polkit asks for a password where it must).
pub fn open_login_items_settings() {
    let _ = run_json(&["service", "install"]);
    let _ = run_json(&["service", "linger"]);
}

/// Whether the unit runs (None: no systemd user manager to ask).
pub fn agent_alive() -> Option<bool> {
    status()
        .ok()
        .filter(|s| s.manager == "ok" && s.installed)
        .map(|s| s.running())
}

/// `maktaba-server runtime status --from <payload> --json`: what runs, and
/// how this app's payload compares (#174: `update_pending` only for a newer
/// payload or the same version built again, `downgrade` for one older than
/// the newest version this machine ran, its watermark).
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct RuntimeStatus {
    /// who updates the runtime: "app" (this app's updater) or "headless" (the
    /// machine itself, from the signed releases: install.sh --app)
    #[serde(default)]
    pub owner: String,
    #[serde(default)]
    pub current: String,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub watermark: String,
    #[serde(default)]
    pub from_version: String,
    #[serde(default)]
    pub update_pending: bool,
    #[serde(default)]
    pub downgrade: bool,
}

/// What this app does with its payload, for a runtime status.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Plan {
    /// Newer than what runs, or nothing runs yet: install it and switch to it.
    Install,
    /// What runs already.
    Keep,
    /// Older than the newest version this machine ran (server): never
    /// installed; the newer runtime keeps running. running: the version the
    /// runtime runs now ("" when not known), which this app may catch up with.
    Older {
        app: String,
        server: String,
        running: String,
    },
}

/// Whether an updater orders this server's updates: it is a machine of a home (its
/// node.json, in the config folder the server's unit pins), or the server's own automatic
/// updates are on (its /api/cluster/self says `updates_auto` to this machine, at
/// `web_url`). Then a newer payload of this app (a newer AppImage, a package upgraded) is
/// kept, not switched to: the home's run installs that release as a new runtime, in its
/// window and after its checks (owner decision D3). A standalone server whose automatic
/// updates are off takes it as before: a machine that once ran an update, or follows an
/// apt mirror with updates off, still follows its package. A server that does not say (the
/// app's start often asks before the server's unit answers; an older server) is taken by
/// its last word here (AUTO_FILE), else by whether its updater ever ran a release here (its
/// journal in the updates folder).
pub fn home_manages(web_url: Option<&str>) -> bool {
    let Some(h) = home() else {
        return false;
    };
    let abs = |v: &String| Path::new(v).is_absolute();
    let config = unit_env(&h, "XDG_CONFIG_HOME")
        .filter(abs)
        .or_else(|| env("XDG_CONFIG_HOME").filter(abs))
        .map(PathBuf::from)
        .unwrap_or_else(|| h.join(".config"));
    let node = std::fs::read_to_string(config.join("maktaba").join("node.json")).ok();
    let updates = super::state_root().map(|r| r.join("updates"));
    let auto = match web_url.and_then(crate::status::updates_auto) {
        Some(on) => {
            if let Some(dir) = &updates {
                remember_auto(dir, on);
            }
            Some(on)
        }
        None => updates.as_deref().and_then(remembered_auto),
    };
    let journal = updates
        .as_deref()
        .is_some_and(|d| d.join("journal.json").is_file());
    managed_by_home(node.as_deref(), auto, journal)
}

/// The server's last word on its automatic updates (/api/cluster/self's `updates_auto`),
/// kept in the updates folder beside app-attempts.json: the app's next start goes by it
/// while the server does not answer yet.
const AUTO_FILE: &str = "app-updates-auto.json";

/// The last word kept (AUTO_FILE in `updates`); None when there is none, or it cannot be
/// read.
fn remembered_auto(updates: &Path) -> Option<bool> {
    let raw = std::fs::read(updates.join(AUTO_FILE)).ok()?;
    serde_json::from_slice::<Value>(&raw)
        .ok()?
        .get("updates_auto")?
        .as_bool()
}

/// Keeps the server's word (AUTO_FILE in `updates`), written when it changed. Best-effort:
/// without it the next start goes by the updater's journal.
fn remember_auto(updates: &Path, on: bool) {
    if remembered_auto(updates) == Some(on) {
        return;
    }
    let _ = std::fs::create_dir_all(updates);
    let _ = std::fs::write(
        updates.join(AUTO_FILE),
        serde_json::json!({ "updates_auto": on }).to_string(),
    );
}

/// home_manages of what the machine holds and says: a node.json of a joined machine (its
/// id and its home's address) is ordered by its home. Else the server's word on its own
/// automatic updates decides (`auto_updates`: said now, or the last word kept); a server
/// with no word is ordered by its updater when that updater ran a release here (its
/// `journal`).
pub fn managed_by_home(node_json: Option<&str>, auto_updates: Option<bool>, journal: bool) -> bool {
    let joined = node_json
        .and_then(|t| serde_json::from_str::<Value>(t).ok())
        .is_some_and(|v| {
            let set = |p: &str| {
                v.pointer(p)
                    .and_then(Value::as_str)
                    .is_some_and(|x| !x.trim().is_empty())
            };
            set("/node_id") && (set("/home/url") || set("/home/api_url"))
        });
    joined || auto_updates.unwrap_or(journal)
}

/// What to do with this app's payload (`home_orders`: an updater orders this server's
/// updates, home_manages).
pub fn plan(st: &RuntimeStatus, home_orders: bool) -> Plan {
    if st.downgrade {
        let server = if st.watermark.is_empty() {
            &st.version
        } else {
            &st.watermark
        };
        return Plan::Older {
            app: st.from_version.clone(),
            server: server.clone(),
            running: st.version.clone(),
        };
    }
    // the same release built again (another build of its payload: v0.3.0's Linux payload
    // tarball and its app's differed) is what runs already: the server switches releases
    // itself (owner decision D3), never this app behind the home's back
    let runs_it =
        !st.version.is_empty() && crate::attempts::at_least(&st.version, &st.from_version);
    // a newer payload, on a server a home manages: the home's run switches to it (a first
    // runtime, nothing running yet, is installed whoever manages it)
    let homes = home_orders && !st.version.is_empty();
    if st.update_pending && !runs_it && !homes {
        Plan::Install
    } else {
        Plan::Keep
    }
}

/// Whether this app is older than the release its server runs (Plan::Older's versions: the
/// app's, the newest the machine ran, what runs), asked without changing anything: the
/// tray's catch-up, asked again while the app runs (the server updates its own runtime).
/// None when it is not, or when this build carries no payload.
pub fn runtime_older() -> Result<Option<(String, String, String)>, String> {
    let Some(p) = payload() else {
        return Ok(None);
    };
    let from = p.server_dir.to_string_lossy().to_string();
    let st: RuntimeStatus = run_json(&["runtime", "status", "--from", &from])
        .and_then(|v| serde_json::from_value(v).map_err(|e| e.to_string()))?;
    Ok(match plan(&st, false) {
        Plan::Older {
            app,
            server,
            running,
        } => Some((app, server, running)),
        _ => None,
    })
}

/// The user's message for an app older than this machine's server, in the
/// system's UI language (lang.rs).
pub fn older_message(app: &str, server: &str) -> String {
    let words = Lang::system().words();
    fill(words.older_app, &[("app", app), ("server", server)])
}

/// What sync_runtime did.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Synced {
    /// This app's payload was installed and the unit switched to it.
    Switched,
    /// The runtime runs this payload already (or the build carries none).
    Current,
    /// This app is older than the server: nothing changed (Plan::Older).
    Older {
        app: String,
        server: String,
        running: String,
    },
}

/// Whether the runtime was found to hold this app's payload (or a newer one)
/// in this run: the check and the copy happen once.
static RUNTIME_READY: Mutex<bool> = Mutex::new(false);

/// At start (after an app update, or any start): when this app's payload is
/// newer than what the unit runs, install it and switch the unit to it (the
/// server stops while `current` moves; a paused, stopped unit stays stopped;
/// the supervisor's next start backs up, migrates and rebuilds the venv as
/// the payload needs), unless an updater orders this server's updates
/// (home_manages, asked at `web_url`: the server's own address). An older app
/// changes nothing.
pub fn sync_runtime(web_url: Option<&str>) -> Result<Synced, String> {
    if payload().is_none() {
        return Ok(Synced::Current); // a client-only build carries none
    }
    sync_with(|_| {}, web_url).map_err(|e| e.message)
}

/// Before a setup step (and the service's registration): the runtime holds
/// this app's payload as `current`, installed now when it is newer or the
/// first. Progress lines of the copy go to on_progress. An older app's setup
/// stops here with `downgrade`.
pub fn ensure_runtime(on_progress: impl FnMut(Progress)) -> Result<(), StepError> {
    // a setup step: no server answers yet to say whether its updates are automatic
    match sync_with(on_progress, None)? {
        Synced::Older { app, server, .. } => {
            Err(StepError::new("downgrade", older_message(&app, &server)))
        }
        Synced::Switched | Synced::Current => Ok(()),
    }
}

fn sync_with(
    on_progress: impl FnMut(Progress),
    web_url: Option<&str>,
) -> Result<Synced, StepError> {
    let mut ready = RUNTIME_READY.lock().unwrap_or_else(|e| e.into_inner());
    if *ready {
        return Ok(Synced::Current);
    }
    let p = payload().ok_or_else(|| {
        StepError::new(
            "server_missing",
            "This build of Maktaba does not include the server.",
        )
    })?;
    let from = p.server_dir.to_string_lossy().to_string();
    let st: RuntimeStatus = run_json(&["runtime", "status", "--from", &from])
        .and_then(|v| serde_json::from_value(v).map_err(|e| e.to_string()))
        .map_err(|e| StepError::new("runtime", e))?;
    let home_orders = home_manages(web_url);
    if home_orders
        && st.update_pending
        && !st.version.is_empty()
        && !crate::attempts::at_least(&st.version, &st.from_version)
    {
        crate::attempts::log(&format!(
            "this app's server payload ({}) is newer than the release the server runs ({}): \
             a home, or this server's own automatic updates, switch to it",
            st.from_version, st.version
        ));
    }
    let synced = match plan(&st, home_orders) {
        Plan::Older {
            app,
            server,
            running,
        } => {
            return Ok(Synced::Older {
                app,
                server,
                running,
            })
        }
        Plan::Keep => {
            // this payload runs already; a machine set up by install.sh --app
            // (owner headless) that this app now manages gets one updater, the
            // app's: an install of the running payload copies nothing and only
            // records the owner
            if st.owner != "app" {
                setup::run_ndjson(
                    &p.server_bin(),
                    &[
                        "runtime", "install", "--json", "--from", &from, "--owner", "app",
                    ],
                    &[],
                    None,
                    on_progress,
                    "maktaba-server runtime install --owner app",
                )?;
            }
            Synced::Current
        }
        Plan::Install => {
            setup::run_ndjson(
                &p.server_bin(),
                &[
                    "runtime",
                    "install",
                    "--json",
                    "--from",
                    &from,
                    "--activate",
                    "--owner",
                    "app",
                ],
                &[],
                None,
                on_progress,
                "maktaba-server runtime install",
            )?;
            Synced::Switched
        }
    };
    *ready = true;
    Ok(synced)
}

/// The steps that run from this app's payload: they only read, and come
/// before anything is copied (a computer `check` refuses never waits for a
/// copy it cannot use).
pub fn reads_only(step: &str) -> bool {
    matches!(step, "status" | "check")
}

/// The maktaba-server a setup step runs, and its environment. `status` (asked
/// at every launch) and `check` run from this app's payload, as an app
/// payload (MAKTABA_APP_BUNDLE: the bundled Postgres is what will run); a
/// `check` that says ok then installs the runtime (after_step). Every later
/// step runs from the runtime's copy, with MAKTABA_APP_BUNDLE the unresolved
/// runtime/current: the venv is then built against runtime/current/python,
/// not this app's mount. A dev build (no payload, MAKTABA_SERVER_BIN) runs
/// `bin` as it is.
pub fn setup_server(
    step: &str,
    bin: &Path,
    on_progress: impl FnMut(Progress),
) -> Result<(PathBuf, Vec<(String, String)>), StepError> {
    let Some(p) = payload() else {
        return Ok((bin.to_path_buf(), Vec::new()));
    };
    if reads_only(step) {
        return Ok((
            bin.to_path_buf(),
            vec![(
                "MAKTABA_APP_BUNDLE".to_string(),
                p.server_dir.to_string_lossy().to_string(),
            )],
        ));
    }
    ensure_runtime(on_progress)?;
    let current = runtime_current().ok_or_else(|| StepError::new("internal", "HOME is not set"))?;
    Ok((
        current.join("bin").join("maktaba-server"),
        vec![(
            "MAKTABA_APP_BUNDLE".to_string(),
            current.to_string_lossy().to_string(),
        )],
    ))
}

/// Whether a step's result is a `check` that says this computer can run the
/// server: the runtime is installed right then.
pub fn copies_after(step: &str, result: &Value) -> bool {
    step == "check" && result.get("ok").and_then(Value::as_bool) == Some(true)
}

/// After a setup step: a `check` that passed installs the runtime (its copy's
/// progress goes to on_progress, while the screen says it is checking), so
/// the steps after it run from the copy.
pub fn after_step(
    step: &str,
    result: &Value,
    on_progress: impl FnMut(Progress),
) -> Result<(), StepError> {
    if payload().is_some() && copies_after(step, result) {
        ensure_runtime(on_progress)?;
    }
    Ok(())
}

/// Is a StatusNotifier host (a tray) running in this session? Asked through
/// D-Bus with gdbus, else dbus-send; with neither, assumed so.
pub fn tray_available() -> bool {
    let gdbus = Command::new("gdbus")
        .args([
            "call",
            "--session",
            "--dest",
            "org.freedesktop.DBus",
            "--object-path",
            "/org/freedesktop/DBus",
            "--method",
            "org.freedesktop.DBus.NameHasOwner",
            "org.kde.StatusNotifierWatcher",
        ])
        .output();
    if let Ok(out) = gdbus {
        if out.status.success() {
            return name_has_owner(&String::from_utf8_lossy(&out.stdout)).unwrap_or(true);
        }
    }
    let dbus_send = Command::new("dbus-send")
        .args([
            "--session",
            "--print-reply",
            "--dest=org.freedesktop.DBus",
            "/org/freedesktop/DBus",
            "org.freedesktop.DBus.NameHasOwner",
            "string:org.kde.StatusNotifierWatcher",
        ])
        .output();
    match dbus_send {
        Ok(out) if out.status.success() => {
            name_has_owner(&String::from_utf8_lossy(&out.stdout)).unwrap_or(true)
        }
        _ => true,
    }
}

/// NameHasOwner's answer from gdbus ("(true,)") or dbus-send ("boolean true").
pub fn name_has_owner(out: &str) -> Option<bool> {
    let o = out.trim();
    if o.contains("true") {
        Some(true)
    } else if o.contains("false") {
        Some(false)
    } else {
        None
    }
}

/// The argv[1] that starts the app with its window hidden (the tray at login).
pub const HIDDEN_FLAG: &str = "--hidden";

/// The key that marks an autostart entry as this app's.
const AUTOSTART_MARK: &str = "X-Maktaba-Tray=true";

/// The tray's autostart entry (the XDG autostart spec).
fn autostart_path() -> Option<PathBuf> {
    let base = env("XDG_CONFIG_HOME")
        .filter(|v| Path::new(v).is_absolute())
        .map(PathBuf::from)
        .or_else(|| home().map(|h| h.join(".config")))?;
    Some(base.join("autostart").join("maktaba.desktop"))
}

/// <state>/state/autostart-written: the entry was written once. Without the
/// entry, it means the user removed it, and it stays removed.
fn autostart_written_path() -> Option<PathBuf> {
    super::state_root().map(|r| r.join("state").join("autostart-written"))
}

/// The program the entry starts: the AppImage file ($APPIMAGE, set by its
/// runtime; the mount point changes every run), else this executable.
fn app_program() -> Option<PathBuf> {
    env("APPIMAGE")
        .map(PathBuf::from)
        .or_else(|| std::env::current_exe().ok())
}

/// The entry's text. TryExec makes the desktop skip it once the program is
/// gone (the package removed, the AppImage deleted). `enabled` false keeps a
/// user's "off" (GNOME's X-GNOME-Autostart-enabled, KDE's Hidden).
pub fn autostart_entry(program: &Path, enabled: bool) -> String {
    let p = program.to_string_lossy();
    let mut e = format!(
        "[Desktop Entry]\nType=Application\nName=Maktaba\nName[ar]=مكتبة\n\
         Comment=The Maktaba server's status in the tray\n\
         Comment[ar]=حالة خادم مكتبة في شريط النظام\n\
         TryExec={}\nExec={} {HIDDEN_FLAG}\nTerminal=false\nNoDisplay=true\n\
         X-GNOME-Autostart-enabled={enabled}\n{AUTOSTART_MARK}\n",
        desktop_string(&p),
        exec_value(&p)
    );
    if !enabled {
        e.push_str("Hidden=true\n");
    }
    e
}

/// A desktop entry string value: `\`, newline, tab and CR escaped.
pub fn desktop_string(s: &str) -> String {
    s.replace('\\', "\\\\")
        .replace('\n', "\\n")
        .replace('\t', "\\t")
        .replace('\r', "\\r")
}

/// A word of a desktop entry's Exec key as it is written in the file. The
/// Desktop Entry spec reads the value as a string first (its `\` escapes)
/// and then splits it by its quoting rules, so the quoted word (exec_quote)
/// is string-escaped on top: a literal `\` in a quoted path is `\\\\`, a `$`
/// is `\\$`.
pub fn exec_value(word: &str) -> String {
    desktop_string(&exec_quote(word))
}

/// A word of a desktop entry's Exec key, by its quoting rules alone: quoted
/// when it holds a reserved character, with `"`, `` ` ``, `$` and `\` escaped
/// inside the quotes, and `%` doubled (a field code otherwise).
pub fn exec_quote(word: &str) -> String {
    let word = word.replace('%', "%%");
    if !word.is_empty()
        && !word
            .chars()
            .any(|c| c.is_whitespace() || "\"'\\`$<>~|&;*?#()".contains(c))
    {
        return word;
    }
    let mut out = String::from("\"");
    for c in word.chars() {
        if matches!(c, '"' | '`' | '$' | '\\') {
            out.push('\\');
        }
        out.push(c);
    }
    out.push('"');
    out
}

/// What to do with the autostart entry.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Autostart {
    /// Write it (with the user's on/off kept).
    Write { enabled: bool },
    /// Leave it: the user removed it, it is not ours, it starts this program
    /// already, or this app is older than the server.
    Leave,
}

/// The entry is written once; after that it is only rewritten when it is
/// this app's and starts another program (the AppImage moved, a newer app),
/// never for an app older than the server, and never back after the user
/// removed it.
pub fn autostart_decision(
    existing: Option<&str>,
    written_before: bool,
    program: &Path,
    older: bool,
) -> Autostart {
    if older {
        return Autostart::Leave;
    }
    let Some(e) = existing else {
        return if written_before {
            Autostart::Leave
        } else {
            Autostart::Write { enabled: true }
        };
    };
    let lines: Vec<&str> = e.lines().map(str::trim).collect();
    if !lines.contains(&AUTOSTART_MARK) {
        return Autostart::Leave;
    }
    let exec = format!(
        "Exec={} {HIDDEN_FLAG}",
        exec_value(&program.to_string_lossy())
    );
    if lines.contains(&exec.as_str()) {
        return Autostart::Leave;
    }
    let off = lines.contains(&"X-GNOME-Autostart-enabled=false") || lines.contains(&"Hidden=true");
    Autostart::Write { enabled: !off }
}

/// Write the tray's autostart entry when autostart_decision says so.
pub fn sync_autostart(older: bool) -> Result<(), String> {
    let path = autostart_path().ok_or("HOME is not set")?;
    let program = app_program().ok_or("cannot find this program")?;
    let marker = autostart_written_path();
    let existing = std::fs::read_to_string(&path).ok();
    let written_before = marker.as_ref().is_some_and(|m| m.is_file());
    let decision = autostart_decision(existing.as_deref(), written_before, &program, older);
    if let Autostart::Write { enabled } = decision {
        write_file_atomic(&path, &autostart_entry(&program, enabled))?;
    }
    // ours and present (written now or before this marker existed): its
    // removal from now on is the user's
    let ours = decision != Autostart::Leave
        || existing.is_some_and(|e| e.lines().any(|l| l.trim() == AUTOSTART_MARK));
    if let (true, Some(m)) = (ours, marker) {
        if !written_before {
            write_file_atomic(
                &m,
                "written by the Maktaba app; remove the entry to keep it off\n",
            )?;
        }
    }
    Ok(())
}

/// Write a file through a temporary file renamed over it.
fn write_file_atomic(path: &Path, text: &str) -> Result<(), String> {
    let dir = path.parent().ok_or("no parent directory")?;
    std::fs::create_dir_all(dir).map_err(|e| format!("{}: {e}", dir.display()))?;
    let tmp = path.with_extension("tmp");
    std::fs::write(&tmp, text).map_err(|e| format!("{}: {e}", tmp.display()))?;
    std::fs::rename(&tmp, path).map_err(|e| format!("{}: {e}", path.display()))
}

/// Remove the entry (this computer no longer runs the server); a later server
/// setup writes it again.
pub fn remove_autostart() {
    if let Some(p) = autostart_path() {
        if std::fs::read_to_string(&p).is_ok_and(|e| e.lines().any(|l| l.trim() == AUTOSTART_MARK))
        {
            let _ = std::fs::remove_file(p);
        }
    }
    if let Some(m) = autostart_written_path() {
        let _ = std::fs::remove_file(m);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn st(installed: bool, enabled: bool, linger: bool, manager: &str) -> UnitStatus {
        UnitStatus {
            installed,
            enabled,
            linger,
            manager: manager.into(),
            active: "active".into(),
            ..Default::default()
        }
    }

    #[test]
    fn login_item_from_the_unit() {
        assert_eq!(
            login_item_of(&st(true, true, true, "ok")),
            LoginItem::Enabled
        );
        assert_eq!(
            login_item_of(&st(true, true, false, "ok")),
            LoginItem::RequiresApproval
        );
        assert_eq!(
            login_item_of(&st(true, false, true, "ok")),
            LoginItem::RequiresApproval
        );
        assert_eq!(
            login_item_of(&st(false, false, true, "ok")),
            LoginItem::NotRegistered
        );
        assert_eq!(
            login_item_of(&st(true, true, true, "unsupported")),
            LoginItem::Unsupported
        );
    }

    #[test]
    fn unit_status_reads_the_service_json() {
        let v: UnitStatus = serde_json::from_str(
            r#"{"unit":"/u","installed":true,"enabled":true,"active":"activating","sub_state":"start",
               "linger":false,"manager":"ok"}"#,
        )
        .unwrap();
        assert!(v.installed && v.enabled && !v.linger && v.running());
        let stopped: UnitStatus =
            serde_json::from_str(r#"{"active":"inactive","manager":"ok"}"#).unwrap();
        assert!(!stopped.running());
    }

    #[test]
    fn json_results_and_errors() {
        let out = "{\"type\":\"progress\",\"stage\":\"copy\",\"done\":1,\"total\":2}\n\
                   {\"type\":\"result\",\"data\":{\"activated\":true}}\n";
        assert_eq!(parse_result(out).unwrap()["activated"], true);
        assert_eq!(
            parse_result(
                "{\"type\":\"error\",\"code\":\"service_taken\",\"message\":\"taken by /x\"}\n"
            )
            .unwrap_err(),
            "taken by /x"
        );
        assert!(parse_result("not json\n").is_err());
    }

    // `runtime status --json` as #174 prints it
    fn rt(json: &str) -> RuntimeStatus {
        serde_json::from_str(json).unwrap()
    }

    // the plan of a server no home manages
    fn plan_free(st: &RuntimeStatus) -> Plan {
        plan(st, false)
    }

    /// A server a home manages keeps its runtime when this app is newer: the home's run
    /// switches it (D3). Its first runtime, an older app and the same payload are as before.
    #[test]
    fn a_home_orders_the_update_of_a_server_it_manages() {
        let newer = rt(
            r#"{"current":"v1.5.0_1a2b3c4d","version":"v1.5.0","watermark":"v1.5.0",
                           "from_version":"v1.6.0","update_pending":true,"downgrade":false}"#,
        );
        assert_eq!(plan(&newer, false), Plan::Install);
        assert_eq!(plan(&newer, true), Plan::Keep);
        let first = rt(
            r#"{"root":"/r","owner":"app","installed":[],"from_version":"v1.6.0",
                           "update_pending":true,"downgrade":false}"#,
        );
        assert_eq!(plan(&first, true), Plan::Install);
        let older = rt(
            r#"{"version":"v1.6.0","watermark":"v1.6.0","from_version":"v1.5.0","downgrade":true}"#,
        );
        assert!(matches!(plan(&older, true), Plan::Older { .. }));

        // what tells it: a joined machine's node.json, whatever its server says
        let joined = r#"{"node_id":"n1","name":"studio","private_key":"k","home":{"url":"http://192.168.50.177:8080"}}"#;
        assert!(managed_by_home(Some(joined), None, false));
        assert!(managed_by_home(Some(joined), Some(false), false));
        assert!(managed_by_home(Some(joined), Some(true), false));
        let older_key = r#"{"node_id":"n1","home":{"api_url":"http://home:8080"}}"#;
        assert!(managed_by_home(Some(older_key), None, false));
        assert!(!managed_by_home(
            Some(r#"{"node_id":"","home":{"url":"http://h"}}"#),
            None,
            false
        ));
        assert!(!managed_by_home(
            Some(r#"{"node_id":"n1","home":{}}"#),
            None,
            false
        ));
        assert!(!managed_by_home(Some("not json"), None, false));
        // a standalone server: its automatic updates on keep the payload, off take it,
        // whatever its updater ran before
        assert!(managed_by_home(None, Some(true), false));
        assert!(managed_by_home(None, Some(true), true));
        assert!(!managed_by_home(None, Some(false), false));
        assert!(!managed_by_home(None, Some(false), true));
        // one that does not say (not answering yet at the app's start, an older server):
        // its updater's journal of a release it ran keeps the payload; none takes it
        assert!(managed_by_home(None, None, true));
        assert!(!managed_by_home(None, None, false));
    }

    /// The server's last word on its automatic updates is kept for the next start, which
    /// often asks before the server answers; rewritten only when it changes.
    #[test]
    fn the_servers_last_word_on_its_updates_is_kept() {
        let dir = tempfile::tempdir().unwrap();
        let updates = dir.path().join("updates");
        assert_eq!(remembered_auto(&updates), None);
        remember_auto(&updates, true);
        assert_eq!(remembered_auto(&updates), Some(true));
        remember_auto(&updates, false);
        assert_eq!(remembered_auto(&updates), Some(false));
        std::fs::write(updates.join(AUTO_FILE), "not json").unwrap();
        assert_eq!(remembered_auto(&updates), None);
    }

    /// The server's unit is found in $XDG_CONFIG_HOME/systemd/user, else ~/.config's.
    #[test]
    fn the_unit_is_found_where_xdg_config_home_says() {
        let home = tempfile::tempdir().unwrap();
        let config = tempfile::tempdir().unwrap();
        let unit_dir = config.path().join("systemd").join("user");
        std::fs::create_dir_all(&unit_dir).unwrap();
        std::fs::write(
            unit_dir.join(UNIT_FILE),
            "[Service]\nEnvironment=\"XDG_DATA_HOME=/srv/data\"\n",
        )
        .unwrap();
        let cfg = config.path().to_string_lossy().to_string();
        let with = |k: &str| (k == "XDG_CONFIG_HOME").then(|| cfg.clone());
        assert_eq!(
            unit_env_with(with, home.path(), "XDG_DATA_HOME").as_deref(),
            Some("/srv/data")
        );
        // not there, and not set: ~/.config's
        assert_eq!(unit_env_with(|_| None, home.path(), "XDG_DATA_HOME"), None);
        let default_dir = home.path().join(".config").join("systemd").join("user");
        std::fs::create_dir_all(&default_dir).unwrap();
        std::fs::write(
            default_dir.join(UNIT_FILE),
            "[Service]\nEnvironment=\"XDG_DATA_HOME=/home/me/data\"\n",
        )
        .unwrap();
        assert_eq!(
            unit_env_with(|_| None, home.path(), "XDG_DATA_HOME").as_deref(),
            Some("/home/me/data")
        );
        // a relative XDG_CONFIG_HOME is not one
        assert_eq!(
            unit_env_with(
                |k: &str| (k == "XDG_CONFIG_HOME").then(|| "rel/config".to_string()),
                home.path(),
                "XDG_DATA_HOME"
            )
            .as_deref(),
            Some("/home/me/data")
        );
    }

    #[test]
    fn the_plan_follows_the_runtime_status() {
        // nothing installed yet: the first runtime
        assert_eq!(
            plan_free(&rt(
                r#"{"root":"/r","owner":"app","installed":[],"from":"v1.5.0_1a2b3c4d",
                       "from_version":"v1.5.0","update_pending":true,"downgrade":false}"#
            )),
            Plan::Install
        );
        // the same payload runs
        assert_eq!(
            plan_free(&rt(
                r#"{"current":"v1.5.0_1a2b3c4d","version":"v1.5.0","watermark":"v1.5.0",
                       "from":"v1.5.0_1a2b3c4d","from_version":"v1.5.0","update_pending":false,"downgrade":false}"#
            )),
            Plan::Keep
        );
        // an older app (an old AppImage opened again): never installed
        assert_eq!(
            plan_free(&rt(
                r#"{"current":"v1.5.0_1a2b3c4d","version":"v1.5.0","watermark":"v1.5.0",
                       "from_version":"v1.4.0","update_pending":false,"downgrade":true}"#
            )),
            Plan::Older {
                app: "v1.4.0".into(),
                server: "v1.5.0".into(),
                running: "v1.5.0".into()
            }
        );
        // the same release built again (another build of its payload): what runs already
        assert_eq!(
            plan_free(&rt(
                r#"{"current":"v1.5.0_1a2b3c4d","version":"v1.5.0","watermark":"v1.5.0",
                       "from":"v1.5.0_9f8e7d6c","from_version":"v1.5.0","update_pending":true,"downgrade":false}"#
            )),
            Plan::Keep
        );
        // a newer one: installed
        assert_eq!(
            plan_free(&rt(
                r#"{"current":"v1.5.0_1a2b3c4d","version":"v1.5.0","watermark":"v1.5.0",
                       "from_version":"v1.6.0","update_pending":true,"downgrade":false}"#
            )),
            Plan::Install
        );
        // the newest this machine ran is what an older app is compared with
        assert_eq!(
            plan_free(&rt(
                r#"{"version":"v1.3.0","watermark":"v1.5.0","from_version":"v1.4.0","downgrade":true}"#
            )),
            Plan::Older {
                app: "v1.4.0".into(),
                server: "v1.5.0".into(),
                running: "v1.3.0".into()
            }
        );
    }

    /// The unit's pinned environment, as service/appunit.go writes it: quoted, escaped, %
    /// doubled; the last line that sets a name wins; none without a unit line for it.
    #[test]
    fn the_unit_s_environment_is_read() {
        let unit = "[Service]\nEnvironment=\"XDG_CONFIG_HOME=/home/me/.config\"\n\
                    Environment=\"XDG_DATA_HOME=/srv/me/100%% \\\"data\\\"\"\n\
                    ExecStart=/x\n";
        assert_eq!(
            unit_env_in(unit, "XDG_DATA_HOME").as_deref(),
            Some("/srv/me/100% \"data\"")
        );
        assert_eq!(
            unit_env_in(unit, "XDG_CONFIG_HOME").as_deref(),
            Some("/home/me/.config")
        );
        assert_eq!(unit_env_in(unit, "XDG_STATE_HOME"), None);
        assert_eq!(unit_env_in(unit, "XDG_DATA"), None);
    }

    #[test]
    fn the_older_message_names_both_versions() {
        let m = older_message("v1.4.0", "v1.5.0");
        assert!(m.contains("v1.4.0") && m.contains("v1.5.0"), "{m}");
    }

    // XDG paths are absolute POSIX paths ("/state" is relative on Windows)
    #[cfg(unix)]
    #[test]
    fn xdg_dirs() {
        let none = |_: &str| None;
        assert_eq!(
            data_dir(none, Path::new("/home/me")),
            Path::new("/home/me/.local/share/maktaba")
        );
        assert_eq!(
            logs_dir(none, Path::new("/home/me")),
            Path::new("/home/me/.local/state/maktaba/logs")
        );
        let set = |k: &str| (k == "XDG_STATE_HOME").then(|| "/state".to_string());
        assert_eq!(
            logs_dir(set, Path::new("/h")),
            Path::new("/state/maktaba/logs")
        );
        // a relative XDG path is ignored, as the spec says
        let rel = |_: &str| Some("relative".to_string());
        assert_eq!(
            data_dir(rel, Path::new("/h")),
            Path::new("/h/.local/share/maktaba")
        );
    }

    #[test]
    fn name_has_owner_answers() {
        assert_eq!(name_has_owner("(true,)\n"), Some(true));
        assert_eq!(name_has_owner("(false,)"), Some(false));
        assert_eq!(
            name_has_owner("method return time=1 sender=x -> destination=y serial=3 reply_serial=2\n   boolean true\n"),
            Some(true)
        );
        assert_eq!(name_has_owner(""), None);
    }

    #[test]
    fn autostart_entry_quotes_the_program() {
        let e = autostart_entry(Path::new("/home/me/Applications/Maktaba.AppImage"), true);
        assert!(e.contains("Exec=/home/me/Applications/Maktaba.AppImage --hidden\n"));
        assert!(e.contains("TryExec=/home/me/Applications/Maktaba.AppImage\n"));
        assert!(e.starts_with("[Desktop Entry]\nType=Application\n"));
        assert!(e.contains("Comment[ar]=") && e.contains("Name[ar]=مكتبة\n"));
        assert!(e.contains("X-GNOME-Autostart-enabled=true\n") && !e.contains("Hidden=true"));
        let off = autostart_entry(Path::new("/opt/My Apps/M.AppImage"), false);
        assert!(off.contains("TryExec=/opt/My Apps/M.AppImage\n"));
        assert!(off.contains("Exec=\"/opt/My Apps/M.AppImage\" --hidden\n"));
        assert!(off.contains("X-GNOME-Autostart-enabled=false\n") && off.contains("Hidden=true\n"));
        assert_eq!(exec_quote("/home/my apps/M"), "\"/home/my apps/M\"");
        assert_eq!(exec_quote("/a$b"), "\"/a\\$b\"");
        assert_eq!(exec_quote("/100%"), "/100%%");
        assert_eq!(exec_quote("/x\"y"), "\"/x\\\"y\"");
        assert_eq!(desktop_string("/a\\b"), "/a\\\\b");
        // in the file: the quoting's escapes string-escaped again (the spec's
        // "\\\\" for a literal backslash, "\\$" for a dollar sign)
        assert_eq!(exec_value("/a$b"), "\"/a\\\\$b\"");
        assert_eq!(exec_value("/a\\b"), "\"/a\\\\\\\\b\"");
        assert_eq!(exec_value("/x\"y"), "\"/x\\\\\"y\"");
        assert_eq!(exec_value("/plain/M.AppImage"), "/plain/M.AppImage");
    }

    /// An Exec value read as the Desktop Entry spec reads it: the string's
    /// escapes first, then the quoting rules, then `%%`.
    fn read_exec(value: &str) -> Vec<String> {
        let mut s = String::new();
        let mut chars = value.chars();
        while let Some(c) = chars.next() {
            if c != '\\' {
                s.push(c);
                continue;
            }
            match chars.next() {
                Some('s') => s.push(' '),
                Some('n') => s.push('\n'),
                Some('t') => s.push('\t'),
                Some('r') => s.push('\r'),
                Some('\\') => s.push('\\'),
                Some(o) => {
                    s.push('\\');
                    s.push(o);
                }
                None => s.push('\\'),
            }
        }
        let (mut args, mut cur, mut quoted, mut any) = (Vec::new(), String::new(), false, false);
        let mut it = s.chars();
        while let Some(c) = it.next() {
            match c {
                '"' => {
                    quoted = !quoted;
                    any = true;
                }
                '\\' if quoted => cur.extend(it.next()),
                ' ' if !quoted => {
                    if any || !cur.is_empty() {
                        args.push(std::mem::take(&mut cur));
                    }
                    any = false;
                }
                _ => cur.push(c),
            }
        }
        if any || !cur.is_empty() {
            args.push(cur);
        }
        args.into_iter().map(|a| a.replace("%%", "%")).collect()
    }

    #[test]
    fn exec_values_read_back_as_the_program() {
        for path in [
            "/home/me/Maktaba.AppImage",
            "/opt/My Apps/M.AppImage",
            "/home/me/$HOME/a`b`/M",
            "/home/me/back\\slash/M",
            "/home/me/quote\"d/M",
            "/home/me/100% sure/M",
            "/home/me/tab\there/M",
            "/home/me/(x)&;|<>~*?#'/M",
        ] {
            let line = format!("{} {HIDDEN_FLAG}", exec_value(path));
            assert_eq!(
                read_exec(&line),
                vec![path.to_string(), HIDDEN_FLAG.to_string()],
                "{line}"
            );
        }
    }

    #[test]
    fn a_passed_check_copies_the_runtime() {
        let ok = serde_json::json!({"ok": true, "problems": []});
        let refused = serde_json::json!({"ok": false, "problems": [{"code": "glibc_old"}]});
        assert!(copies_after("check", &ok));
        assert!(!copies_after("check", &refused));
        assert!(!copies_after("admin", &ok));
        assert!(reads_only("status") && reads_only("check") && !reads_only("venv"));
    }

    #[test]
    fn the_runtime_status_names_its_owner() {
        assert_eq!(
            rt(r#"{"owner":"headless","current":"v1_1a"}"#).owner,
            "headless"
        );
        assert_eq!(rt(r#"{"current":"v1_1a"}"#).owner, "");
    }

    #[test]
    fn autostart_is_written_once_and_follows_only_its_own_program() {
        let app = Path::new("/home/me/Maktaba.AppImage");
        let ours = autostart_entry(app, true);
        // the first time: written
        assert_eq!(
            autostart_decision(None, false, app, false),
            Autostart::Write { enabled: true }
        );
        // the user removed it: it stays removed
        assert_eq!(autostart_decision(None, true, app, false), Autostart::Leave);
        // ours, this program: left as it is
        assert_eq!(
            autostart_decision(Some(&ours), true, app, false),
            Autostart::Leave
        );
        // ours, the AppImage moved: rewritten, the user's "off" kept
        let moved = Path::new("/home/me/Apps/Maktaba.AppImage");
        assert_eq!(
            autostart_decision(Some(&ours), true, moved, false),
            Autostart::Write { enabled: true }
        );
        let gnome_off = ours.replace(
            "X-GNOME-Autostart-enabled=true",
            "X-GNOME-Autostart-enabled=false",
        );
        assert_eq!(
            autostart_decision(Some(&gnome_off), true, moved, false),
            Autostart::Write { enabled: false }
        );
        let kde_off = format!("{ours}Hidden=true\n");
        assert_eq!(
            autostart_decision(Some(&kde_off), true, moved, false),
            Autostart::Write { enabled: false }
        );
        // an app older than the server never takes the entry
        assert_eq!(
            autostart_decision(Some(&ours), true, moved, true),
            Autostart::Leave
        );
        assert_eq!(
            autostart_decision(None, false, moved, true),
            Autostart::Leave
        );
        // someone else's maktaba.desktop is left alone
        let other = "[Desktop Entry]\nType=Application\nExec=/usr/bin/maktaba-client\n";
        assert_eq!(
            autostart_decision(Some(other), false, app, false),
            Autostart::Leave
        );
    }
}
