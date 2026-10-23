// The home's order to install a release (Epic 35, install-kinds.md "Ask first", owner
// decision D1): a desktop app that runs the server (the home, or a full node) installs
// the release the home's update ordered only when someone clicks here. The server
// writes <state>/updates/app-install.json ({run_id, version, feed, kind, home, state:
// ordered}); this app's status poller reads it, stages the release (kind app: its own
// update from the release's own feed, releases/download/v<version>/latest.json in the
// app's own repository, so the channel, pin and skip the server applied hold;
// downloaded and checked with this app's key, kept in updates/app-update-<version>.bin;
// kind runtime or server: nothing to fetch, the server installs its own runtime or
// switches to a release this app holds already) and shows it in the tray (staged) and
// once in an OS notification. A click says clicked; the server answers go once the
// home's run is ready (the home drains its workers first; a machine waits its turn), and
// this app installs what it staged (checked again, and only while the order still says
// go) and says how it went (installing, failed with the error). Pure decisions live
// here; shell.rs runs the updater.
use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

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

/// The order's kinds: this app's updater installs it (Mac, Windows; an order without a
/// kind is read as one), the server installs it in its runtime (the Linux app), or this
/// app holds that release already (a retry after a rollback) and the server switches to
/// it at the go. Only kind app is downloaded and installed here.
pub const KIND_APP: &str = "app";
pub const KIND_RUNTIME: &str = "runtime";
pub const KIND_SERVER: &str = "server";

/// The app's own repository, where its releases and their feeds are, when its updater
/// endpoint (tauri.conf.json) names none.
pub const OWN_REPO: &str = "https://github.com/Hamza-Labs-Core/Maktaba";

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
    /// This app's updater installs it (kind app, or no kind).
    pub fn by_app(&self) -> bool {
        self.kind.is_empty() || self.kind == KIND_APP
    }

    /// The server installs it (its runtime, or a release this app holds): shown only.
    pub fn by_server(&self) -> bool {
        matches!(self.kind.as_str(), KIND_RUNTIME | KIND_SERVER)
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

/// The order of run_id still says go (the home's go, or this app's installing): an
/// install of it may go on. Read again right before the server is stopped for it and
/// right before it is installed (a cancel, a newer run or a skip in between stops it).
pub fn still_go(path: &Path, run_id: &str) -> bool {
    read(path).is_some_and(|r| r.run_id == run_id && matches!(r.state.as_str(), GO | INSTALLING))
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
/// paused; staged: the release is staged here; busy: a stage or an install is under way;
/// repo: the app's own repository (own_repo), whose release feeds alone are trusted.
pub fn decide(
    req: Option<&Request>,
    server_side: bool,
    paused: bool,
    staged: bool,
    busy: bool,
    repo: &str,
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
    if !req.by_app() && !req.by_server() {
        return Action::Refuse(format!("kind {:?} is not one this app knows", req.kind));
    }
    if req.by_app() && !trusted_feed(&req.feed, &req.version, repo) {
        return Action::Refuse(format!("feed {:?} is not the release's own", req.feed));
    }
    let stage = Action::Stage {
        version: req.version.clone(),
        feed: req.feed.clone(),
    };
    match req.state.as_str() {
        ORDERED if req.by_app() && !staged => stage,
        // kind runtime or server: the server has it; kind app: staged before a restart
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
        // kind runtime or server at its go: the server switches
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

/// The order to notify of now: one that became ready (staged) and was not notified of
/// yet; its run is remembered in notified (one notification per order).
pub fn to_notify(req: Option<&Request>, notified: &mut HashSet<String>) -> Option<Shown> {
    let req = req?;
    match shown(Some(req)) {
        Some(ready @ Shown::Ready { .. }) if notified.insert(req.run_id.clone()) => Some(ready),
        _ => None,
    }
}

/// What a click on the tray's update item does.
#[derive(Debug, PartialEq)]
pub enum Click {
    /// The home's order, staged here: say clicked (it installs at the home's go).
    Order(Request),
    /// The newest release of the app's own feed (an app in client mode).
    OwnFeed,
    /// Exactly this release, from its own feed (the Linux app older than its server).
    Release(String),
    /// Nothing: an app that runs the server installs only what its home orders.
    Nothing,
}

/// click decides the update item's click for an app that runs the server (server_side)
/// or a client; catch_up: the release the Linux app older than its server catches up
/// with (catch_up below), the only update an app that runs the server installs of its
/// own.
pub fn click(order: Option<&Request>, server_side: bool, catch_up: Option<&str>) -> Click {
    if !server_side {
        return Click::OwnFeed;
    }
    if let Some(r) = order.filter(|r| r.state == STAGED) {
        return Click::Order(r.clone());
    }
    match catch_up.filter(|v| valid_version(v)) {
        Some(v) => Click::Release(v.strip_prefix('v').unwrap_or(v).to_string()),
        None => Click::Nothing,
    }
}

/// The release the Linux app (app) older than its server catches up with: the version
/// the server's runtime runs (running), or the newest it ran (watermark) when that is
/// not known. None when the app is that release already (after a rollback below the
/// watermark) or no version is known.
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub fn catch_up(app: &str, running: &str, watermark: &str) -> Option<String> {
    let v = if running.is_empty() {
        watermark
    } else {
        running
    };
    (valid_version(v) && !same_version(app, v)).then(|| v.strip_prefix('v').unwrap_or(v).into())
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

/// The app's own repository: the GitHub repository whose releases its updater endpoints
/// (tauri.conf.json plugins.updater.endpoints) point at, the first that names one; else
/// OWN_REPO.
pub fn own_repo<'a>(endpoints: impl IntoIterator<Item = &'a str>) -> String {
    endpoints
        .into_iter()
        .find_map(repo_of)
        .unwrap_or_else(|| OWN_REPO.to_string())
}

/// https://github.com/<owner>/<repo> of a feed on that repository's releases.
fn repo_of(endpoint: &str) -> Option<String> {
    let rest = endpoint.strip_prefix("https://github.com/")?;
    let mut parts = rest.splitn(3, '/');
    let (owner, repo, tail) = (parts.next()?, parts.next()?, parts.next()?);
    let name = |s: &str| {
        !s.is_empty()
            && !s.starts_with('.')
            && s.chars()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_' | '.'))
    };
    (name(owner) && name(repo) && tail.starts_with("releases/"))
        .then(|| format!("https://github.com/{owner}/{repo}"))
}

/// A release's own feed in repo: <repo>/releases/download/v<version>/latest.json.
pub fn release_feed(repo: &str, version: &str) -> String {
    let v = version.strip_prefix('v').unwrap_or(version);
    format!("{repo}/releases/download/v{v}/latest.json")
}

/// The feed must be exactly the release's own latest.json in the app's own repository
/// (the app checks every update's signature with its own key anyway; this keeps an
/// order from pointing it at another release, or another repository's, than the one the
/// server verified).
pub fn trusted_feed(feed: &str, version: &str, repo: &str) -> bool {
    valid_version(version) && feed == release_feed(repo, version)
}

/// The same version, with or without its v.
pub fn same_version(a: &str, b: &str) -> bool {
    a.strip_prefix('v').unwrap_or(a) == b.strip_prefix('v').unwrap_or(b)
}

/// The updater's version check for an install of exactly `wanted` (the order's, or the
/// server's): the offered release is that version, whatever this app's own version is
/// (the same after a rollback, or newer), and never any other.
pub fn exactly(wanted: &str, offered: &str) -> bool {
    valid_version(wanted) && same_version(wanted, offered)
}

/// How often write_state tries to put the answer in place, and how long it waits
/// between tries (a reader on Windows can hold the file for a moment).
const WRITE_TRIES: u32 = 5;
const WRITE_PAUSE: Duration = Duration::from_millis(40);

/// The order still holds run_id (Err: it cannot be read now; Ok(false): gone, replaced).
fn holds(path: &Path, run_id: &str) -> std::io::Result<bool> {
    match std::fs::read(path) {
        Ok(raw) => Ok(serde_json::from_slice::<Request>(&raw).is_ok_and(|r| r.run_id == run_id)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(false),
        Err(e) => Err(e),
    }
}

/// Writes the order back with a new state (and the error), atomically; only while it is
/// still the same run's (a newer order or a withdrawn one is left alone). It is read
/// again right before the answer is put in place, which leaves the server's own writes
/// only that moment to cross it; a rename that fails (Windows: someone holds the file) is
/// tried again a few times, briefly.
pub fn write_state(path: &Path, req: &Request, state: &str, error: &str) -> std::io::Result<()> {
    if !holds(path, &req.run_id).unwrap_or(true) {
        return Ok(());
    }
    let mut r = req.clone();
    r.state = state.to_string();
    r.error = error.to_string();
    let raw = serde_json::to_vec_pretty(&r).map_err(std::io::Error::other)?;
    // a name of this write's own: the poller and a tray click may answer at once
    static SEQ: AtomicU64 = AtomicU64::new(0);
    let tmp = path.with_extension(format!(
        "json.{}-{}.tmp",
        std::process::id(),
        SEQ.fetch_add(1, Ordering::Relaxed)
    ));
    std::fs::write(&tmp, raw)?;
    let mut last = None;
    for attempt in 0..WRITE_TRIES {
        if attempt > 0 {
            std::thread::sleep(WRITE_PAUSE);
        }
        match holds(path, &req.run_id) {
            Ok(true) => {}
            // withdrawn or replaced meanwhile: left as it is
            Ok(false) => {
                let _ = std::fs::remove_file(&tmp);
                return Ok(());
            }
            Err(e) => {
                last = Some(e);
                continue;
            }
        }
        match std::fs::rename(&tmp, path) {
            Ok(()) => return Ok(()),
            Err(e) => last = Some(e),
        }
    }
    let _ = std::fs::remove_file(&tmp);
    Err(last.unwrap_or_else(|| std::io::Error::other("the order could not be written")))
}

/// The code an ordered install's error starts with when this app cannot be replaced
/// without an administrator's password (macOS: Maktaba.app in a folder this user cannot
/// change, such as /Applications for a standard user). The updater's prompt for the
/// password and its replacing of the app are one step, so an ordered install never asks:
/// a password typed after the server stopped waiting would install a release no run
/// watches (Epic 35 review round 2). The machine then needs a person.
#[cfg(any(target_os = "macos", test))]
pub const NEEDS_ADMIN: &str = "needs_admin";

/// The app bundle (Maktaba.app) of its executable (Contents/MacOS/<name>); None outside
/// one (a development build in target/).
#[cfg(any(target_os = "macos", test))]
pub fn app_bundle_of(exe: &Path) -> Option<PathBuf> {
    let contents = exe.parent()?.parent()?;
    if contents.file_name()? != "Contents" {
        return None;
    }
    let bundle = contents.parent()?;
    let is_app = bundle
        .extension()
        .is_some_and(|e| e.eq_ignore_ascii_case("app"));
    is_app.then(|| bundle.to_path_buf())
}

/// Why replacing the app bundle at `bundle` would need an administrator's password (an
/// error starting with NEEDS_ADMIN, for the order): the updater moves the bundle, which
/// takes changing the folder that holds it and the bundle itself (its `..`). None when
/// this user can (`writable` says whether this user can change a folder).
#[cfg(any(target_os = "macos", test))]
pub fn needs_admin(bundle: &Path, writable: impl Fn(&Path) -> bool) -> Option<String> {
    let parent = bundle.parent()?;
    let locked = [parent, bundle].into_iter().find(|d| !writable(d))?;
    Some(format!(
        "{NEEDS_ADMIN}: {} cannot be replaced without an administrator's password ({} is not \
         this user's to change): move Maktaba.app to a folder this user can change (such as \
         Applications in the home folder), or have an administrator install this update",
        bundle.display(),
        locked.display()
    ))
}

/// Whether this user can change the folder `dir` (create and remove its entries); a
/// read-only volume cannot be.
#[cfg(all(unix, any(target_os = "macos", test)))]
pub fn writable(dir: &Path) -> bool {
    use std::os::unix::ffi::OsStrExt;
    let Ok(c) = std::ffi::CString::new(dir.as_os_str().as_bytes()) else {
        return false;
    };
    // SAFETY: c is a NUL-terminated path that outlives the call; access only reads it
    unsafe { libc::access(c.as_ptr(), libc::W_OK) == 0 }
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
    const REPO: &str = OWN_REPO;

    fn stage() -> Action {
        Action::Stage {
            version: "1.5.0".into(),
            feed: FEED.into(),
        }
    }

    fn decide_on(r: &Request, paused: bool, staged: bool) -> Action {
        decide(Some(r), true, paused, staged, false, REPO)
    }

    #[test]
    fn stages_what_the_home_orders_and_installs_only_at_its_go() {
        let ordered = req(ORDERED, "1.5.0", FEED);
        assert_eq!(decide_on(&ordered, false, false), stage());
        // staged already (before a restart of this app): shown
        assert_eq!(decide_on(&ordered, false, true), Action::Show);
        // shown: nothing until the click and the home's go
        let staged = req(STAGED, "1.5.0", FEED);
        assert_eq!(decide_on(&staged, false, true), Action::Nothing);
        assert_eq!(decide_on(&staged, false, false), stage());
        assert_eq!(
            decide_on(&req(CLICKED, "1.5.0", FEED), false, true),
            Action::Nothing
        );
        assert_eq!(
            decide_on(&req(GO, "1.5.0", FEED), false, true),
            Action::Install {
                version: "1.5.0".into(),
                feed: FEED.into()
            }
        );
        assert_eq!(
            decide(Some(&ordered), false, false, false, false, REPO),
            Action::Nothing
        ); // client mode
        assert_eq!(
            decide(Some(&ordered), true, false, false, true, REPO),
            Action::Nothing
        ); // one at a time
        assert_eq!(
            decide(None, true, false, false, false, REPO),
            Action::Nothing
        );
        for state in [INSTALLING, FAILED] {
            assert_eq!(
                decide_on(&req(state, "1.5.0", FEED), false, true),
                Action::Nothing
            );
        }
        // kind app said, or no kind at all: the same
        let mut app = req(ORDERED, "1.5.0", FEED);
        app.kind = KIND_APP.into();
        assert_eq!(decide_on(&app, false, false), stage());
    }

    #[test]
    fn the_linux_runtime_is_shown_and_left_to_the_server() {
        let mut r = req(ORDERED, "1.5.0", "");
        r.kind = KIND_RUNTIME.into();
        assert_eq!(decide_on(&r, false, false), Action::Show);
        r.state = GO.into();
        assert_eq!(decide_on(&r, false, false), Action::Nothing);
    }

    #[test]
    fn a_release_this_app_holds_is_shown_and_the_server_switches() {
        // kind server: a retry after a rollback, this app has that release already
        let mut r = req(ORDERED, "1.5.0", "");
        r.kind = KIND_SERVER.into();
        assert!(!r.by_app() && r.by_server());
        assert_eq!(decide_on(&r, false, false), Action::Show);
        // never staged, never installed here, at any state, paused or not
        for state in [STAGED, CLICKED, GO, INSTALLING, FAILED] {
            r.state = state.into();
            for paused in [false, true] {
                for staged in [false, true] {
                    assert_eq!(decide_on(&r, paused, staged), Action::Nothing, "{state}");
                }
            }
        }
        // a feed it carries is not fetched either
        let mut with_feed = req(ORDERED, "1.5.0", FEED);
        with_feed.kind = KIND_SERVER.into();
        assert_eq!(decide_on(&with_feed, false, false), Action::Show);
        // shown as the runtime's is
        r.state = STAGED.into();
        r.home = true;
        assert_eq!(
            shown(Some(&r)),
            Some(Shown::Ready {
                version: "1.5.0".into(),
                home: true
            })
        );
        r.state = GO.into();
        assert_eq!(
            shown(Some(&r)),
            Some(Shown::Clicked {
                version: "1.5.0".into()
            })
        );
    }

    #[test]
    fn refuses_a_paused_server_and_strange_orders() {
        assert!(matches!(
            decide_on(&req(GO, "1.5.0", FEED), true, true),
            Action::Refuse(_)
        ));
        assert!(matches!(
            decide_on(&req(ORDERED, "1.5", FEED), false, false),
            Action::Refuse(_)
        ));
        // a kind this app does not know
        let mut odd = req(ORDERED, "1.5.0", FEED);
        odd.kind = "firmware".into();
        assert!(matches!(decide_on(&odd, false, false), Action::Refuse(_)));
        // a feed of another release, another repository, or another host
        for feed in [
            "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.4.0/latest.json",
            "https://github.com/someone/Maktaba/releases/download/v1.5.0/latest.json",
            "https://github.com/Hamza-Labs-Core/Other/releases/download/v1.5.0/latest.json",
            "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.5.0/latest.json?x=1",
            "http://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.5.0/latest.json",
            "https://evil.example/releases/download/v1.5.0/latest.json",
            "",
        ] {
            assert!(
                matches!(
                    decide_on(&req(ORDERED, "1.5.0", feed), false, false),
                    Action::Refuse(_)
                ),
                "{feed}"
            );
        }
    }

    #[test]
    fn only_the_apps_own_repository_is_trusted() {
        assert!(trusted_feed(FEED, "1.5.0", REPO));
        assert!(trusted_feed(FEED, "v1.5.0", REPO));
        assert!(!trusted_feed(FEED, "1.5.1", REPO));
        assert!(!trusted_feed(FEED, "1.5", REPO));
        assert_eq!(release_feed(REPO, "v1.5.0"), FEED);
        // a fork's own app trusts its own repository's releases
        let fork = "https://github.com/me/Maktaba";
        assert!(!trusted_feed(FEED, "1.5.0", fork));
        assert!(trusted_feed(&release_feed(fork, "1.5.0"), "1.5.0", fork));
    }

    #[test]
    fn the_repository_is_the_updater_endpoints() {
        let latest =
            "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/latest.json";
        assert_eq!(own_repo([latest]), OWN_REPO);
        assert_eq!(
            own_repo(["https://github.com/me/my-fork.x/releases/latest/download/latest.json"]),
            "https://github.com/me/my-fork.x"
        );
        // none that names a GitHub repository's releases: the constant
        for odd in [
            "https://updates.example/latest.json",
            "https://github.com/me/repo/archive/main.zip",
            "https://github.com/../x/releases/latest/download/latest.json",
            "https://github.com/me",
            "http://github.com/me/repo/releases/latest/download/latest.json",
        ] {
            assert_eq!(own_repo([odd]), OWN_REPO, "{odd}");
        }
        assert_eq!(own_repo(Vec::<&str>::new()), OWN_REPO);
        // the first that names one
        assert_eq!(
            own_repo([
                "https://updates.example/latest.json",
                "https://github.com/me/repo/releases/latest/download/latest.json"
            ]),
            "https://github.com/me/repo"
        );
    }

    #[test]
    fn the_shipped_config_names_this_repository_and_signed_versions() {
        let conf: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        let updater = &conf["plugins"]["updater"];
        let endpoints: Vec<&str> = updater["endpoints"]
            .as_array()
            .unwrap()
            .iter()
            .filter_map(|e| e.as_str())
            .collect();
        assert_eq!(own_repo(endpoints), OWN_REPO);
        // a feed cannot pair a version with another release's signed package
        assert_eq!(updater["requireSignedVersion"], serde_json::json!(true));
    }

    #[test]
    fn the_install_takes_exactly_the_wanted_version() {
        assert!(exactly("1.5.0", "1.5.0"));
        assert!(exactly("v1.5.0", "1.5.0"));
        assert!(exactly("1.5.0-rc.1", "1.5.0-rc.1"));
        for offered in ["1.5.1", "1.4.9", "1.5.0-rc.1", "2.0.0", ""] {
            assert!(!exactly("1.5.0", offered), "{offered}");
        }
        assert!(!exactly("", ""));
        assert!(!exactly("1.5", "1.5"));
    }

    #[test]
    fn the_install_goes_on_only_while_the_order_says_go() {
        let dir = tempfile::tempdir().unwrap();
        let path = request_path(dir.path());
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        assert!(!still_go(&path, "r1")); // none
        for (state, go) in [
            (GO, true),
            (INSTALLING, true),
            (ORDERED, false),
            (STAGED, false),
            (CLICKED, false),
            (FAILED, false),
        ] {
            let r = req(state, "1.5.0", FEED);
            std::fs::write(&path, serde_json::to_vec(&r).unwrap()).unwrap();
            assert_eq!(still_go(&path, "r1"), go, "{state}");
        }
        // another run's go is not this one's
        assert!(!still_go(&path, "r0"));
        std::fs::write(&path, b"{").unwrap();
        assert!(!still_go(&path, "r1"));
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
    fn an_order_is_notified_once_when_it_is_ready() {
        let mut seen = HashSet::new();
        let mut r = req(ORDERED, "v1.5.0", FEED);
        assert_eq!(to_notify(Some(&r), &mut seen), None);
        r.state = STAGED.into();
        r.home = true;
        assert_eq!(
            to_notify(Some(&r), &mut seen),
            Some(Shown::Ready {
                version: "1.5.0".into(),
                home: true
            })
        );
        // the next polls, and the click that follows, say nothing more
        assert_eq!(to_notify(Some(&r), &mut seen), None);
        r.state = CLICKED.into();
        assert_eq!(to_notify(Some(&r), &mut seen), None);
        // a newer run is another order
        let mut newer = req(STAGED, "1.6.0", FEED);
        newer.run_id = "r2".into();
        assert!(to_notify(Some(&newer), &mut seen).is_some());
        assert_eq!(to_notify(None, &mut seen), None);
    }

    #[test]
    fn a_click_installs_only_what_the_home_ordered_on_an_app_that_runs_the_server() {
        let staged = req(STAGED, "1.5.0", FEED);
        assert_eq!(
            click(Some(&staged), true, None),
            Click::Order(staged.clone())
        );
        // not staged (any more) at the click: nothing, never the app's own feed
        for state in [ORDERED, CLICKED, GO, INSTALLING, FAILED] {
            let r = req(state, "1.5.0", FEED);
            assert_eq!(click(Some(&r), true, None), Click::Nothing, "{state}");
        }
        assert_eq!(click(None, true, None), Click::Nothing);
        // the Linux app older than its server catches up with exactly its release
        assert_eq!(
            click(None, true, Some("v1.5.0")),
            Click::Release("1.5.0".into())
        );
        assert_eq!(click(None, true, Some("1.5")), Click::Nothing);
        // the order comes first
        assert_eq!(
            click(Some(&staged), true, Some("1.4.0")),
            Click::Order(staged.clone())
        );
        // a client keeps its own updates
        assert_eq!(click(None, false, None), Click::OwnFeed);
        assert_eq!(click(Some(&staged), false, None), Click::OwnFeed);
    }

    #[test]
    fn the_older_linux_app_catches_up_with_what_the_server_runs() {
        assert_eq!(catch_up("v1.4.0", "v1.5.0", "v1.5.0"), Some("1.5.0".into()));
        // after a rollback: what runs, not the newest it ran
        assert_eq!(catch_up("v1.4.0", "v1.5.0", "v1.6.0"), Some("1.5.0".into()));
        // the app is what runs already (below the watermark): nothing to install
        assert_eq!(catch_up("v1.5.0", "v1.5.0", "v1.6.0"), None);
        // what runs not known: the newest it ran
        assert_eq!(catch_up("v1.4.0", "", "v1.5.0"), Some("1.5.0".into()));
        assert_eq!(catch_up("v1.4.0", "", ""), None);
        assert_eq!(catch_up("v1.4.0", "nightly", "v1.5.0"), None);
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

    fn leftovers(dir: &Path) -> Vec<String> {
        std::fs::read_dir(dir.join("updates"))
            .unwrap()
            .flatten()
            .map(|e| e.file_name().to_string_lossy().to_string())
            .filter(|n| n.ends_with(".tmp"))
            .collect()
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
        // nothing is left behind
        assert!(
            leftovers(dir.path()).is_empty(),
            "{:?}",
            leftovers(dir.path())
        );
    }

    #[test]
    fn answers_written_at_once_each_land_whole() {
        let dir = tempfile::tempdir().unwrap();
        let path = request_path(dir.path());
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        let r = req(STAGED, "1.5.0", FEED);
        std::fs::write(&path, serde_json::to_vec(&r).unwrap()).unwrap();
        let writers: Vec<_> = (0..8)
            .map(|i| {
                let (path, r) = (path.clone(), r.clone());
                std::thread::spawn(move || {
                    let state = if i % 2 == 0 { CLICKED } else { STAGED };
                    write_state(&path, &r, state, "")
                })
            })
            .collect();
        for w in writers {
            // a rename over a file another writer holds may lose on Windows after its
            // tries; it never leaves a broken order
            let _ = w.join().unwrap();
        }
        let got = read(&path).expect("a whole order");
        assert_eq!(got.run_id, "r1");
        assert!(matches!(got.state.as_str(), CLICKED | STAGED));
        assert!(
            leftovers(dir.path()).is_empty(),
            "{:?}",
            leftovers(dir.path())
        );
    }

    #[test]
    fn app_bundle_of_its_executable() {
        assert_eq!(
            app_bundle_of(Path::new(
                "/Applications/Maktaba.app/Contents/MacOS/Maktaba"
            )),
            Some(PathBuf::from("/Applications/Maktaba.app"))
        );
        assert_eq!(
            app_bundle_of(Path::new("/src/target/debug/maktaba-desktop")),
            None
        );
        assert_eq!(
            app_bundle_of(Path::new("/x/NotAnApp/Contents/MacOS/Maktaba")),
            None
        );
    }

    // An ordered install never meets the updater's password prompt (review round 2):
    // a bundle this user cannot move says why, with the code the server knows.
    #[test]
    fn an_app_that_needs_an_admin_password_says_so() {
        let bundle = Path::new("/Applications/Maktaba.app");
        assert_eq!(needs_admin(bundle, |_| true), None);
        let apps = bundle.parent().unwrap();
        let why = needs_admin(bundle, |d| d != apps).unwrap();
        assert!(why.starts_with("needs_admin: "), "{why}");
        assert!(
            why.contains(&format!("({} is not this user's", apps.display())),
            "{why}"
        );
        let why = needs_admin(bundle, |d| d != bundle).unwrap();
        assert!(
            why.contains(&format!("({} is not this user's", bundle.display())),
            "{why}"
        );
    }

    #[cfg(unix)]
    #[test]
    fn writable_follows_the_folder_permissions() {
        use std::os::unix::fs::PermissionsExt;
        let dir = tempfile::tempdir().unwrap();
        assert!(writable(dir.path()));
        // root may write anywhere: only an ordinary user sees the folder locked
        if unsafe { libc::geteuid() } != 0 {
            let locked = dir.path().join("locked");
            std::fs::create_dir(&locked).unwrap();
            std::fs::set_permissions(&locked, std::fs::Permissions::from_mode(0o555)).unwrap();
            assert!(!writable(&locked));
            std::fs::set_permissions(&locked, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        assert!(!writable(&dir.path().join("missing")));
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
