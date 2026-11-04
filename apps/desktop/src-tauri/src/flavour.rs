//! The payload's flavour and the update-feed key it asks for (Epic 51 story 51.17).
//!
//! The Mac has two apps: Full (today's) and Core, whose payload leaves out ollama. The
//! payload's payload.json says which (`"flavour": "full" | "core"`). The release's feed has
//! a key of its own for the Core app (`darwin-aarch64-core`, tools/release/updaterfeed), so
//! a Core app updates to Core apps. A release without that key (one from before the Core
//! dmg, or one whose Core leg failed) is no reason to stop updating: the Core app then takes
//! the Full key, a superset. A Full app never asks for the Core key.

use std::future::Future;
use std::path::Path;

/// The flavour payload.json in server_dir names, trimmed; None when there is no file, it
/// is not JSON or it names none (every payload before story 51.17: Full).
pub fn of_payload(server_dir: &Path) -> Option<String> {
    let raw = std::fs::read(server_dir.join("payload.json")).ok()?;
    let j: serde_json::Value = serde_json::from_slice(&raw).ok()?;
    let f = j["flavour"].as_str()?.trim();
    (!f.is_empty()).then(|| f.to_string())
}

/// The feed key a payload of this flavour asks for first: `<os>-<arch>-core` for a core
/// payload, None (the updater's own keys, the Full app's) for any other.
pub fn core_key(flavour: Option<&str>, os: &str, arch: &str) -> Option<String> {
    (flavour == Some("core")).then(|| format!("{os}-{arch}-core"))
}

/// The updater's name of this machine's OS and architecture (tauri-plugin-updater's
/// `{os}-{arch}`): darwin-aarch64 on an Apple silicon Mac.
pub fn here() -> (&'static str, &'static str) {
    let os = match std::env::consts::OS {
        "macos" => "darwin",
        o => o,
    };
    (os, std::env::consts::ARCH)
}

/// check asks with `core` as the target when there is one, and asks again with no target
/// (the Full keys) only when that key is missing from the feed (`missing`). Without `core`
/// it asks once, with no target. Any other error is the answer.
pub async fn check<T, E, F, Fut>(
    core: Option<&str>,
    ask: F,
    missing: impl Fn(&E) -> bool,
) -> Result<T, E>
where
    F: Fn(Option<String>) -> Fut,
    Fut: Future<Output = Result<T, E>>,
{
    if let Some(k) = core {
        match ask(Some(k.to_string())).await {
            Err(e) if missing(&e) => {}
            r => return r,
        }
    }
    ask(None).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    #[test]
    fn payload_flavour_is_read_from_payload_json() {
        let d = tempfile::tempdir().unwrap();
        assert_eq!(of_payload(d.path()), None, "no payload.json");
        std::fs::write(d.path().join("payload.json"), br#"{"version":"v1"}"#).unwrap();
        assert_eq!(of_payload(d.path()), None, "a payload from before flavours");
        std::fs::write(d.path().join("payload.json"), b"not json").unwrap();
        assert_eq!(of_payload(d.path()), None);
        std::fs::write(d.path().join("payload.json"), br#"{"flavour": " core "}"#).unwrap();
        assert_eq!(of_payload(d.path()).as_deref(), Some("core"));
        std::fs::write(d.path().join("payload.json"), br#"{"flavour":"full"}"#).unwrap();
        assert_eq!(of_payload(d.path()).as_deref(), Some("full"));
    }

    #[test]
    fn only_a_core_payload_asks_for_the_core_key() {
        assert_eq!(
            core_key(Some("core"), "darwin", "aarch64").as_deref(),
            Some("darwin-aarch64-core")
        );
        assert_eq!(core_key(Some("full"), "darwin", "aarch64"), None);
        assert_eq!(core_key(None, "darwin", "aarch64"), None);
        assert_eq!(core_key(Some("Core"), "darwin", "aarch64"), None);
    }

    #[test]
    fn here_names_the_os_as_the_updater_does() {
        let (os, arch) = here();
        assert!(!os.is_empty() && !arch.is_empty());
        assert_ne!(os, "macos");
    }

    #[derive(Debug, PartialEq)]
    enum Err {
        Missing,
        Network,
    }

    /// runs check with a feed that answers each target from `answer`, and returns what it
    /// answered and every target asked, in order
    fn run(
        core: Option<&str>,
        answer: impl Fn(Option<&str>) -> Result<&'static str, Err>,
    ) -> (Result<&'static str, Err>, Vec<Option<String>>) {
        let asked = Mutex::new(Vec::new());
        let r = tauri::async_runtime::block_on(check(
            core,
            |t: Option<String>| {
                asked.lock().unwrap().push(t.clone());
                let a = answer(t.as_deref());
                async move { a }
            },
            |e: &Err| *e == Err::Missing,
        ));
        (r, asked.into_inner().unwrap())
    }

    #[test]
    fn a_core_app_takes_the_core_key_when_the_feed_has_it() {
        let (r, asked) = run(Some("darwin-aarch64-core"), |t| match t {
            Some("darwin-aarch64-core") => Ok("core"),
            _ => Ok("full"),
        });
        assert_eq!(r, Ok("core"));
        assert_eq!(asked, vec![Some("darwin-aarch64-core".to_string())]);
    }

    #[test]
    fn a_core_app_falls_back_to_the_full_key() {
        let (r, asked) = run(Some("darwin-aarch64-core"), |t| match t {
            Some(_) => Err(Err::Missing),
            None => Ok("full"),
        });
        assert_eq!(r, Ok("full"));
        assert_eq!(asked, vec![Some("darwin-aarch64-core".to_string()), None]);
    }

    #[test]
    fn a_core_app_does_not_fall_back_on_another_error() {
        let (r, asked) = run(Some("darwin-aarch64-core"), |t| match t {
            Some(_) => Err(Err::Network),
            None => Ok("full"),
        });
        assert_eq!(r, Err(Err::Network));
        assert_eq!(asked.len(), 1);
    }

    #[test]
    fn a_full_app_never_asks_for_the_core_key() {
        let (r, asked) = run(None, |t| match t {
            Some(_) => Ok("core"),
            None => Ok("full"),
        });
        assert_eq!(r, Ok("full"));
        assert_eq!(asked, vec![None]);
        // and a Full payload has no core key to ask with
        let (r, asked) = run(
            core_key(Some("full"), "darwin", "aarch64").as_deref(),
            |t| match t {
                Some(_) => Ok("core"),
                None => Ok("full"),
            },
        );
        assert_eq!(r, Ok("full"));
        assert_eq!(asked, vec![None]);
    }
}
