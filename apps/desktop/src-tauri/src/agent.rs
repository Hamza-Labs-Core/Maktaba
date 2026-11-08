// The server login item (Epic 36 story 36.5; Epic 38 story 38.3 on Windows).
//
// macOS. Maktaba.app ships a LaunchAgent plist at
// Contents/Library/LaunchAgents/com.hamzalabs.maktaba.server.plist
// (source: src-tauri/LaunchAgents/, copied by tauri.conf.json
// bundle.macOS.files). `register` hands it to launchd through
// SMAppService (macOS 13+), which also lists it under System Settings →
// Login Items as "Maktaba".
//
// The plist runs this same executable with `--server-agent`
// (BundleProgram must be inside the bundle, and launchd cannot expand
// `~` in StandardOutPath). That mode opens ~/Library/Logs/Maktaba/
// server.log as stdout/stderr and execs
// Contents/Resources/server/bin/maktaba-launcher serve, so launchd
// supervises the launcher itself and signals reach it directly. The
// launcher sees it sits in Maktaba.app/Contents/Resources/server/bin and
// runs in app-bundle mode (track D: state under ~/Library/Application
// Support/Maktaba, no self-update), so no root or env is passed here.
//
// Pause / resume talk to launchd: SIGTERM stops the launcher, which shuts
// the supervisor down cleanly and exits 0; the plist's KeepAlive is
// {SuccessfulExit: false}, so launchd leaves it stopped until Resume
// (kickstart) or the next login.
//
// Windows (agent_windows.rs, specs/epics/38-windows-app/contract-agent.md).
// A sign-in task of the user, "\Maktaba Server <the user's SID>", runs the
// same executable with `--server-agent`, which starts
// <install>\server\bin\maktaba-launcher in a console without a window and
// restarts it if it fails. The task's watchdog trigger runs it again every
// 5 minutes (nothing happens while it runs), so an agent that died comes
// back; one started while the server is paused, or stopped for an install,
// in this sign-in exits at once. A service is not used: it would run in
// session 0, where the user's mapped NAS drives do not exist. Pause sets the
// named event the launcher watches; Resume runs the task again. The
// installer's hooks call `--server-start`, `--server-stop` and
// `--server-remove`.
//
// Linux (agent_linux.rs, specs/epics/39-linux-app/contract-runtime.md). A
// systemd user unit runs a copy of the payload (<prefix>/share/maktaba/server)
// in ~/.local/share/maktaba/runtime (never this app's files: an AppImage's
// mount moves every run), written and controlled through the payload's own
// `maktaba-server runtime` and `maktaba-server service`; linger starts it at
// boot.
use std::ffi::OsStr;
use std::fs::File;
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};
#[cfg(not(windows))]
use std::process::Command;

#[cfg(windows)]
#[path = "agent_windows.rs"]
mod win;

// The agent answers the home's update orders on Windows; its decisions are tested on
// every OS
#[cfg(any(windows, test))]
#[path = "agent_orders.rs"]
pub mod orders;

/// The agent's kill-on-close job, for agent_orders' test of an installer outliving it.
#[cfg(all(windows, test))]
pub fn join_job_for_tests() -> Result<(), String> {
    win::join_job_for_tests()
}

// compiled in every test build too: its decisions are tested on every OS
#[cfg(any(target_os = "linux", test))]
#[path = "agent_linux.rs"]
pub mod linux;

pub const LABEL: &str = "com.hamzalabs.maktaba.server";
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub const PLIST_NAME: &str = "com.hamzalabs.maktaba.server.plist";
/// argv[1] that turns the app executable into the server agent.
pub const AGENT_FLAG: &str = "--server-agent";
/// The argument that starts the app with its window hidden: the tray at sign-in (the Linux
/// autostart entry, the Windows Run value).
pub const HIDDEN_FLAG: &str = "--hidden";
/// argv[1] of the Windows installer's hooks: start the server when it is set
/// up and not paused, stop it and wait, stop it and remove the sign-in task.
#[cfg_attr(not(windows), allow(dead_code))]
pub const START_FLAG: &str = "--server-start";
#[cfg_attr(not(windows), allow(dead_code))]
pub const STOP_FLAG: &str = "--server-stop";
#[cfg_attr(not(windows), allow(dead_code))]
pub const REMOVE_FLAG: &str = "--server-remove";
/// server.log is rotated to server.log.1 when it grows past this.
const LOG_ROTATE_BYTES: u64 = 50 << 20;
/// How much of server.log's end the tray reads for why the server does not
/// answer: a launcher's last two starts (a crash loop's) fit in it many times.
const LOG_TAIL_BYTES: u64 = 64 << 10;

/// Where the server payload is, from the app executable's path:
/// …/Maktaba.app/Contents/MacOS/Maktaba on a Mac, <install>\Maktaba.exe on
/// Windows (the payload is <install>\server beside it), <prefix>/bin/Maktaba
/// on Linux (the payload is <prefix>/share/maktaba/server).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct BundlePaths {
    /// Contents/Resources/server, or <install>\server
    pub server_dir: PathBuf,
}

impl BundlePaths {
    pub fn from_exe(exe: &Path) -> Option<Self> {
        if cfg!(windows) {
            Self::from_install_exe(exe)
        } else if cfg!(target_os = "linux") {
            Self::from_linux_exe(exe)
        } else {
            Self::from_app_exe(exe)
        }
    }

    /// <prefix>/bin/Maktaba → <prefix>/share/maktaba/server, where the Linux
    /// bundles put the payload (tools/release/linux-app/payload.sh's overlay):
    /// /usr in a .deb or an .rpm, $APPDIR/usr in the AppImage (mounted at a new
    /// /tmp/.mount_* every run). Not among the resources in <prefix>/lib:
    /// linuxdeploy rewrites the ELF files it finds there.
    fn from_linux_exe(exe: &Path) -> Option<Self> {
        Some(Self {
            server_dir: exe
                .parent()?
                .parent()?
                .join("share")
                .join("maktaba")
                .join("server"),
        })
    }

    /// Maktaba.app/Contents/MacOS/Maktaba → Contents/Resources/server.
    fn from_app_exe(exe: &Path) -> Option<Self> {
        let contents = exe.parent()?.parent()?;
        if contents.file_name()? != "Contents" {
            return None;
        }
        Some(Self {
            server_dir: contents.join("Resources").join("server"),
        })
    }

    /// <install>\Maktaba.exe → <install>\server (where the NSIS installer puts
    /// the bundle's resources). A dev build in target\ has none: payload_present
    /// is false there.
    fn from_install_exe(exe: &Path) -> Option<Self> {
        Some(Self {
            server_dir: exe.parent()?.join("server"),
        })
    }

    pub fn launcher(&self) -> PathBuf {
        self.server_dir
            .join("bin")
            .join(exe_name("maktaba-launcher"))
    }

    pub fn server_bin(&self) -> PathBuf {
        self.server_dir.join("bin").join(exe_name("maktaba-server"))
    }

    /// The bundle carries a server payload (a client-only build does not).
    pub fn payload_present(&self) -> bool {
        self.server_bin().is_file()
    }
}

/// A program's file name on this OS.
fn exe_name(name: &str) -> String {
    if cfg!(windows) {
        format!("{name}.exe")
    } else {
        name.to_string()
    }
}

/// The install's state folder (server.toml, keys, the database, logs on
/// Windows): %LOCALAPPDATA%\Maktaba on Windows (Epic 38), ~/Library/
/// Application Support/Maktaba on a Mac (Epic 36), $XDG_DATA_HOME/maktaba on
/// Linux (Epic 39).
#[cfg(target_os = "linux")]
pub fn state_root() -> Option<PathBuf> {
    // the server's unit pins its XDG directories: where it keeps the home's order and the
    // attempts is where that environment says (this app's own may differ)
    home().map(|h| {
        let unit = linux::unit_env(&h, "XDG_DATA_HOME");
        linux::data_dir(
            |k| match k {
                "XDG_DATA_HOME" => unit.clone().or_else(|| env_var(k)),
                _ => env_var(k),
            },
            &h,
        )
    })
}

#[cfg(not(target_os = "linux"))]
pub fn state_root() -> Option<PathBuf> {
    if cfg!(windows) {
        let local = std::env::var_os("LOCALAPPDATA")
            .map(PathBuf::from)
            .or_else(|| home().map(|h| h.join("AppData").join("Local")))?;
        return Some(local.join("Maktaba"));
    }
    Some(
        home()?
            .join("Library")
            .join("Application Support")
            .join("Maktaba"),
    )
}

/// Where the server's log goes: ~/Library/Logs/Maktaba on a Mac,
/// %LOCALAPPDATA%\Maktaba\logs on Windows, $XDG_STATE_HOME/maktaba/logs on
/// Linux (maktaba-server's own logs dir).
#[cfg(target_os = "linux")]
pub fn logs_dir() -> Option<PathBuf> {
    home().map(|h| linux::logs_dir(env_var, &h))
}

/// A set, non-empty environment variable.
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
fn env_var(name: &str) -> Option<String> {
    std::env::var(name).ok().filter(|v| !v.is_empty())
}

#[cfg(not(target_os = "linux"))]
pub fn logs_dir() -> Option<PathBuf> {
    if cfg!(windows) {
        return state_root().map(|r| r.join("logs"));
    }
    home().map(|h| mac_logs_dir(&h))
}

/// ~/Library/Logs/Maktaba
#[cfg_attr(target_os = "linux", allow(dead_code))]
fn mac_logs_dir(home: &Path) -> PathBuf {
    home.join("Library").join("Logs").join("Maktaba")
}

fn home() -> Option<PathBuf> {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
}

/// Move server.log aside once it is large (one old copy is kept).
fn rotate(log: &Path, limit: u64) {
    if std::fs::metadata(log)
        .map(|m| m.len() > limit)
        .unwrap_or(false)
    {
        let _ = std::fs::rename(log, log.with_extension("log.1"));
    }
}

/// server.log, opened for append (its folder made, rotated when large).
fn open_server_log() -> Result<(File, PathBuf), String> {
    let logs = logs_dir().ok_or("cannot find the home folder (HOME / LOCALAPPDATA)")?;
    std::fs::create_dir_all(&logs).map_err(|e| format!("cannot create {}: {e}", logs.display()))?;
    let path = logs.join("server.log");
    rotate(&path, LOG_ROTATE_BYTES);
    let log = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)
        .map_err(|e| format!("cannot open {}: {e}", path.display()))?;
    Ok((log, path))
}

/// The end of server.log, for the tray's reason when the server does not
/// answer (status::failure_in_log). None when there is no log.
pub fn server_log_tail() -> Option<String> {
    read_tail(&logs_dir()?.join("server.log"), LOG_TAIL_BYTES)
}

/// The last `max` bytes of a file, from its first whole line.
fn read_tail(path: &Path, max: u64) -> Option<String> {
    let mut f = File::open(path).ok()?;
    let len = f.metadata().ok()?.len();
    let from = len.saturating_sub(max);
    f.seek(SeekFrom::Start(from)).ok()?;
    let mut buf = Vec::new();
    f.read_to_end(&mut buf).ok()?;
    let text = String::from_utf8_lossy(&buf).into_owned();
    // a line cut by the seek is not read as one
    Some(match text.split_once('\n') {
        Some((_, rest)) if from > 0 => rest.to_string(),
        _ => text,
    })
}

/// The pause flag: <state>/state/server-paused while the user paused the
/// server. A paused server stays paused when the window restarts (an update
/// relaunches it) until Resume, or until the next sign-in / login. On a Mac
/// the agent clears it when launchd starts it; on Windows the flag names the
/// sign-in it was set in and holds only in that one, so the task's watchdog
/// (which starts the agent again and again) never undoes it.
fn pause_flag_path() -> Option<PathBuf> {
    state_root().map(|r| r.join("state").join("server-paused"))
}

#[cfg(windows)]
pub fn paused_flag() -> bool {
    pause_flag_path().is_some_and(|p| win::flag_holds(&p))
}

#[cfg(not(windows))]
pub fn paused_flag() -> bool {
    pause_flag_path().is_some_and(|p| p.is_file())
}

/// The pause flag's text: on Windows also the sign-in it belongs to.
fn pause_text() -> String {
    const WHAT: &str = "paused from the Maktaba app";
    #[cfg(windows)]
    {
        win::flag_text(WHAT)
    }
    #[cfg(not(windows))]
    {
        format!("{WHAT}\n")
    }
}

pub fn set_paused_flag(on: bool) {
    let Some(p) = pause_flag_path() else { return };
    // a flag that cannot be written or removed: the pause does not hold (or holds) across
    // the app's restart; the server's own state is set either way, so it is logged
    let res = if on {
        p.parent()
            .map_or(Ok(()), std::fs::create_dir_all)
            .and_then(|()| std::fs::write(&p, pause_text()))
    } else {
        match std::fs::remove_file(&p) {
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
            other => other,
        }
    };
    if let Err(e) = res {
        let what = if on { "set" } else { "cleared" };
        crate::applog::warn(&format!("the server's pause flag could not be {what}"), e);
    }
}

/// `Maktaba --server-agent`: become the launcher. Returns only on error.
#[cfg(unix)]
pub fn run_server_agent() -> Result<i32, String> {
    use std::os::unix::process::CommandExt;

    let exe =
        std::env::current_exe().map_err(|e| format!("cannot find the app executable: {e}"))?;
    let Some(paths) = BundlePaths::from_exe(&exe) else {
        return Err(format!(
            "{} is not inside Maktaba.app/Contents/MacOS",
            exe.display()
        ));
    };
    let (log, log_path) = open_server_log()?;
    let log2 = log
        .try_clone()
        .map_err(|e| format!("cannot open {}: {e}", log_path.display()))?;
    // launchd started us: the login (or Resume) undoes a pause
    set_paused_flag(false);
    let err = Command::new(paths.launcher())
        .arg("serve")
        .stdout(log)
        .stderr(log2)
        .exec();
    Err(format!(
        "cannot start {}: {err}",
        paths.launcher().display()
    ))
}

#[cfg(windows)]
pub use win::run_server_agent;

/// The Windows installer's hooks (contract-agent): the exit code for NSIS.
#[cfg(windows)]
pub use win::installer_hook;

/// Whether the login item is registered (and allowed).
#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize)]
#[serde(rename_all = "snake_case")]
pub enum LoginItem {
    NotRegistered,
    Enabled,
    /// Registered, but turned off: in System Settings → Login Items on a Mac,
    /// the sign-in task disabled in Task Scheduler on Windows.
    RequiresApproval,
    /// The plist is not in the bundle (a dev build run outside Maktaba.app).
    #[cfg_attr(not(target_os = "macos"), allow(dead_code))]
    NotFound,
    #[cfg_attr(any(target_os = "macos", windows), allow(dead_code))]
    Unsupported,
}

/// The login item's registration error as the app says it: one macOS refused for the app's
/// code signature (SMAppService's "Codesigning failure loading plist", code -67056,
/// SMAppServiceErrorDomain 3: an app bundle that is not signed, as the Mac's 0.3.0 was,
/// #273) starts with "login_item_unsigned: " and says what mends it (the setup screens say
/// it in the reader's language by that code); any other is as macOS said it.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub fn login_item_error(raw: &str) -> String {
    let unsigned = raw.contains("Codesigning failure")
        || raw.contains("-67056")
        || raw.contains("SMAppServiceErrorDomain 3)");
    if !unsigned {
        return raw.to_string();
    }
    format!(
        "{}: this app is not code-signed, so macOS will not load the server's login item. \
         Install a signed release of Maktaba, or sign this copy: {} ({raw})",
        crate::attempts::LOGIN_ITEM_UNSIGNED,
        crate::attempts::fix_for(crate::attempts::LOGIN_ITEM_UNSIGNED)
    )
}

#[cfg(target_os = "macos")]
mod sm {
    use objc2_foundation::NSString;
    use objc2_service_management::{SMAppService, SMAppServiceStatus};

    use super::{LoginItem, PLIST_NAME};

    fn service() -> objc2::rc::Retained<SMAppService> {
        // SAFETY: a plain class-method call with a valid NSString.
        unsafe { SMAppService::agentServiceWithPlistName(&NSString::from_str(PLIST_NAME)) }
    }

    pub fn status() -> LoginItem {
        // SAFETY: reading a property of a live SMAppService.
        let s = unsafe { service().status() };
        match s {
            SMAppServiceStatus::Enabled => LoginItem::Enabled,
            SMAppServiceStatus::RequiresApproval => LoginItem::RequiresApproval,
            SMAppServiceStatus::NotFound => LoginItem::NotFound,
            _ => LoginItem::NotRegistered,
        }
    }

    pub fn register() -> Result<(), String> {
        // SAFETY: SMAppService register/unregister are thread-safe calls
        // on a live object; the error is an owned NSError.
        unsafe { service().registerAndReturnError() }.map_err(|e| {
            let raw = format!("{} ({} {})", e.localizedDescription(), e.domain(), e.code());
            let said = super::login_item_error(&raw);
            crate::attempts::log(&format!("login item: {said}"));
            said
        })
    }

    pub fn unregister() -> Result<(), String> {
        // SAFETY: as in register.
        unsafe { service().unregisterAndReturnError() }
            .map_err(|e| e.localizedDescription().to_string())
    }

    pub fn open_settings() {
        // SAFETY: opens System Settings → Login Items; no arguments.
        unsafe { SMAppService::openSystemSettingsLoginItems() }
    }
}

#[cfg(target_os = "macos")]
pub use sm::{
    open_settings as open_login_items_settings, register, status as login_item, unregister,
};

/// What `register_login_item` found and did.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Synced {
    /// Already this build's (or left to the newer build on disk): nothing was done.
    Unchanged,
    /// Registered (again) for this build.
    Registered,
    /// macOS refused to register it again, but launchd has the job: it was started.
    Started,
    /// Registered, but turned off in System Settings › Login Items.
    NeedsApproval,
}

/// What the server's login item needs (login_item_step).
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LoginStep {
    /// It is this build's: nothing to do (the stamp is written when it changed).
    Keep,
    /// Nothing is registered: register.
    Register,
    /// Another build's registration: unregister, then register.
    Reregister,
    /// The registration is good: start the job launchd has.
    Kickstart,
    /// Turned off in System Settings › Login Items: only the user can turn it on.
    Approve,
    /// This process runs a build the app on disk replaced (an install swapped the bundle
    /// under it): the new build's start registers the login item, not this one.
    LeaveToNewBuild,
}

/// What macOS refusing to register the login item leads to (on_refusal).
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Refusal {
    /// launchd still has the job (the login item is Enabled): start it, no dialog.
    Kickstart,
    /// Turned off in System Settings › Login Items: the user turns it on there.
    Approve,
    /// Nothing registered that could start: the error stands.
    Fail,
}

/// The registration stamp of an app signed with a Team ID (a Developer ID release): its
/// team, its bundled plist and where the bundle is. The executable is not in it: a
/// Developer ID build keeps the same designated requirement (anchor, team, identifier)
/// across updates, so launchd keeps starting the registered job after an update and
/// registering it again is needed only when the plist changed.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub fn team_stamp(team: &str, plist: &[u8], bundle: &Path) -> String {
    // FNV-1a: stable across Rust releases (DefaultHasher is not promised to be)
    let hash = plist.iter().fold(0xcbf2_9ce4_8422_2325_u64, |h, b| {
        (h ^ u64::from(*b)).wrapping_mul(0x0100_0000_01b3)
    });
    format!("team:{team}|plist:{hash:016x}|{}", bundle.display())
}

/// A team stamp's team and plist hash; None for a stamp of an app signed ad hoc (its
/// executable's path, size and time).
fn team_stamp_parts(stamp: &str) -> Option<(&str, &str)> {
    let mut parts = stamp.strip_prefix("team:")?.split('|');
    let team = parts.next()?;
    let plist = parts.next()?.strip_prefix("plist:")?;
    Some((team, plist))
}

/// The Team ID in `codesign -dv`'s output; None for an app signed ad hoc ("not set").
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub fn team_from_codesign(said: &str) -> Option<String> {
    said.lines()
        .find_map(|l| l.trim().strip_prefix("TeamIdentifier="))
        .map(str::trim)
        .filter(|t| !t.is_empty() && *t != "not set")
        .map(String::from)
}

/// What the server's login item needs, from this build's stamp (`now`), the one saved by
/// the build that last registered it, its status, `force` (the server stayed silent after
/// an update) and `stale` (an install replaced this process's build on disk).
///
/// An app signed ad hoc is identified by its code hash, so after an update launchd refuses
/// the old registration ("Launch Constraint Violation", exit 78): it is registered again
/// (unregister, then register). A Developer ID build is not: its registration stays valid,
/// and unregistering it only risks macOS refusing the register that follows (2026-11-08:
/// SMAppServiceErrorDomain 1 on every launch, while launchd still had the job). It is
/// registered again only for a changed plist; a silent server's job is started instead.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub fn login_item_step(
    force: bool,
    stale: bool,
    now: &str,
    saved: Option<&str>,
    status: LoginItem,
) -> LoginStep {
    if stale {
        return LoginStep::LeaveToNewBuild;
    }
    let Some((_, plist)) = team_stamp_parts(now) else {
        return if !force && saved == Some(now) {
            LoginStep::Keep
        } else {
            LoginStep::Reregister
        };
    };
    match status {
        LoginItem::Enabled => {
            let plist_changed = saved
                .and_then(team_stamp_parts)
                .is_some_and(|(_, was)| was != plist);
            if plist_changed {
                LoginStep::Reregister
            } else if force {
                LoginStep::Kickstart
            } else {
                LoginStep::Keep
            }
        }
        LoginItem::RequiresApproval => LoginStep::Approve,
        _ => LoginStep::Register,
    }
}

/// macOS refused to register the login item (`err`): what then, from its status now and
/// whether launchd still has the job. A login item that is Enabled, or a job launchd has
/// loaded, is a valid registration: its job is started rather than the server called
/// unable to start.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub fn on_refusal(err: &str, status: LoginItem, job_loaded: bool) -> Refusal {
    if err.starts_with(crate::attempts::LOGIN_ITEM_UNSIGNED) {
        Refusal::Fail
    } else if status == LoginItem::RequiresApproval {
        Refusal::Approve
    } else if status == LoginItem::Enabled || job_loaded {
        Refusal::Kickstart
    } else {
        Refusal::Fail
    }
}

/// Whether `launchctl print`'s output says the job is running.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub fn job_running(print: &str) -> bool {
    print.lines().any(|l| l.trim() == "state = running")
}

/// This build of the app as it was when this process started (remember_this_build): an
/// install that swaps the bundle under the running app changes the executable on disk.
#[cfg(target_os = "macos")]
static BUILD_AT_START: std::sync::OnceLock<String> = std::sync::OnceLock::new();

/// Remember this process's build, before any install can replace it on disk.
#[cfg(target_os = "macos")]
pub fn remember_this_build() {
    if let Some(stamp) = build_stamp() {
        let _ = BUILD_AT_START.set(stamp);
    }
}

/// macOS: the server's login item is bound to the build of the app that registered it
/// (login_item_step says when it must be registered again). Checked once per build (the
/// stamp in <state>/state/login-item-build).
#[cfg(target_os = "macos")]
pub fn register_for_this_build() -> Result<Synced, String> {
    register_login_item(false)
}

/// One registration of the server's login item at a time (the app's start and the
/// settling of its update may both ask).
#[cfg(target_os = "macos")]
static LOGIN_ITEM_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

/// The server's login item made right for this build (login_item_step), or with `force`
/// once the server stayed silent after an update. A registration macOS refuses while
/// launchd has the job starts the job instead (on_refusal).
#[cfg(target_os = "macos")]
pub fn register_login_item(force: bool) -> Result<Synced, String> {
    let _one = LOGIN_ITEM_LOCK.lock().unwrap_or_else(|e| e.into_inner());
    let exe_stamp = build_stamp().ok_or("cannot read this app's executable")?;
    let stale = BUILD_AT_START.get().is_some_and(|at| *at != exe_stamp);
    let now = registration_stamp(&exe_stamp);
    let path = state_root()
        .ok_or("cannot find the home folder")?
        .join("state")
        .join("login-item-build");
    let saved = std::fs::read_to_string(&path).ok();
    let status = login_item();
    let step = login_item_step(force, stale, &now, saved.as_deref(), status);
    let save = || {
        if let Some(dir) = path.parent() {
            let _ = std::fs::create_dir_all(dir);
        }
        let _ = std::fs::write(&path, &now);
    };
    if step != LoginStep::Keep || saved.as_deref() != Some(now.as_str()) {
        crate::attempts::log(&format!(
            "login item: {step:?} (it is {status:?}{})",
            if force { ", the server is silent" } else { "" }
        ));
    }
    match step {
        LoginStep::LeaveToNewBuild => Ok(Synced::Unchanged),
        LoginStep::Keep => {
            if saved.as_deref() != Some(now.as_str()) {
                register_bundle_with_launch_services();
                save();
            }
            Ok(Synced::Unchanged)
        }
        LoginStep::Approve => {
            save();
            Ok(Synced::NeedsApproval)
        }
        LoginStep::Kickstart => {
            register_bundle_with_launch_services();
            start_registered_job()?;
            save();
            Ok(Synced::Started)
        }
        LoginStep::Register | LoginStep::Reregister => {
            // LaunchServices first learns where this bundle now is: the login item names
            // its program relative to the bundle, which launchd finds through it. Right
            // after an install it still knows the old copy, and the agent "Could not find
            // and/or execute program ... Contents/MacOS/Maktaba" until the app launched
            // again.
            register_bundle_with_launch_services();
            // an older build's registration: replaced by this build's. The unregister
            // finishes in the background; a register that comes first leaves the job
            // unable to find its program, so wait until it is gone.
            if step == LoginStep::Reregister && unregister().is_ok() {
                for _ in 0..50 {
                    if !matches!(
                        login_item(),
                        LoginItem::Enabled | LoginItem::RequiresApproval
                    ) {
                        break;
                    }
                    std::thread::sleep(std::time::Duration::from_millis(200));
                }
                std::thread::sleep(std::time::Duration::from_secs(1));
            }
            let synced = register_or_start()?;
            if synced != Synced::Started {
                save();
            }
            Ok(synced)
        }
    }
}

/// Register the server's login item; one macOS refuses while launchd has the job (or it is
/// Enabled) starts that job instead (on_refusal), and only a refusal with nothing that can
/// start is an error.
#[cfg(target_os = "macos")]
pub fn register_or_start() -> Result<Synced, String> {
    let err = match register() {
        Ok(()) if login_item() == LoginItem::RequiresApproval => return Ok(Synced::NeedsApproval),
        Ok(()) => return Ok(Synced::Registered),
        Err(e) => e,
    };
    let status = login_item();
    match on_refusal(&err, status, job_loaded()) {
        Refusal::Fail => Err(err),
        Refusal::Approve => Ok(Synced::NeedsApproval),
        Refusal::Kickstart => {
            crate::attempts::log(&format!(
                "login item: macOS would not register it again ({err}), but it is {status:?} \
                 and launchd has its job: starting that job"
            ));
            start_registered_job()
                .map_err(|e| format!("{err}; starting the registered job failed too: {e}"))?;
            Ok(Synced::Started)
        }
    }
}

/// Whether launchd has the server's job (`launchctl print gui/<uid>/<label>`), and its output.
#[cfg(target_os = "macos")]
fn print_job() -> Option<String> {
    let target = format!("gui/{}/{LABEL}", uid());
    let out = Command::new("/bin/launchctl")
        .args(["print", &target])
        .output()
        .ok()?;
    out.status
        .success()
        .then(|| String::from_utf8_lossy(&out.stdout).into_owned())
}

#[cfg(target_os = "macos")]
fn job_loaded() -> bool {
    print_job().is_some()
}

/// How long launchd may take to run the job it was asked to start.
#[cfg(target_os = "macos")]
const KICKSTART_WAIT: std::time::Duration = std::time::Duration::from_secs(10);

/// Start the job launchd has for the server (`launchctl kickstart`; one that runs is left
/// running) and wait until launchd runs it.
#[cfg(target_os = "macos")]
fn start_registered_job() -> Result<(), String> {
    let out = Command::new("/bin/launchctl")
        .args(launchctl_args(Action::Resume, uid()))
        .output()
        .map_err(|e| format!("launchctl: {e}"))?;
    if !out.status.success() {
        return Err(format!(
            "launchctl kickstart: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        ));
    }
    let began = std::time::Instant::now();
    while began.elapsed() < KICKSTART_WAIT {
        if print_job().is_some_and(|p| job_running(&p)) {
            return Ok(());
        }
        std::thread::sleep(std::time::Duration::from_millis(250));
    }
    Err(format!(
        "launchd did not run the server's job within {} s of starting it",
        KICKSTART_WAIT.as_secs()
    ))
}

/// This build's registration stamp: team_stamp for an app signed with a Team ID, the
/// executable's stamp (build_stamp) for one signed ad hoc.
#[cfg(target_os = "macos")]
fn registration_stamp(exe_stamp: &str) -> String {
    let Some(bundle) = this_bundle() else {
        return exe_stamp.to_string();
    };
    let team = Command::new("/usr/bin/codesign")
        .arg("-dv")
        .arg(&bundle)
        .output()
        .ok()
        .and_then(|o| team_from_codesign(&String::from_utf8_lossy(&o.stderr)));
    let plist = std::fs::read(
        bundle
            .join("Contents/Library/LaunchAgents")
            .join(PLIST_NAME),
    );
    match (team, plist) {
        (Some(team), Ok(plist)) => team_stamp(&team, &plist, &bundle),
        _ => exe_stamp.to_string(),
    }
}

/// The Maktaba.app bundle this process runs from; None outside one (a dev build).
#[cfg(target_os = "macos")]
fn this_bundle() -> Option<PathBuf> {
    std::env::current_exe()
        .ok()
        .and_then(|exe| exe.ancestors().nth(3).map(|p| p.to_path_buf()))
        .filter(|p| p.extension().is_some_and(|e| e == "app"))
}

/// Tell LaunchServices where this app bundle is (`lsregister -f`); best effort.
#[cfg(target_os = "macos")]
fn register_bundle_with_launch_services() {
    const LSREGISTER: &str = "/System/Library/Frameworks/CoreServices.framework/Frameworks/\
        LaunchServices.framework/Support/lsregister";
    let Some(bundle) = this_bundle() else {
        return;
    };
    let _ = Command::new(LSREGISTER).arg("-f").arg(&bundle).output();
}

/// This build of the app: its executable's path, size and modification time.
#[cfg(target_os = "macos")]
fn build_stamp() -> Option<String> {
    let exe = std::env::current_exe().ok()?;
    let meta = std::fs::metadata(&exe).ok()?;
    let at = meta
        .modified()
        .ok()?
        .duration_since(std::time::UNIX_EPOCH)
        .ok()?
        .as_nanos();
    Some(format!("{}:{}:{at}", exe.display(), meta.len()))
}

// Windows: the sign-in task. Turning it on again needs no settings screen:
// open_login_items_settings enables and runs it. Windows only: stopping the
// server for an install (its files are in use while it runs), moving a task
// of the old shared name to this user's, and whether setup ran on this PC.
// The tray at sign-in too: the Run value (run_entry.rs).
#[cfg(windows)]
pub use win::{
    agent_alive, control, login_item, open_login_items_settings, register, registered_here,
    replace_old_task, stop_for_install, sync_autostart, unregister,
};

// Linux: the systemd user unit, through `maktaba-server service`.
#[cfg(target_os = "linux")]
pub use linux::{
    agent_alive, control, login_item, open_login_items_settings, register, unregister,
};

#[cfg(not(any(target_os = "macos", windows, target_os = "linux")))]
pub fn login_item() -> LoginItem {
    LoginItem::Unsupported
}
#[cfg(not(any(target_os = "macos", windows, target_os = "linux")))]
pub fn register() -> Result<(), String> {
    Err("the server login item is macOS, Windows and Linux only".into())
}
#[cfg(not(any(target_os = "macos", windows, target_os = "linux")))]
pub fn unregister() -> Result<(), String> {
    Err("the server login item is macOS, Windows and Linux only".into())
}
#[cfg(not(any(target_os = "macos", windows, target_os = "linux")))]
pub fn open_login_items_settings() {}

/// Whether the server agent runs in this session, when the OS can tell (the
/// Windows agent's mutex, the Linux unit); None on a Mac, where launchd knows.
#[cfg(not(any(windows, target_os = "linux")))]
pub fn agent_alive() -> Option<bool> {
    None
}

/// launchctl arguments for an action on the agent (gui/<uid>/<label>).
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub fn launchctl_args(action: Action, uid: u32) -> Vec<String> {
    let target = format!("gui/{uid}/{LABEL}");
    match action {
        Action::Pause => vec!["kill".into(), "TERM".into(), target],
        Action::Resume => vec!["kickstart".into(), target],
        Action::Restart => vec!["kickstart".into(), "-k".into(), target],
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Action {
    Pause,
    Resume,
    Restart,
}

#[cfg(unix)]
#[cfg_attr(target_os = "linux", allow(dead_code))]
fn uid() -> u32 {
    // SAFETY: getuid cannot fail.
    unsafe { libc::getuid() }
}

/// Pause, resume or restart the server through launchd.
#[cfg(not(any(windows, target_os = "linux")))]
pub fn control(action: Action) -> Result<(), String> {
    // a login item another build registered cannot start: this build's first. Right after
    // an install swapped the bundle under this process, the new build's start does that
    // (LeaveToNewBuild) and launchd restarts the job on the new bundle; a registration
    // macOS refused while launchd has the job already started it (Synced::Started)
    #[cfg(target_os = "macos")]
    if action != Action::Pause
        && register_for_this_build()? == Synced::Started
        && action == Action::Resume
    {
        return Ok(());
    }
    let out = Command::new("/bin/launchctl")
        .args(launchctl_args(action, uid()))
        .output()
        .map_err(|e| format!("launchctl: {e}"))?;
    if out.status.success() {
        Ok(())
    } else {
        Err(format!(
            "launchctl: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        ))
    }
}

/// Show the server's logs folder (created if missing).
pub fn open_logs() -> Result<(), String> {
    let dir = logs_dir().ok_or("cannot find the home folder")?;
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    open_path(dir.as_os_str())
}

/// Open a folder or an https address with the system's handler: Finder or
/// the browser on a Mac, Explorer or the browser on Windows.
pub fn open_path(target: &OsStr) -> Result<(), String> {
    #[cfg(windows)]
    {
        win::shell_open(target)
    }
    #[cfg(target_os = "macos")]
    {
        Command::new("/usr/bin/open")
            .arg(target)
            .spawn()
            .map(|_| ())
            .map_err(|e| e.to_string())
    }
    #[cfg(not(any(target_os = "macos", windows)))]
    {
        Command::new("xdg-open")
            .arg(target)
            .spawn()
            .map(|_| ())
            .map_err(|e| e.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_login_item_refused_for_the_signature_says_so() {
        for raw in [
            "Codesigning failure loading plist: com.hamzalabs.maktaba.server code: -67056 (SMAppServiceErrorDomain 3)",
            "The operation couldn\u{2019}t be completed. (SMAppServiceErrorDomain 3)",
        ] {
            let said = login_item_error(raw);
            assert!(said.starts_with("login_item_unsigned: "), "{said}");
            assert!(said.contains("codesign --force --deep -s - --identifier com.hamzalabs.maktaba"), "{said}");
            assert!(said.ends_with(&format!("({raw})")), "the raw error is kept: {said}");
        }
        let other = "Operation not permitted (SMAppServiceErrorDomain 1)";
        assert_eq!(login_item_error(other), other);
    }

    const PLIST: &[u8] = include_bytes!("../LaunchAgents/com.hamzalabs.maktaba.server.plist");

    fn signed() -> String {
        team_stamp("G58UVTKQPB", PLIST, Path::new("/Applications/Maktaba.app"))
    }

    #[test]
    fn the_team_id_is_read_from_codesign() {
        let developer_id = "Executable=/Applications/Maktaba.app/Contents/MacOS/Maktaba\n\
            Identifier=com.hamzalabs.maktaba\nTeamIdentifier=G58UVTKQPB\nSealed Resources version=2\n";
        assert_eq!(
            team_from_codesign(developer_id).as_deref(),
            Some("G58UVTKQPB")
        );
        let ad_hoc = "Signature=adhoc\nInfo.plist=not bound\nTeamIdentifier=not set\n";
        assert_eq!(team_from_codesign(ad_hoc), None);
        assert_eq!(team_from_codesign("code object is not signed at all"), None);
    }

    #[test]
    fn a_team_stamp_follows_the_plist_not_the_executable() {
        let now = signed();
        assert!(now.starts_with("team:G58UVTKQPB|plist:"), "{now}");
        assert_eq!(now, signed(), "stable for the same plist");
        assert_ne!(
            now,
            team_stamp(
                "G58UVTKQPB",
                b"<plist/>",
                Path::new("/Applications/Maktaba.app")
            ),
            "a changed plist changes it"
        );
    }

    #[test]
    fn a_developer_id_update_keeps_the_registration() {
        // 0.3.90 → 0.3.96 on 2026-11-08: the saved stamp was the old build's executable
        let old = "/Applications/Maktaba.app/Contents/MacOS/Maktaba:1234:1730000000";
        let now = signed();
        let step = |force, saved, status| login_item_step(force, false, &now, saved, status);
        assert_eq!(step(false, Some(old), LoginItem::Enabled), LoginStep::Keep);
        assert_eq!(step(false, None, LoginItem::Enabled), LoginStep::Keep);
        assert_eq!(
            step(false, Some(now.as_str()), LoginItem::Enabled),
            LoginStep::Keep
        );
        // a moved bundle keeps it too (LaunchServices learns the new place)
        let moved = team_stamp(
            "G58UVTKQPB",
            PLIST,
            Path::new("/Users/me/Applications/Maktaba.app"),
        );
        assert_eq!(
            step(false, Some(moved.as_str()), LoginItem::Enabled),
            LoginStep::Keep
        );
        // the server stays silent after the update: its job is started, never re-registered
        assert_eq!(
            step(true, Some(now.as_str()), LoginItem::Enabled),
            LoginStep::Kickstart
        );
        assert_eq!(
            step(true, Some(old), LoginItem::Enabled),
            LoginStep::Kickstart
        );
        // only a changed plist registers it again
        let other_plist = team_stamp(
            "G58UVTKQPB",
            b"<plist/>",
            Path::new("/Applications/Maktaba.app"),
        );
        assert_eq!(
            step(false, Some(other_plist.as_str()), LoginItem::Enabled),
            LoginStep::Reregister
        );
        // nothing registered: register (no unregister first)
        assert_eq!(
            step(false, Some(now.as_str()), LoginItem::NotRegistered),
            LoginStep::Register
        );
        // turned off in System Settings: only the user can turn it on
        assert_eq!(
            step(false, Some(now.as_str()), LoginItem::RequiresApproval),
            LoginStep::Approve
        );
        assert_eq!(
            step(true, None, LoginItem::RequiresApproval),
            LoginStep::Approve
        );
    }

    #[test]
    fn an_ad_hoc_build_registers_again_for_each_build() {
        let old = "/Applications/Maktaba.app/Contents/MacOS/Maktaba:1234:1730000000";
        let now = "/Applications/Maktaba.app/Contents/MacOS/Maktaba:5678:1731000000";
        let step = |force, saved, status| login_item_step(force, false, now, saved, status);
        assert_eq!(
            step(false, Some(old), LoginItem::Enabled),
            LoginStep::Reregister
        );
        assert_eq!(step(false, None, LoginItem::Enabled), LoginStep::Reregister);
        assert_eq!(step(false, Some(now), LoginItem::Enabled), LoginStep::Keep);
        assert_eq!(
            step(true, Some(now), LoginItem::Enabled),
            LoginStep::Reregister
        );
    }

    #[test]
    fn a_process_whose_bundle_was_swapped_leaves_it_to_the_new_build() {
        // the old app restarting the server right after the install (error 22 on 2026-11-08)
        for now in [
            signed(),
            "/Applications/Maktaba.app/Contents/MacOS/Maktaba:1:2".to_string(),
        ] {
            for force in [false, true] {
                assert_eq!(
                    login_item_step(force, true, &now, None, LoginItem::Enabled),
                    LoginStep::LeaveToNewBuild
                );
            }
        }
    }

    #[test]
    fn a_refused_registration_starts_the_job_launchd_has() {
        let not_permitted = "Operation not permitted (SMAppServiceErrorDomain 1)";
        let invalid = "Invalid argument (SMAppServiceErrorDomain 22)";
        // Enabled (or the job loaded) and register refused: kickstart, not the dialog
        assert_eq!(
            on_refusal(not_permitted, LoginItem::Enabled, true),
            Refusal::Kickstart
        );
        assert_eq!(
            on_refusal(not_permitted, LoginItem::Enabled, false),
            Refusal::Kickstart
        );
        assert_eq!(
            on_refusal(invalid, LoginItem::NotRegistered, true),
            Refusal::Kickstart
        );
        // really turned off: the settings prompt
        assert_eq!(
            on_refusal(not_permitted, LoginItem::RequiresApproval, true),
            Refusal::Approve
        );
        // nothing that could start: the error stands
        assert_eq!(
            on_refusal(not_permitted, LoginItem::NotRegistered, false),
            Refusal::Fail
        );
        assert_eq!(
            on_refusal(invalid, LoginItem::NotFound, false),
            Refusal::Fail
        );
        // an unsigned app cannot run the job either way
        let unsigned =
            login_item_error("Codesigning failure loading plist (SMAppServiceErrorDomain 3)");
        assert_eq!(
            on_refusal(&unsigned, LoginItem::Enabled, true),
            Refusal::Fail
        );
    }

    #[test]
    fn launchctl_print_says_whether_the_job_runs() {
        let stopped = "gui/501/com.hamzalabs.maktaba.server = {\n\tactive count = 0\n\
            \tstate = not running\n\tlast exit code = 0\n}\n";
        assert!(!job_running(stopped));
        let running =
            "gui/501/com.hamzalabs.maktaba.server = {\n\tstate = running\n\tpid = 4242\n}\n";
        assert!(job_running(running));
    }

    #[test]
    fn bundle_paths_from_the_mac_app_executable() {
        let p = BundlePaths::from_app_exe(Path::new(
            "/Applications/Maktaba.app/Contents/MacOS/Maktaba",
        ))
        .unwrap();
        assert_eq!(
            p.server_dir,
            Path::new("/Applications/Maktaba.app/Contents/Resources/server")
        );
        // `cargo run` / `tauri dev` runs from target/, not a bundle
        assert_eq!(
            BundlePaths::from_app_exe(Path::new("/src/target/debug/maktaba-desktop")),
            None
        );
    }

    #[test]
    fn bundle_paths_from_the_linux_executable() {
        let deb = BundlePaths::from_linux_exe(Path::new("/usr/bin/Maktaba")).unwrap();
        assert_eq!(
            deb.server_dir,
            Path::new("/usr/share").join("maktaba").join("server")
        );
        let appimage =
            BundlePaths::from_linux_exe(Path::new("/tmp/.mount_MakX1/usr/bin/Maktaba")).unwrap();
        assert_eq!(
            appimage.server_dir,
            Path::new("/tmp/.mount_MakX1/usr/share")
                .join("maktaba")
                .join("server")
        );
    }

    #[test]
    fn bundle_paths_from_the_windows_install() {
        let p = BundlePaths::from_install_exe(Path::new("install/Maktaba.exe")).unwrap();
        assert_eq!(p.server_dir, Path::new("install").join("server"));
    }

    #[test]
    fn programs_are_named_for_this_os() {
        let p = BundlePaths {
            server_dir: PathBuf::from("server"),
        };
        let ext = if cfg!(windows) { ".exe" } else { "" };
        assert_eq!(
            p.launcher(),
            Path::new("server")
                .join("bin")
                .join(format!("maktaba-launcher{ext}"))
        );
        assert_eq!(
            p.server_bin(),
            Path::new("server")
                .join("bin")
                .join(format!("maktaba-server{ext}"))
        );
    }

    #[test]
    fn payload_present_needs_the_server_binary() {
        let tmp = tempfile::tempdir().unwrap();
        let exe = if cfg!(windows) {
            tmp.path().join("Maktaba").join("Maktaba.exe")
        } else {
            tmp.path().join("Maktaba.app/Contents/MacOS/Maktaba")
        };
        let p = BundlePaths::from_exe(&exe).unwrap();
        assert!(!p.payload_present());
        std::fs::create_dir_all(p.server_bin().parent().unwrap()).unwrap();
        std::fs::write(p.server_bin(), b"").unwrap();
        assert!(p.payload_present());
    }

    #[test]
    fn launchctl_targets_the_gui_domain() {
        assert_eq!(
            launchctl_args(Action::Pause, 501),
            ["kill", "TERM", "gui/501/com.hamzalabs.maktaba.server"]
        );
        assert_eq!(
            launchctl_args(Action::Resume, 501),
            ["kickstart", "gui/501/com.hamzalabs.maktaba.server"]
        );
        assert_eq!(
            launchctl_args(Action::Restart, 501),
            ["kickstart", "-k", "gui/501/com.hamzalabs.maktaba.server"]
        );
    }

    #[test]
    fn mac_logs_go_under_library_logs() {
        assert_eq!(
            mac_logs_dir(Path::new("/Users/me")),
            Path::new("/Users/me/Library/Logs/Maktaba")
        );
    }

    #[test]
    fn rotate_keeps_one_old_log() {
        let tmp = tempfile::tempdir().unwrap();
        let log = tmp.path().join("server.log");
        std::fs::write(&log, b"0123456789").unwrap();
        rotate(&log, 100);
        assert!(log.exists());
        rotate(&log, 5);
        assert!(!log.exists());
        assert_eq!(
            std::fs::read(tmp.path().join("server.log.1")).unwrap(),
            b"0123456789"
        );
    }

    #[test]
    fn the_log_tail_starts_at_a_whole_line() {
        let tmp = tempfile::tempdir().unwrap();
        let log = tmp.path().join("server.log");
        assert_eq!(read_tail(&log, 10), None);
        std::fs::write(&log, "first line\nsecond\nthird\n").unwrap();
        assert_eq!(
            read_tail(&log, 100).as_deref(),
            Some("first line\nsecond\nthird\n")
        );
        // 12 bytes reach into "second": that cut line is dropped
        assert_eq!(read_tail(&log, 12).as_deref(), Some("third\n"));
    }

    #[test]
    fn the_shipped_plist_matches_the_code() {
        let plist = include_str!("../LaunchAgents/com.hamzalabs.maktaba.server.plist")
            .replace("\r\n", "\n");
        assert!(plist.contains(&format!("<key>Label</key>\n\t<string>{LABEL}</string>")));
        assert!(
            plist.contains("<key>BundleProgram</key>\n\t<string>Contents/MacOS/Maktaba</string>")
        );
        assert!(plist.contains(&format!("<string>{AGENT_FLAG}</string>")));
        // KeepAlive restarts a crash but not a clean stop (Pause)
        assert!(plist.contains("<key>SuccessfulExit</key>\n\t\t<false/>"));
        assert!(plist.contains("<key>RunAtLoad</key>\n\t<true/>"));
        // launchd waits out the launcher's 60 s stop grace before SIGKILL
        assert!(plist.contains("<key>ExitTimeOut</key>\n\t<integer>90</integer>"));
    }
}
