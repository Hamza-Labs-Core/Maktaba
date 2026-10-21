// Which Maktaba the window shows (Epic 36, stories 36.5 / 36.6).
//
// - server mode: this Mac runs the server (the login item); the window
//   loads the local web app (http://127.0.0.1:<web port>).
// - client mode: the window connects to another Maktaba, as before
//   Epic 36; with no saved address it shows the bundled web app.
//
// The choice is saved in mode.json by the setup screens: in the app's config
// folder on a Mac, its local data folder on Windows (%LOCALAPPDATA%: a
// roaming profile must not carry this PC's server mode to another PC, which
// would then wait on a server it does not run). Before it exists, a build
// that carries the server payload opens setup.
use std::path::Path;

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "mode", rename_all = "snake_case")]
pub enum AppMode {
    Server {
        /// the local web app, from the `finish` / `status` setup step
        web_url: String,
        /// the supervisor's state endpoint base
        #[serde(default = "default_supervisor_url")]
        supervisor_url: String,
    },
    Client {
        server_url: String,
    },
}

fn default_supervisor_url() -> String {
    format!("http://{}", crate::status::DEFAULT_SUPERVISOR_ADDR)
}

/// What the main window shows at start.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(tag = "target", content = "url", rename_all = "snake_case")]
pub enum Target {
    /// The bundled web app's setup screens.
    Setup,
    /// The bundled web app waits for the local server, then opens url.
    Starting(String),
    /// Load this Maktaba (another machine) in the window.
    Remote(String),
    /// The bundled web app as is (client mode without an address).
    Bundled,
}

pub fn start_target(mode: Option<&AppMode>, payload_present: bool) -> Target {
    match mode {
        Some(AppMode::Server { web_url, .. }) => Target::Starting(web_url.clone()),
        Some(AppMode::Client { server_url }) => Target::Remote(server_url.clone()),
        None if payload_present => Target::Setup,
        None => Target::Bundled,
    }
}

pub fn load(path: &Path) -> Option<AppMode> {
    let raw = std::fs::read(path).ok()?;
    serde_json::from_slice(&raw).ok()
}

pub fn save(path: &Path, mode: &AppMode) -> std::io::Result<()> {
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir)?;
    }
    let tmp = path.with_extension("json.tmp");
    std::fs::write(
        &tmp,
        serde_json::to_vec_pretty(mode).map_err(std::io::Error::other)?,
    )?;
    std::fs::rename(tmp, path)
}

/// Move a mode.json from `from` (the roaming folder Windows kept it in
/// before) to `to`, once: only while `to` has none. A server mode moves only
/// when `server_here` (setup registered the server on this PC): on a roaming
/// profile it may be another PC's, which is left where it is. Copied, then
/// removed, since the two folders may be on different drives. True when it
/// moved.
#[cfg_attr(not(windows), allow(dead_code))]
pub fn adopt(from: &Path, to: &Path, server_here: bool) -> std::io::Result<bool> {
    if to.exists() {
        return Ok(false);
    }
    let Some(m) = load(from) else {
        return Ok(false);
    };
    if matches!(m, AppMode::Server { .. }) && !server_here {
        return Ok(false);
    }
    save(to, &m)?;
    // the copy is the one in use now; a roaming one left behind is never read
    // again on this PC
    let _ = std::fs::remove_file(from);
    Ok(true)
}

/// Accept an http(s) address with a host and nothing that could run
/// script in the window (javascript:, file:, data: …).
pub fn valid_server_url(url: &str) -> Option<String> {
    let url = url.trim().trim_end_matches('/');
    let rest = url
        .strip_prefix("https://")
        .or_else(|| url.strip_prefix("http://"))?;
    let host = rest.split(['/', '?', '#']).next()?;
    if host.is_empty() || host.contains(['@', ' ']) {
        return None;
    }
    Some(url.to_string())
}

/// A web URL for the local server: loopback http only, so the window
/// never gets pointed somewhere else through the server mode path.
pub fn valid_local_url(url: &str) -> Option<String> {
    let u = valid_server_url(url)?;
    let rest = u.strip_prefix("http://")?;
    let host = rest.split(['/', '?', '#']).next()?;
    let name = host.rsplit_once(':').map_or(host, |(h, _)| h);
    matches!(name, "127.0.0.1" | "localhost" | "[::1]").then_some(u)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn start_target_follows_the_saved_mode() {
        let server = AppMode::Server {
            web_url: "http://127.0.0.1:8088".into(),
            supervisor_url: default_supervisor_url(),
        };
        let client = AppMode::Client {
            server_url: "https://maktaba.home".into(),
        };
        assert_eq!(
            start_target(Some(&server), true),
            Target::Starting("http://127.0.0.1:8088".into())
        );
        assert_eq!(
            start_target(Some(&client), true),
            Target::Remote("https://maktaba.home".into())
        );
        assert_eq!(start_target(None, true), Target::Setup);
        assert_eq!(start_target(None, false), Target::Bundled);
    }

    #[test]
    fn mode_round_trips_and_defaults_the_supervisor() {
        let tmp = tempfile::tempdir().unwrap();
        let p = tmp.path().join("cfg/mode.json");
        assert_eq!(load(&p), None);
        let m = AppMode::Client {
            server_url: "http://nas.local:8088".into(),
        };
        save(&p, &m).unwrap();
        assert_eq!(load(&p), Some(m));
        std::fs::write(&p, r#"{"mode":"server","web_url":"http://127.0.0.1:8088"}"#).unwrap();
        assert_eq!(
            load(&p),
            Some(AppMode::Server {
                web_url: "http://127.0.0.1:8088".into(),
                supervisor_url: "http://127.0.0.1:9103".into()
            })
        );
        std::fs::write(&p, "garbage").unwrap();
        assert_eq!(load(&p), None);
    }

    #[test]
    fn a_roaming_mode_moves_to_this_pc_once() {
        let tmp = tempfile::tempdir().unwrap();
        let roaming = tmp.path().join("Roaming/com.hamzalabs.maktaba/mode.json");
        let local = tmp.path().join("Local/com.hamzalabs.maktaba/mode.json");
        let server = AppMode::Server {
            web_url: "http://127.0.0.1:8088".into(),
            supervisor_url: default_supervisor_url(),
        };
        let client = AppMode::Client {
            server_url: "https://maktaba.home".into(),
        };
        // nothing to move
        assert!(!adopt(&roaming, &local, true).unwrap());
        // another PC's server mode stays where it is: this PC opens setup
        save(&roaming, &server).unwrap();
        assert!(!adopt(&roaming, &local, false).unwrap());
        assert!(roaming.exists() && !local.exists());
        // this PC's server mode moves (copied, then removed)
        assert!(adopt(&roaming, &local, true).unwrap());
        assert_eq!(load(&local), Some(server.clone()));
        assert!(!roaming.exists());
        // once: a local one is never replaced
        save(&roaming, &client).unwrap();
        assert!(!adopt(&roaming, &local, true).unwrap());
        assert_eq!(load(&local), Some(server));
        // client mode moves on any PC
        std::fs::remove_file(&local).unwrap();
        assert!(adopt(&roaming, &local, false).unwrap());
        assert_eq!(load(&local), Some(client));
        // an unreadable one is left alone
        std::fs::remove_file(&local).unwrap();
        std::fs::write(&roaming, "garbage").unwrap();
        assert!(!adopt(&roaming, &local, true).unwrap());
        assert!(roaming.exists() && !local.exists());
    }

    #[test]
    fn server_urls_are_http_with_a_host() {
        assert_eq!(
            valid_server_url(" https://maktaba.home/ "),
            Some("https://maktaba.home".into())
        );
        assert_eq!(
            valid_server_url("http://10.0.0.5:8088"),
            Some("http://10.0.0.5:8088".into())
        );
        for bad in [
            "javascript:alert(1)",
            "file:///etc/passwd",
            "https://",
            "ftp://x",
            "http://user@evil",
            "maktaba.home",
        ] {
            assert_eq!(valid_server_url(bad), None, "{bad}");
        }
    }

    #[test]
    fn local_urls_are_loopback_only() {
        assert_eq!(
            valid_local_url("http://127.0.0.1:8088"),
            Some("http://127.0.0.1:8088".into())
        );
        assert_eq!(
            valid_local_url("http://localhost:8088/"),
            Some("http://localhost:8088".into())
        );
        assert_eq!(valid_local_url("http://192.168.1.2:8088"), None);
        assert_eq!(valid_local_url("https://127.0.0.1:8088"), None);
        assert_eq!(valid_local_url("http://127.0.0.1.evil.com"), None);
    }
}
