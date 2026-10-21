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
// A sign-in task of the user, "\Maktaba Server", runs the same executable
// with `--server-agent`, which starts <install>\server\bin\maktaba-launcher
// in a console without a window and restarts it if it fails. A service is
// not used: it would run in session 0, where the user's mapped NAS drives do
// not exist. Pause sets the named event the launcher watches; Resume runs
// the task again. The installer's hooks call `--server-start`,
// `--server-stop` and `--server-remove`.
//
// Linux (agent_linux.rs, specs/epics/39-linux-app/contract-runtime.md). A
// systemd user unit runs a copy of the payload in ~/.local/share/maktaba/
// runtime (never this app's files: an AppImage's mount moves every run),
// written and controlled through the payload's own `maktaba-server runtime`
// and `maktaba-server service`; linger starts it at boot.
use std::ffi::OsStr;
use std::fs::File;
use std::path::{Path, PathBuf};
#[cfg(not(windows))]
use std::process::Command;

#[cfg(windows)]
#[path = "agent_windows.rs"]
mod win;

// compiled in every test build too: its decisions are tested on every OS
#[cfg(any(target_os = "linux", test))]
#[path = "agent_linux.rs"]
pub mod linux;

pub const LABEL: &str = "com.hamzalabs.maktaba.server";
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub const PLIST_NAME: &str = "com.hamzalabs.maktaba.server.plist";
/// argv[1] that turns the app executable into the server agent.
pub const AGENT_FLAG: &str = "--server-agent";
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

/// Where the server payload is, from the app executable's path:
/// …/Maktaba.app/Contents/MacOS/Maktaba on a Mac, <install>\Maktaba.exe on
/// Windows (the payload is <install>\server beside it), <prefix>/bin/Maktaba
/// on Linux (the payload is <prefix>/lib/Maktaba/server).
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
    home().map(|h| linux::data_dir(env_var, &h))
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

/// The pause flag: <state>/state/server-paused while the user paused the
/// server. A paused server stays paused when the window restarts (an update
/// relaunches it) until Resume, or until the agent starts again at the next
/// sign-in / login, which clears it.
fn pause_flag_path() -> Option<PathBuf> {
    state_root().map(|r| r.join("state").join("server-paused"))
}

pub fn paused_flag() -> bool {
    pause_flag_path().is_some_and(|p| p.is_file())
}

pub fn set_paused_flag(on: bool) {
    let Some(p) = pause_flag_path() else { return };
    if on {
        if let Some(dir) = p.parent() {
            let _ = std::fs::create_dir_all(dir);
        }
        let _ = std::fs::write(&p, b"paused from the Maktaba app\n");
    } else {
        let _ = std::fs::remove_file(&p);
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
        unsafe { service().registerAndReturnError() }
            .map_err(|e| e.localizedDescription().to_string())
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

// Windows: the sign-in task. Turning it on again needs no settings screen:
// open_login_items_settings enables and runs it.
#[cfg(windows)]
pub use win::{
    agent_alive, control, login_item, open_login_items_settings, register, stop_and_wait,
    unregister,
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
