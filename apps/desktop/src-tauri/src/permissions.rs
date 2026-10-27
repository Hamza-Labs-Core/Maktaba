// What the Mac server app needs from macOS, and whether it has it (the
// permissions walkthrough): the setup's Permissions step and the reminder in
// this Mac's own web app read it with `host_permissions`, open the exact
// System Settings pane with `open_privacy_pane` (an allow-listed pane, never
// an arbitrary URL) and remember the walkthrough was shown with
// `permissions_seen`.
//
// Only what the server really needs:
// - Full Disk Access: the server reads the library folders, a NAS share and
//   the folders macOS protects (tools/release/mac-app/payload.sh). Without it
//   a read fails with EPERM ("operation not permitted"). A grant applies to
//   the processes started after it: the server is restarted (Pause, Resume).
// - Local Network: Plex, a NAS by its address, the joined machines. macOS
//   lists the server as "maktaba-launcher" and tells no app whether it is
//   allowed: the state is unknown (the API's System health names a "no route
//   to host" to a local address instead).
// - The login item (SMAppService, agent.rs): the server starts at login.
// - Keeping the Mac awake (optional): nothing holds a sleep assertion, so a
//   Mac that sleeps stops serving. Read from `pmset -g`.
// Notifications are not listed: only an update's notice uses them, and the
// menu bar says the same.
use std::io;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};
use tauri::AppHandle;

use crate::agent::{self, LoginItem};

/// A grant's state.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Grant {
    Granted,
    Denied,
    NotAsked,
    Unknown,
}

/// The System Settings panes the app may open (the web sends one of these,
/// never a URL).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Pane {
    FullDiskAccess,
    LocalNetwork,
    LoginItems,
    Energy,
}

impl Pane {
    pub fn url(self) -> &'static str {
        match self {
            Pane::FullDiskAccess => {
                "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"
            }
            Pane::LocalNetwork => {
                "x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork"
            }
            Pane::LoginItems => "x-apple.systempreferences:com.apple.LoginItems-Settings.extension",
            Pane::Energy => "x-apple.systempreferences:com.apple.Energy-Settings.extension",
        }
    }
}

/// One thing the server needs.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Item {
    /// full_disk_access, local_network, login_item, keep_awake: the web's
    /// words (perm.item.<id>.*)
    pub id: &'static str,
    pub required: bool,
    pub state: Grant,
    pub pane: Pane,
}

#[derive(Debug, Clone, Serialize)]
pub struct Report {
    pub platform: &'static str,
    /// this Mac runs the server (the reminder shows only then)
    pub server_mode: bool,
    /// the walkthrough was shown once
    pub seen: bool,
    /// empty off a Mac
    pub items: Vec<Item>,
}

/// Full Disk Access from opening a file only it opens: the user's TCC
/// database. Denied (EPERM) without it; not found says nothing.
pub fn full_disk_access_from(open: io::Result<()>) -> Grant {
    match open {
        Ok(()) => Grant::Granted,
        Err(e) if e.kind() == io::ErrorKind::PermissionDenied => Grant::Denied,
        Err(_) => Grant::Unknown,
    }
}

/// The files only Full Disk Access opens, in the order tried.
fn protected_files(home: &Path) -> [PathBuf; 2] {
    [
        home.join("Library/Application Support/com.apple.TCC/TCC.db"),
        home.join("Library/Safari/Bookmarks.plist"),
    ]
}

fn full_disk_access(home: Option<&Path>) -> Grant {
    let Some(home) = home else {
        return Grant::Unknown;
    };
    for f in protected_files(home) {
        let g = full_disk_access_from(std::fs::File::open(&f).map(|_| ()));
        if g != Grant::Unknown {
            return g;
        }
    }
    Grant::Unknown
}

pub fn login_grant(item: LoginItem) -> Option<Grant> {
    match item {
        LoginItem::Enabled => Some(Grant::Granted),
        LoginItem::RequiresApproval => Some(Grant::Denied),
        LoginItem::NotRegistered => Some(Grant::NotAsked),
        LoginItem::NotFound => Some(Grant::Unknown),
        LoginItem::Unsupported => None,
    }
}

/// The minutes after which the system sleeps (`pmset -g`'s " sleep N" line;
/// 0: never).
pub fn pmset_sleep_minutes(out: &str) -> Option<u32> {
    out.lines().find_map(|l| {
        let mut w = l.split_whitespace();
        (w.next()? == "sleep").then_some(())?;
        w.next()?.parse().ok()
    })
}

pub fn keep_awake_from(minutes: Option<u32>) -> Grant {
    match minutes {
        Some(0) => Grant::Granted,
        Some(_) => Grant::Denied,
        None => Grant::Unknown,
    }
}

fn keep_awake() -> Grant {
    let out = std::process::Command::new("/usr/bin/pmset")
        .arg("-g")
        .output()
        .ok()
        .filter(|o| o.status.success());
    keep_awake_from(out.and_then(|o| pmset_sleep_minutes(&String::from_utf8_lossy(&o.stdout))))
}

/// The items, from what was read.
pub fn items(fda: Grant, login: Option<Grant>, awake: Grant) -> Vec<Item> {
    let mut v = vec![
        Item {
            id: "full_disk_access",
            required: true,
            state: fda,
            pane: Pane::FullDiskAccess,
        },
        Item {
            id: "local_network",
            required: true,
            state: Grant::Unknown,
            pane: Pane::LocalNetwork,
        },
    ];
    if let Some(state) = login {
        v.push(Item {
            id: "login_item",
            required: true,
            state,
            pane: Pane::LoginItems,
        });
    }
    v.push(Item {
        id: "keep_awake",
        required: false,
        state: awake,
        pane: Pane::Energy,
    });
    v
}

fn seen_path() -> Option<PathBuf> {
    Some(agent::state_root()?.join("state").join("permissions-seen"))
}

pub fn report(server_mode: bool) -> Report {
    let platform = std::env::consts::OS;
    let seen = seen_path().is_some_and(|p| p.is_file());
    let items = if cfg!(target_os = "macos") {
        let home = std::env::var_os("HOME").map(PathBuf::from);
        items(
            full_disk_access(home.as_deref()),
            login_grant(agent::login_item()),
            keep_awake(),
        )
    } else {
        Vec::new()
    };
    Report {
        platform,
        server_mode,
        seen,
        items,
    }
}

#[tauri::command(async)]
pub fn host_permissions(app: AppHandle) -> Report {
    report(crate::shell::server_mode(&app))
}

#[tauri::command(async)]
pub fn open_privacy_pane(pane: Pane) -> Result<(), String> {
    if !cfg!(target_os = "macos") {
        return Err("System Settings is a Mac's".into());
    }
    agent::open_path(std::ffi::OsStr::new(pane.url()))
}

#[tauri::command(async)]
pub fn permissions_seen() -> Result<(), String> {
    let p = seen_path().ok_or("cannot find the home folder")?;
    if let Some(dir) = p.parent() {
        std::fs::create_dir_all(dir).map_err(|e| e.to_string())?;
    }
    std::fs::write(&p, "shown\n").map_err(|e| e.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn full_disk_access_is_read_from_the_open() {
        assert_eq!(full_disk_access_from(Ok(())), Grant::Granted);
        #[cfg(unix)]
        {
            let eperm = io::Error::from_raw_os_error(1); // EPERM: what TCC answers
            assert_eq!(full_disk_access_from(Err(eperm)), Grant::Denied);
        }
        let eacces = io::Error::from(io::ErrorKind::PermissionDenied);
        assert_eq!(full_disk_access_from(Err(eacces)), Grant::Denied);
        let missing = io::Error::from(io::ErrorKind::NotFound);
        assert_eq!(full_disk_access_from(Err(missing)), Grant::Unknown);
    }

    #[test]
    fn full_disk_access_without_the_files_is_unknown() {
        let dir = std::env::temp_dir().join(format!("perm-test-{}", std::process::id()));
        assert_eq!(full_disk_access(Some(&dir)), Grant::Unknown);
        assert_eq!(full_disk_access(None), Grant::Unknown);
        // a readable file stands for the grant
        let f = dir.join("Library/Safari/Bookmarks.plist");
        std::fs::create_dir_all(f.parent().unwrap()).unwrap();
        std::fs::write(&f, "x").unwrap();
        assert_eq!(full_disk_access(Some(&dir)), Grant::Granted);
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn only_the_listed_panes_open() {
        let all = [
            Pane::FullDiskAccess,
            Pane::LocalNetwork,
            Pane::LoginItems,
            Pane::Energy,
        ];
        for p in all {
            assert!(p.url().starts_with("x-apple.systempreferences:com.apple."));
        }
        assert!(Pane::FullDiskAccess.url().ends_with("?Privacy_AllFiles"));
        assert!(Pane::LocalNetwork.url().ends_with("?Privacy_LocalNetwork"));
        // the web names a pane, never a URL
        let p: Pane = serde_json::from_str("\"full_disk_access\"").unwrap();
        assert_eq!(p, Pane::FullDiskAccess);
        assert!(serde_json::from_str::<Pane>("\"https://example.com\"").is_err());
        assert!(serde_json::from_str::<Pane>("\"x-apple.systempreferences:x\"").is_err());
    }

    #[test]
    fn the_login_item_maps_to_a_grant() {
        assert_eq!(login_grant(LoginItem::Enabled), Some(Grant::Granted));
        assert_eq!(
            login_grant(LoginItem::RequiresApproval),
            Some(Grant::Denied)
        );
        assert_eq!(login_grant(LoginItem::NotRegistered), Some(Grant::NotAsked));
        assert_eq!(login_grant(LoginItem::NotFound), Some(Grant::Unknown));
        assert_eq!(login_grant(LoginItem::Unsupported), None);
    }

    #[test]
    fn pmset_says_when_the_mac_sleeps() {
        let out = "System-wide power settings:\nCurrently in use:\n standby              1\n \
                   displaysleep         10\n sleep                1 (sleep prevented by coreaudiod)\n \
                   disksleep            10\n";
        assert_eq!(pmset_sleep_minutes(out), Some(1));
        assert_eq!(keep_awake_from(pmset_sleep_minutes(out)), Grant::Denied);
        let never = "Currently in use:\n displaysleep 10\n sleep 0\n";
        assert_eq!(keep_awake_from(pmset_sleep_minutes(never)), Grant::Granted);
        assert_eq!(
            keep_awake_from(pmset_sleep_minutes("garbage")),
            Grant::Unknown
        );
    }

    #[test]
    fn the_items_are_what_the_server_needs() {
        let v = items(Grant::Denied, Some(Grant::Granted), Grant::Unknown);
        let ids: Vec<_> = v.iter().map(|i| i.id).collect();
        assert_eq!(
            ids,
            [
                "full_disk_access",
                "local_network",
                "login_item",
                "keep_awake"
            ]
        );
        assert!(v[0].required && v[1].required && v[2].required && !v[3].required);
        assert_eq!(v[1].state, Grant::Unknown, "macOS tells no app");
        let json = serde_json::to_value(&v[0]).unwrap();
        assert_eq!(
            json,
            serde_json::json!({"id": "full_disk_access", "required": true,
                "state": "denied", "pane": "full_disk_access"})
        );
        let no_login = items(Grant::Granted, None, Grant::Granted);
        assert!(no_login.iter().all(|i| i.id != "login_item"));
    }
}
