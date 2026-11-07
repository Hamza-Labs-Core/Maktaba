// The recovery console and the launcher's can't-start cut-off, from the app (Epic 53,
// stories 53.04 and 53.05).
//
// - "Open recovery page": the supervisor's loopback admin port gives a one-time link
//   (POST /recovery/link, 2 minutes, single use) that signs this Mac's browser in to
//   /recovery without the API or the database.
// - The launcher stops starting a server that keeps failing before it is up and writes
//   updates/start-failed.json; the tray then says so and offers the page's choices (Try
//   again, Start in safe mode, Go back to the version kept), which it leaves for the
//   launcher in updates/start-choice.
use serde::Deserialize;
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream, ToSocketAddrs};
use std::path::{Path, PathBuf};
use std::time::Duration;

/// The launcher's record (cmd/maktaba-launcher/cantstart.go StartFailed).
#[derive(Debug, Clone, Default, Deserialize, PartialEq)]
pub struct CantStart {
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub exits: Vec<serde_json::Value>,
    #[serde(default)]
    pub last_lines: Vec<String>,
    #[serde(default)]
    pub rollback: Option<String>,
}

fn updates(state_root: &Path) -> PathBuf {
    state_root.join("updates")
}

/// The record, when the launcher has cut the server off.
pub fn cant_start(state_root: &Path) -> Option<CantStart> {
    read_record(
        &updates(state_root).join("start-failed.json"),
        5,
        Duration::from_millis(20),
    )
}

/// Reads a record the launcher replaces whole (a rename). On Windows an open can meet the
/// file while its rename lands (a sharing violation, a scan): a file that is there but cannot
/// be read or parsed yet is tried again a few times before it reads as no record.
fn read_record<T: serde::de::DeserializeOwned>(
    path: &Path,
    tries: u32,
    wait: Duration,
) -> Option<T> {
    for i in 0..tries {
        match std::fs::read(path) {
            Ok(raw) => {
                if let Ok(v) = serde_json::from_slice(&raw) {
                    return Some(v);
                }
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return None,
            Err(_) => {}
        }
        if i + 1 < tries {
            std::thread::sleep(wait);
        }
    }
    None
}

/// Leaves a choice for the launcher: "retry", "safe" or "rollback".
pub fn choose(state_root: &Path, choice: &str) -> Result<(), String> {
    if !matches!(choice, "retry" | "safe" | "rollback") {
        return Err(format!("no such choice: {choice}"));
    }
    let dir = updates(state_root);
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let tmp = dir.join("start-choice.tmp");
    std::fs::write(&tmp, format!("{choice}\n")).map_err(|e| e.to_string())?;
    std::fs::rename(&tmp, dir.join("start-choice")).map_err(|e| e.to_string())
}

/// A one-time link to the recovery page from the supervisor at `addr` (host:port,
/// loopback only); None when it does not answer.
pub fn link(addr: &str) -> Option<String> {
    let sock: SocketAddr = addr
        .to_socket_addrs()
        .ok()?
        .find(|a| a.ip().is_loopback())?;
    let timeout = Duration::from_secs(2);
    let mut s = TcpStream::connect_timeout(&sock, timeout).ok()?;
    s.set_read_timeout(Some(timeout)).ok()?;
    s.set_write_timeout(Some(timeout)).ok()?;
    let req = format!(
        "POST /recovery/link HTTP/1.0\r\nHost: {addr}\r\nContent-Length: 0\r\nAccept: application/json\r\n\r\n"
    );
    s.write_all(req.as_bytes()).ok()?;
    let mut raw = Vec::new();
    s.take(64 << 10).read_to_end(&mut raw).ok()?;
    link_of_response(&raw)
}

/// The url in a /recovery/link answer (a 200 only).
fn link_of_response(raw: &[u8]) -> Option<String> {
    let text = std::str::from_utf8(raw).ok()?;
    let (head, body) = text.split_once("\r\n\r\n")?;
    if !head.lines().next()?.contains(" 200") {
        return None;
    }
    #[derive(Deserialize)]
    struct Link {
        url: String,
    }
    let l: Link = serde_json::from_str(body.trim()).ok()?;
    l.url.contains("/recovery?code=").then_some(l.url)
}

/// The recovery page without a one-time link (the supervisor did not answer): the page
/// then says how to sign in.
pub fn page_url(web_url: &str) -> String {
    format!("{}/recovery", web_url.trim_end_matches('/'))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_record_reads_and_a_choice_is_left() {
        let dir = tempfile::tempdir().unwrap();
        assert_eq!(cant_start(dir.path()), None);
        std::fs::create_dir_all(dir.path().join("updates")).unwrap();
        std::fs::write(
            dir.path().join("updates").join("start-failed.json"),
            r#"{"at":"2026-11-06T10:00:00Z","version":"0.3.50","exits":[{"code":1},{"code":1},{"code":2}],"last_lines":["boom"],"rollback":"0.3.49"}"#,
        )
        .unwrap();
        let c = cant_start(dir.path()).unwrap();
        assert_eq!(c.version, "0.3.50");
        assert_eq!(c.exits.len(), 3);
        assert_eq!(c.rollback.as_deref(), Some("0.3.49"));
        choose(dir.path(), "safe").unwrap();
        assert_eq!(
            std::fs::read_to_string(dir.path().join("updates").join("start-choice")).unwrap(),
            "safe\n"
        );
        assert!(choose(dir.path(), "delete-everything").is_err());
    }

    #[test]
    fn a_link_answer_is_read() {
        let ok = b"HTTP/1.0 200 OK\r\nContent-Type: application/json\r\n\r\n{\"url\":\"http://127.0.0.1:8088/recovery?code=ab12\",\"expires_in\":120}\n";
        assert_eq!(
            link_of_response(ok).as_deref(),
            Some("http://127.0.0.1:8088/recovery?code=ab12")
        );
        let refused = b"HTTP/1.0 405 Method Not Allowed\r\n\r\n{\"error\":\"POST\"}";
        assert_eq!(link_of_response(refused), None);
        let odd = b"HTTP/1.0 200 OK\r\n\r\n{\"url\":\"http://elsewhere/\"}";
        assert_eq!(link_of_response(odd), None);
        assert_eq!(
            page_url("http://127.0.0.1:8088/"),
            "http://127.0.0.1:8088/recovery"
        );
    }

    #[test]
    fn no_supervisor_no_link() {
        assert_eq!(link("127.0.0.1:1"), None);
    }

    // Epic 53 story 53.05: a record caught while the launcher writes it (empty, or not whole
    // yet) is read once it is whole; a record that is not there answers at once.
    #[test]
    fn a_record_caught_mid_write_is_read_once_whole() {
        let dir = tempfile::tempdir().unwrap();
        let p = dir.path().join("start-failed.json");
        std::fs::write(&p, "").unwrap();
        let q = p.clone();
        let writer = std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(30));
            std::fs::write(&q, r#"{"version":"2.0.0","exits":[]}"#).unwrap();
        });
        let got: Option<serde_json::Value> = read_record(&p, 20, Duration::from_millis(10));
        writer.join().unwrap();
        assert_eq!(got.unwrap()["version"], "2.0.0");
        let start = std::time::Instant::now();
        let none: Option<serde_json::Value> =
            read_record(&dir.path().join("none.json"), 20, Duration::from_millis(50));
        assert!(none.is_none());
        assert!(
            start.elapsed() < Duration::from_millis(40),
            "a missing record is not waited for"
        );
        std::fs::write(&p, "{not json").unwrap();
        let bad: Option<serde_json::Value> = read_record(&p, 3, Duration::from_millis(1));
        assert!(bad.is_none());
    }
}
