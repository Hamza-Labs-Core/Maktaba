// The server login item (Epic 36, story 36.5).
//
// Maktaba.app ships a LaunchAgent plist at
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
use std::path::{Path, PathBuf};
use std::process::Command;

pub const LABEL: &str = "com.hamzalabs.maktaba.server";
pub const PLIST_NAME: &str = "com.hamzalabs.maktaba.server.plist";
/// argv[1] that turns the app executable into the server agent.
pub const AGENT_FLAG: &str = "--server-agent";
/// server.log is rotated to server.log.1 when it grows past this.
const LOG_ROTATE_BYTES: u64 = 50 << 20;

/// Where things are inside Maktaba.app, from the app executable's path
/// (…/Maktaba.app/Contents/MacOS/Maktaba).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct BundlePaths {
    /// Contents/Resources/server
    pub server_dir: PathBuf,
}

impl BundlePaths {
    pub fn from_exe(exe: &Path) -> Option<Self> {
        let contents = exe.parent()?.parent()?;
        if contents.file_name()? != "Contents" {
            return None;
        }
        Some(Self {
            server_dir: contents.join("Resources").join("server"),
        })
    }

    pub fn launcher(&self) -> PathBuf {
        self.server_dir.join("bin").join("maktaba-launcher")
    }

    pub fn server_bin(&self) -> PathBuf {
        self.server_dir.join("bin").join("maktaba-server")
    }

    /// The bundle carries a server payload (a client-only build does not).
    pub fn payload_present(&self) -> bool {
        self.server_bin().is_file()
    }
}

/// ~/Library/Logs/Maktaba
pub fn logs_dir(home: &Path) -> PathBuf {
    home.join("Library").join("Logs").join("Maktaba")
}

fn home() -> Option<PathBuf> {
    std::env::var_os("HOME").map(PathBuf::from)
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

/// `Maktaba --server-agent`: become the launcher. Returns only on error.
#[cfg(unix)]
pub fn run_server_agent() -> String {
    use std::os::unix::process::CommandExt;

    let exe = match std::env::current_exe() {
        Ok(p) => p,
        Err(e) => return format!("cannot find the app executable: {e}"),
    };
    let Some(paths) = BundlePaths::from_exe(&exe) else {
        return format!("{} is not inside Maktaba.app/Contents/MacOS", exe.display());
    };
    let Some(home) = home() else {
        return "HOME is not set".into();
    };
    let logs = logs_dir(&home);
    if let Err(e) = std::fs::create_dir_all(&logs) {
        return format!("cannot create {}: {e}", logs.display());
    }
    let log_path = logs.join("server.log");
    rotate(&log_path, LOG_ROTATE_BYTES);
    let log = match std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)
    {
        Ok(f) => f,
        Err(e) => return format!("cannot open {}: {e}", log_path.display()),
    };
    let log2 = match log.try_clone() {
        Ok(f) => f,
        Err(e) => return format!("cannot open {}: {e}", log_path.display()),
    };
    let err = Command::new(paths.launcher())
        .arg("serve")
        .stdout(log)
        .stderr(log2)
        .exec();
    format!("cannot start {}: {err}", paths.launcher().display())
}

#[cfg(not(unix))]
pub fn run_server_agent() -> String {
    "the server agent runs on macOS only".into()
}

/// Whether the login item is registered (and allowed) with launchd.
#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize)]
#[serde(rename_all = "snake_case")]
pub enum LoginItem {
    NotRegistered,
    Enabled,
    /// Registered, but the user turned it off in System Settings → Login Items.
    RequiresApproval,
    /// The plist is not in the bundle (a dev build run outside Maktaba.app).
    NotFound,
    #[cfg_attr(target_os = "macos", allow(dead_code))]
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

#[cfg(not(target_os = "macos"))]
pub fn login_item() -> LoginItem {
    LoginItem::Unsupported
}
#[cfg(not(target_os = "macos"))]
pub fn register() -> Result<(), String> {
    Err("the server login item is macOS only".into())
}
#[cfg(not(target_os = "macos"))]
pub fn unregister() -> Result<(), String> {
    Err("the server login item is macOS only".into())
}
#[cfg(not(target_os = "macos"))]
pub fn open_login_items_settings() {}

/// launchctl arguments for an action on the agent (gui/<uid>/<label>).
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
fn uid() -> u32 {
    // SAFETY: getuid cannot fail.
    unsafe { libc::getuid() }
}

#[cfg(not(unix))]
fn uid() -> u32 {
    0
}

/// Pause, resume or restart the server through launchd.
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

/// Show ~/Library/Logs/Maktaba in Finder (created if missing).
pub fn open_logs() -> Result<(), String> {
    let dir = logs_dir(&home().ok_or("HOME is not set")?);
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    Command::new("/usr/bin/open")
        .arg(&dir)
        .spawn()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bundle_paths_from_the_app_executable() {
        let p = BundlePaths::from_exe(Path::new(
            "/Applications/Maktaba.app/Contents/MacOS/Maktaba",
        ))
        .unwrap();
        assert_eq!(
            p.server_dir,
            Path::new("/Applications/Maktaba.app/Contents/Resources/server")
        );
        assert_eq!(
            p.launcher(),
            Path::new("/Applications/Maktaba.app/Contents/Resources/server/bin/maktaba-launcher")
        );
        assert_eq!(
            p.server_bin(),
            Path::new("/Applications/Maktaba.app/Contents/Resources/server/bin/maktaba-server")
        );
        // `cargo run` / `tauri dev` runs from target/, not a bundle
        assert_eq!(
            BundlePaths::from_exe(Path::new("/src/target/debug/maktaba-desktop")),
            None
        );
    }

    #[test]
    fn payload_present_needs_the_server_binary() {
        let tmp = tempfile::tempdir().unwrap();
        let exe = tmp.path().join("Maktaba.app/Contents/MacOS/Maktaba");
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
    fn logs_go_under_library_logs() {
        assert_eq!(
            logs_dir(Path::new("/Users/me")),
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
        let plist = include_str!("../LaunchAgents/com.hamzalabs.maktaba.server.plist");
        assert!(plist.contains(&format!("<key>Label</key>\n\t<string>{LABEL}</string>")));
        assert!(
            plist.contains("<key>BundleProgram</key>\n\t<string>Contents/MacOS/Maktaba</string>")
        );
        assert!(plist.contains(&format!("<string>{AGENT_FLAG}</string>")));
        // KeepAlive restarts a crash but not a clean stop (Pause)
        assert!(plist.contains("<key>SuccessfulExit</key>\n\t\t<false/>"));
        assert!(plist.contains("<key>RunAtLoad</key>\n\t<true/>"));
    }
}
