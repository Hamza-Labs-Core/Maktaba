// The server's request to install an app update (Epic 35, install-kinds.md "The app
// shell installs without a click"): a desktop app that runs the server (the home, or a
// full node) installs the release the home's update ordered, in the update's window,
// without a click. The server writes <state>/updates/app-install.json ({run_id,
// version, feed, state: requested}); this app's status poller reads it, installs that
// exact release from its own feed (releases/download/v<version>/latest.json, so the
// channel, pin and skip the server applied hold), and says how it went in the same file
// (installing, failed with the error): the server's run follows it. A successful install
// restarts the server, which reports the rest. Pure decisions live here; shell.rs runs
// the updater.
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

/// The request's file name in the state folder's updates directory.
pub const REQUEST_FILE: &str = "app-install.json";

/// The deep link the server opens the app with when it does not answer the request
/// (a running app takes it too: the poller then sees the request at once).
pub const INSTALL_URL: &str = "maktaba://install-update";

/// app-install.json.
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct Request {
    pub run_id: String,
    pub version: String,
    #[serde(default)]
    pub feed: String,
    pub state: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(default)]
    pub at: String,
}

/// The request's path under the state folder.
pub fn request_path(state_root: &Path) -> PathBuf {
    state_root.join("updates").join(REQUEST_FILE)
}

/// Reads the request (None: none, or one that cannot be read).
pub fn read(path: &Path) -> Option<Request> {
    let raw = std::fs::read(path).ok()?;
    serde_json::from_slice(&raw).ok()
}

/// What the app does about a request.
#[derive(Debug, PartialEq)]
pub enum Action {
    /// Nothing asked, or it is not for this app (client mode, a paused server).
    Nothing,
    /// Install this release from this feed.
    Install { version: String, feed: String },
    /// The request cannot be served: say so in it.
    Refuse(String),
}

/// decide reads a request for an app that runs the server (server_side) and is not
/// paused, while no install is under way here (busy).
pub fn decide(req: Option<&Request>, server_side: bool, paused: bool, busy: bool) -> Action {
    let Some(req) = req else {
        return Action::Nothing;
    };
    if req.state != "requested" || busy || !server_side {
        return Action::Nothing;
    }
    if paused {
        // the user paused the server here: an update would start it
        return Action::Refuse("the server is paused on this machine".into());
    }
    if !valid_version(&req.version) {
        return Action::Refuse(format!("bad version {:?}", req.version));
    }
    if !trusted_feed(&req.feed, &req.version) {
        return Action::Refuse(format!("feed {:?} is not the release's own", req.feed));
    }
    Action::Install {
        version: req.version.clone(),
        feed: req.feed.clone(),
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
                && p
                    .chars()
                    .all(|c| c.is_ascii_alphanumeric() || c == '.' || c == '-')
        })
}

/// The feed must be the release's own latest.json on GitHub (the app checks every
/// update's signature with its own key anyway; this keeps a request from pointing it at
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

/// Writes the request back with a new state (and the error), atomically.
pub fn write_state(path: &Path, req: &Request, state: &str, error: &str) -> std::io::Result<()> {
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

    #[test]
    fn installs_what_the_server_asks_only_when_it_runs_the_server() {
        let r = req("requested", "1.5.0", FEED);
        assert_eq!(
            decide(Some(&r), true, false, false),
            Action::Install {
                version: "1.5.0".into(),
                feed: FEED.into()
            }
        );
        assert_eq!(decide(Some(&r), false, false, false), Action::Nothing); // client mode
        assert_eq!(decide(Some(&r), true, false, true), Action::Nothing); // one at a time
        assert_eq!(decide(None, true, false, false), Action::Nothing);
        for state in ["installing", "failed"] {
            assert_eq!(decide(Some(&req(state, "1.5.0", FEED)), true, false, false), Action::Nothing);
        }
    }

    #[test]
    fn refuses_a_paused_server_and_strange_requests() {
        assert!(matches!(
            decide(Some(&req("requested", "1.5.0", FEED)), true, true, false),
            Action::Refuse(_)
        ));
        assert!(matches!(
            decide(Some(&req("requested", "1.5", FEED)), true, false, false),
            Action::Refuse(_)
        ));
        // a feed of another release, or another host
        for feed in [
            "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.4.0/latest.json",
            "https://evil.example/releases/download/v1.5.0/latest.json",
            "",
        ] {
            assert!(matches!(
                decide(Some(&req("requested", "1.5.0", feed)), true, false, false),
                Action::Refuse(_)
            ));
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
    fn writes_the_outcome_back() {
        let dir = tempfile::tempdir().unwrap();
        let path = request_path(dir.path());
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        let r = req("requested", "1.5.0", FEED);
        write_state(&path, &r, "failed", "signature mismatch").unwrap();
        let got = read(&path).unwrap();
        assert_eq!(got.state, "failed");
        assert_eq!(got.error, "signature mismatch");
        assert_eq!(got.run_id, "r1");
    }
}
