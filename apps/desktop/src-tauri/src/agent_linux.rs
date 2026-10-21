// The server on Linux (Epic 39 story 39.8, specs/epics/39-linux-app/
// contract-runtime.md).
//
// The server never runs from this app's files: an AppImage is mounted at a new
// /tmp/.mount_* path on every run, and a .deb/.rpm upgrade replaces
// /usr/lib/Maktaba under a running server. It runs from a copy in
// ~/.local/share/maktaba/runtime/<version> (`current`), as a systemd user
// unit started at boot by linger. The app never writes the unit or the copy
// itself: it runs the payload's own `maktaba-server runtime …` and
// `maktaba-server service …` (`--json`, one result or error line), the same
// commands the terminal setup of a machine without a desktop uses.
//
// Where no StatusNotifier host is running (GNOME without the AppIndicator
// extension), there is no tray to return to: closing the window quits the
// app (the server keeps running), and there is no tray autostart entry.
#![cfg_attr(not(target_os = "linux"), allow(dead_code))]

use std::path::{Path, PathBuf};
use std::process::Command;

use serde::Deserialize;
use serde_json::Value;

use super::{Action, BundlePaths, LoginItem};

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

/// This app's payload, when the build carries one.
fn payload() -> Option<BundlePaths> {
    std::env::current_exe()
        .ok()
        .and_then(|e| BundlePaths::from_linux_exe(&e))
        .filter(BundlePaths::payload_present)
}

/// The maktaba-server to ask: this app's payload's, else the runtime's (a
/// client-only build still finds a server installed by another). A payload
/// binary without its executable bit (an .rpm's files keep the packager's
/// modes) is run from a copy in the cache that has it.
fn server() -> Result<PathBuf, String> {
    if let Some(p) = payload() {
        return runnable(&p.server_bin());
    }
    let h = home().ok_or("HOME is not set")?;
    let rt = data_dir(env, &h)
        .join("runtime")
        .join("current")
        .join("bin")
        .join("maktaba-server");
    if rt.is_file() {
        return Ok(rt);
    }
    Err("this build of Maktaba does not include the server".into())
}

#[cfg(not(unix))]
fn runnable(bin: &Path) -> Result<PathBuf, String> {
    Ok(bin.to_path_buf())
}

#[cfg(unix)]
fn runnable(bin: &Path) -> Result<PathBuf, String> {
    use std::os::unix::fs::PermissionsExt;
    let meta = std::fs::metadata(bin).map_err(|e| format!("{}: {e}", bin.display()))?;
    if meta.permissions().mode() & 0o111 != 0 {
        return Ok(bin.to_path_buf());
    }
    let cache = env("XDG_CACHE_HOME")
        .filter(|v| Path::new(v).is_absolute())
        .map(PathBuf::from)
        .or_else(|| home().map(|h| h.join(".cache")))
        .ok_or("HOME is not set")?;
    let copy = cache
        .join("maktaba")
        .join("bin")
        .join(format!("maktaba-server-{}", meta.len()));
    if let Some(dir) = copy.parent() {
        std::fs::create_dir_all(dir).map_err(|e| e.to_string())?;
    }
    std::fs::copy(bin, &copy).map_err(|e| format!("{}: {e}", copy.display()))?;
    std::fs::set_permissions(&copy, std::fs::Permissions::from_mode(0o755))
        .map_err(|e| e.to_string())?;
    Ok(copy)
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
    let bin = server()?;
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

/// Copy this app's payload into the runtime (when it is not there yet), point
/// `current` at it, write and start the unit, and ask for linger. Linger
/// refused (no polkit agent) leaves a server that runs while the user is
/// logged in: login_item says RequiresApproval, and setup offers it again.
pub fn register() -> Result<(), String> {
    install_runtime()?;
    run_json(&["service", "install"])?;
    let _ = run_json(&["service", "linger"]);
    if tray_available() {
        let _ = write_autostart();
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

/// After an app update (or at any start): when this app's payload is newer
/// than the runtime the unit runs, install it and switch the unit to it (the
/// server stops while `current` moves; the supervisor's next start backs up,
/// migrates and rebuilds the venv as the payload needs). Returns whether it
/// switched.
pub fn sync_runtime() -> Result<bool, String> {
    let Some(p) = payload() else {
        return Ok(false); // a client-only build carries none
    };
    let from = p.server_dir.to_string_lossy().to_string();
    let st = run_json(&["runtime", "status", "--from", &from])?;
    if st.get("update_pending").and_then(Value::as_bool) != Some(true) {
        return Ok(false);
    }
    install_runtime()?;
    Ok(true)
}

/// Copy this app's payload into the runtime and switch the unit to it.
fn install_runtime() -> Result<(), String> {
    let p = payload().ok_or("this build of Maktaba does not include the server")?;
    let from = p.server_dir.to_string_lossy().to_string();
    run_json(&[
        "runtime",
        "install",
        "--from",
        &from,
        "--activate",
        "--owner",
        "app",
    ])
    .map(|_| ())
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

/// The tray's autostart entry (the XDG autostart spec).
fn autostart_path() -> Option<PathBuf> {
    let base = env("XDG_CONFIG_HOME")
        .filter(|v| Path::new(v).is_absolute())
        .map(PathBuf::from)
        .or_else(|| home().map(|h| h.join(".config")))?;
    Some(base.join("autostart").join("maktaba.desktop"))
}

/// The program the entry starts: the AppImage file ($APPIMAGE, set by its
/// runtime; the mount point changes every run), else this executable.
fn app_program() -> Option<PathBuf> {
    env("APPIMAGE")
        .map(PathBuf::from)
        .or_else(|| std::env::current_exe().ok())
}

/// The entry's text.
pub fn autostart_entry(program: &Path) -> String {
    format!(
        "[Desktop Entry]\nType=Application\nName=Maktaba\n\
         Comment=The Maktaba server's status in the tray\n\
         Exec={} {HIDDEN_FLAG}\nTerminal=false\nNoDisplay=true\n\
         X-GNOME-Autostart-enabled=true\n",
        exec_quote(&program.to_string_lossy())
    )
}

/// A word of a desktop entry's Exec key: quoted when it needs it, with `"`,
/// `` ` ``, `$` and `\` escaped inside the quotes, and `%` doubled (a field
/// code otherwise).
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

pub fn write_autostart() -> Result<(), String> {
    let path = autostart_path().ok_or("HOME is not set")?;
    let program = app_program().ok_or("cannot find this program")?;
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir).map_err(|e| e.to_string())?;
    }
    std::fs::write(&path, autostart_entry(&program)).map_err(|e| e.to_string())
}

pub fn remove_autostart() {
    if let Some(p) = autostart_path() {
        let _ = std::fs::remove_file(p);
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
        let e = autostart_entry(Path::new("/home/me/Applications/Maktaba.AppImage"));
        assert!(e.contains("Exec=/home/me/Applications/Maktaba.AppImage --hidden\n"));
        assert!(e.starts_with("[Desktop Entry]\nType=Application\n"));
        assert_eq!(exec_quote("/home/my apps/M"), "\"/home/my apps/M\"");
        assert_eq!(exec_quote("/a$b"), "\"/a\\$b\"");
        assert_eq!(exec_quote("/100%"), "/100%%");
        assert_eq!(exec_quote("/x\"y"), "\"/x\\\"y\"");
    }
}
