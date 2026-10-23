// The home's order to install a release (Epic 35, install-kinds.md "Ask first", owner
// decision D1): a desktop app that runs the server (the home, or a full node) installs
// the release the home's update ordered only when someone clicks here. The server
// writes <state>/updates/app-install.json ({run_id, version, feed, kind, home, state:
// ordered}); this app's status poller reads it, stages the release (kind app: its own
// update from the release's own feed, releases/download/v<version>/latest.json, so the
// channel, pin and skip the server applied hold; downloaded and checked with this app's
// key, kept in updates/app-update-<version>.bin; kind runtime: nothing to fetch, the
// server installs its own runtime) and shows it in the tray (staged). A click says
// clicked; the server answers go once the home's run is ready (the home drains its
// workers first; a machine waits its turn), and this app installs what it staged
// (checked again) and says how it went (installing, failed with the error). Pure
// decisions live here; shell.rs runs the updater.
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

/// The order's file name in the state folder's updates directory.
pub const REQUEST_FILE: &str = "app-install.json";

/// The deep link the server opens the app with when it does not answer the order (a
/// running app takes it too: the poller then sees the order at once).
pub const INSTALL_URL: &str = "maktaba://install-update";

/// The order's states (the server writes ordered and go, this app the others).
pub const ORDERED: &str = "ordered";
pub const STAGED: &str = "staged";
pub const CLICKED: &str = "clicked";
pub const GO: &str = "go";
pub const INSTALLING: &str = "installing";
pub const FAILED: &str = "failed";

/// The order's kind: this app's updater installs it (Mac, Windows), or the server
/// installs it in its runtime (the Linux app).
pub const KIND_RUNTIME: &str = "runtime";

/// app-install.json.
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct Request {
    pub run_id: String,
    pub version: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub feed: String,
    #[serde(default)]
    pub kind: String,
    /// This machine is the home (else the cluster's update waits for it).
    #[serde(default)]
    pub home: bool,
    pub state: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(default)]
    pub at: String,
}

impl Request {
    /// This app's updater installs it (not the server in its runtime).
    pub fn by_app(&self) -> bool {
        self.kind != KIND_RUNTIME
    }
}

/// The order's path under the state folder.
pub fn request_path(state_root: &Path) -> PathBuf {
    state_root.join("updates").join(REQUEST_FILE)
}

/// Where the staged update of version is kept.
pub fn staged_path(state_root: &Path, version: &str) -> PathBuf {
    let v: String = version
        .strip_prefix('v')
        .unwrap_or(version)
        .chars()
        .filter(|c| c.is_ascii_alphanumeric() || *c == '.' || *c == '-')
        .collect();
    state_root
        .join("updates")
        .join(format!("app-update-{v}.bin"))
}

/// Removes staged updates other than keep's (an order withdrawn, or installed).
pub fn clean_staged(state_root: &Path, keep: Option<&Path>) {
    let Ok(dir) = std::fs::read_dir(state_root.join("updates")) else {
        return;
    };
    for e in dir.flatten() {
        let name = e.file_name().to_string_lossy().to_string();
        if name.starts_with("app-update-") && Some(e.path().as_path()) != keep {
            let _ = std::fs::remove_file(e.path());
        }
    }
}

/// Reads the order (None: none, or one that cannot be read).
pub fn read(path: &Path) -> Option<Request> {
    let raw = std::fs::read(path).ok()?;
    serde_json::from_slice(&raw).ok()
}

/// What the app does about an order.
#[derive(Debug, PartialEq)]
pub enum Action {
    /// Nothing to do now (none, not for this app, under way, or waiting for a click).
    Nothing,
    /// Download this release's app update from this feed, check it and keep it.
    Stage { version: String, feed: String },
    /// Show it: nothing to fetch (the server installs it, or it is staged already).
    Show,
    /// Install what was staged (the home said go).
    Install { version: String, feed: String },
    /// The order cannot be served: say so in it.
    Refuse(String),
}

/// decide reads an order for an app that runs the server (server_side) and is not
/// paused; staged: the release is staged here; busy: a stage or an install is under way.
pub fn decide(
    req: Option<&Request>,
    server_side: bool,
    paused: bool,
    staged: bool,
    busy: bool,
) -> Action {
    let Some(req) = req else {
        return Action::Nothing;
    };
    if busy || !server_side || !matches!(req.state.as_str(), ORDERED | STAGED | GO) {
        return Action::Nothing;
    }
    if !valid_version(&req.version) {
        return Action::Refuse(format!("bad version {:?}", req.version));
    }
    if req.by_app() && !trusted_feed(&req.feed, &req.version) {
        return Action::Refuse(format!("feed {:?} is not the release's own", req.feed));
    }
    let stage = Action::Stage {
        version: req.version.clone(),
        feed: req.feed.clone(),
    };
    match req.state.as_str() {
        ORDERED if req.by_app() && !staged => stage,
        ORDERED => Action::Show,
        // shown already; staged again when this app lost it (restarted, cleaned)
        STAGED if req.by_app() && !staged => stage,
        GO if req.by_app() && paused => {
            // the user paused the server here: an install would start it
            Action::Refuse("the server is paused on this machine".into())
        }
        GO if req.by_app() => Action::Install {
            version: req.version.clone(),
            feed: req.feed.clone(),
        },
        _ => Action::Nothing,
    }
}

/// What the tray shows of an order.
#[derive(Debug, Clone, PartialEq)]
pub enum Shown {
    /// Ready: a click installs it (the home's own, or one the cluster waits for).
    Ready { version: String, home: bool },
    /// Clicked (or installing): it goes when the cluster is ready.
    Clicked { version: String },
}

/// shown is what the tray says of an order (None: nothing to show yet, or over).
pub fn shown(req: Option<&Request>) -> Option<Shown> {
    let req = req?;
    let version = req
        .version
        .strip_prefix('v')
        .unwrap_or(&req.version)
        .to_string();
    match req.state.as_str() {
        STAGED => Some(Shown::Ready {
            version,
            home: req.home,
        }),
        CLICKED | GO | INSTALLING => Some(Shown::Clicked { version }),
        _ => None,
    }
}

/// A plain release version (1.2.3, 1.2.3-rc.1), with or without its v.
pub fn valid_version(v: &str) -> bool {
    let v = v.strip_prefix('v').unwrap_or(v);
    let (core, pre) = match v.split_once('-') {
        Some((c, p)) => (c, Some(p)),
        None => (v, None),
    };
    let parts: Vec<&str> = core.split('.').collect();
    parts.len() == 3
        && parts
            .iter()
            .all(|p| !p.is_empty() && p.chars().all(|c| c.is_ascii_digit()))
        && pre.map_or(true, |p| {
            !p.is_empty()
                && p.chars()
                    .all(|c| c.is_ascii_alphanumeric() || c == '.' || c == '-')
        })
}

/// The feed must be the release's own latest.json on GitHub (the app checks every
/// update's signature with its own key anyway; this keeps an order from pointing it at
/// another release than the one the server verified).
pub fn trusted_feed(feed: &str, version: &str) -> bool {
    let tag = format!("v{}", version.strip_prefix('v').unwrap_or(version));
    feed.starts_with("https://github.com/")
        && feed.ends_with(&format!("/releases/download/{tag}/latest.json"))
}

/// The same version, with or without its v.
pub fn same_version(a: &str, b: &str) -> bool {
    a.strip_prefix('v').unwrap_or(a) == b.strip_prefix('v').unwrap_or(b)
}

/// Writes the order back with a new state (and the error), atomically; only while it is
/// still the same run's (a newer order or a withdrawn one is left alone).
pub fn write_state(path: &Path, req: &Request, state: &str, error: &str) -> std::io::Result<()> {
    if read(path).map(|r| r.run_id) != Some(req.run_id.clone()) {
        return Ok(());
    }
    let mut r = req.clone();
    r.state = state.to_string();
    r.error = error.to_string();
    let raw = serde_json::to_vec_pretty(&r).map_err(std::io::Error::other)?;
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, raw)?;
    std::fs::rename(&tmp, path)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn req(state: &str, version: &str, feed: &str) -> Request {
        Request {
            run_id: "r1".into(),
            version: version.into(),
            feed: feed.into(),
            state: state.into(),
            ..Default::default()
        }
    }

    const FEED: &str =
        "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.5.0/latest.json";

    fn stage() -> Action {
        Action::Stage {
            version: "1.5.0".into(),
            feed: FEED.into(),
        }
    }

    #[test]
    fn stages_what_the_home_orders_and_installs_only_at_its_go() {
        let ordered = req(ORDERED, "1.5.0", FEED);
        assert_eq!(decide(Some(&ordered), true, false, false, false), stage());
        // staged already (before a restart of this app): shown
        assert_eq!(
            decide(Some(&ordered), true, false, true, false),
            Action::Show
        );
        // shown: nothing until the click and the home's go
        let staged = req(STAGED, "1.5.0", FEED);
        assert_eq!(
            decide(Some(&staged), true, false, true, false),
            Action::Nothing
        );
        assert_eq!(decide(Some(&staged), true, false, false, false), stage());
        assert_eq!(
            decide(Some(&req(CLICKED, "1.5.0", FEED)), true, false, true, false),
            Action::Nothing
        );
        assert_eq!(
            decide(Some(&req(GO, "1.5.0", FEED)), true, false, true, false),
            Action::Install {
                version: "1.5.0".into(),
                feed: FEED.into()
            }
        );
        assert_eq!(
            decide(Some(&ordered), false, false, false, false),
            Action::Nothing
        ); // client mode
        assert_eq!(
            decide(Some(&ordered), true, false, false, true),
            Action::Nothing
        ); // one at a time
        assert_eq!(decide(None, true, false, false, false), Action::Nothing);
        for state in [INSTALLING, FAILED] {
            assert_eq!(
                decide(Some(&req(state, "1.5.0", FEED)), true, false, true, false),
                Action::Nothing
            );
        }
    }

    #[test]
    fn the_linux_runtime_is_shown_and_left_to_the_server() {
        let mut r = req(ORDERED, "1.5.0", "");
        r.kind = KIND_RUNTIME.into();
        assert_eq!(decide(Some(&r), true, false, false, false), Action::Show);
        r.state = GO.into();
        assert_eq!(decide(Some(&r), true, false, false, false), Action::Nothing);
    }

    #[test]
    fn refuses_a_paused_server_and_strange_orders() {
        assert!(matches!(
            decide(Some(&req(GO, "1.5.0", FEED)), true, true, true, false),
            Action::Refuse(_)
        ));
        assert!(matches!(
            decide(Some(&req(ORDERED, "1.5", FEED)), true, false, false, false),
            Action::Refuse(_)
        ));
        // a feed of another release, or another host
        for feed in [
            "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.4.0/latest.json",
            "https://evil.example/releases/download/v1.5.0/latest.json",
            "",
        ] {
            assert!(matches!(
                decide(
                    Some(&req(ORDERED, "1.5.0", feed)),
                    true,
                    false,
                    false,
                    false
                ),
                Action::Refuse(_)
            ));
        }
    }

    #[test]
    fn the_tray_says_who_waits() {
        let mut r = req(STAGED, "v1.5.0", FEED);
        assert_eq!(
            shown(Some(&r)),
            Some(Shown::Ready {
                version: "1.5.0".into(),
                home: false
            })
        );
        r.home = true;
        assert!(matches!(
            shown(Some(&r)),
            Some(Shown::Ready { home: true, .. })
        ));
        for state in [CLICKED, GO, INSTALLING] {
            r.state = state.into();
            assert_eq!(
                shown(Some(&r)),
                Some(Shown::Clicked {
                    version: "1.5.0".into()
                })
            );
        }
        for state in [ORDERED, FAILED] {
            r.state = state.into();
            assert_eq!(shown(Some(&r)), None);
        }
    }

    #[test]
    fn versions() {
        for v in ["1.5.0", "v1.5.0", "0.3.0-rc.3"] {
            assert!(valid_version(v), "{v}");
        }
        for v in ["", "1.5", "1.5.0.1", "1.x.0", "1.5.0-", "1.5.0-a b"] {
            assert!(!valid_version(v), "{v}");
        }
        assert!(same_version("v1.5.0", "1.5.0"));
        assert!(!same_version("1.5.1", "1.5.0"));
    }

    #[test]
    fn writes_the_answer_back_while_the_order_stands() {
        let dir = tempfile::tempdir().unwrap();
        let path = request_path(dir.path());
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        let r = req(ORDERED, "1.5.0", FEED);
        std::fs::write(&path, serde_json::to_vec(&r).unwrap()).unwrap();
        write_state(&path, &r, FAILED, "signature mismatch").unwrap();
        let got = read(&path).unwrap();
        assert_eq!(got.state, FAILED);
        assert_eq!(got.error, "signature mismatch");
        assert_eq!(got.run_id, "r1");
        // a newer order in its place is left alone
        let mut newer = req(ORDERED, "1.6.0", FEED);
        newer.run_id = "r2".into();
        std::fs::write(&path, serde_json::to_vec(&newer).unwrap()).unwrap();
        write_state(&path, &r, CLICKED, "").unwrap();
        assert_eq!(read(&path).unwrap(), newer);
        // a withdrawn one stays withdrawn
        std::fs::remove_file(&path).unwrap();
        write_state(&path, &r, CLICKED, "").unwrap();
        assert!(read(&path).is_none());
    }

    #[test]
    fn staged_updates_are_named_by_version_and_cleaned() {
        let dir = tempfile::tempdir().unwrap();
        let keep = staged_path(dir.path(), "v1.5.0");
        assert!(keep.ends_with("updates/app-update-1.5.0.bin"));
        std::fs::create_dir_all(keep.parent().unwrap()).unwrap();
        let old = staged_path(dir.path(), "1.4.9");
        std::fs::write(&keep, b"x").unwrap();
        std::fs::write(&old, b"x").unwrap();
        clean_staged(dir.path(), Some(&keep));
        assert!(keep.exists() && !old.exists());
        clean_staged(dir.path(), None);
        assert!(!keep.exists());
        // a version cannot name a path outside updates/
        assert!(staged_path(dir.path(), "../../x")
            .parent()
            .unwrap()
            .ends_with("updates"));
    }
}
