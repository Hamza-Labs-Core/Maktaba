// Server status for the menu-bar item (Epic 36, story 36.5).
//
// The supervisor (`maktaba-server serve`) serves its state as JSON at
// GET http://127.0.0.1:9103/processes (supervisor.Snapshot in
// cmd/maktaba-server/internal/supervisor/state.go). This module reads it
// with a tiny loopback HTTP/1.0 client and folds it into one status:
// running / starting / problem / stopped / paused / setup needed.
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream, ToSocketAddrs};
use std::time::Duration;

use serde::{Deserialize, Serialize};

/// The supervisor's default admin address (config.SupervisorDefaults).
pub const DEFAULT_SUPERVISOR_ADDR: &str = "127.0.0.1:9103";

/// The part of supervisor.Snapshot the app reads. Unknown fields are
/// ignored, so the supervisor can grow without breaking the app.
#[derive(Debug, Clone, Deserialize, PartialEq)]
pub struct Snapshot {
    #[serde(default)]
    pub processes: Vec<Child>,
    /// The language model server (the bundled Ollama): absent when this
    /// machine has none.
    #[serde(default)]
    pub llm: Option<Llm>,
}

/// The model server's state (llmserver.Status in cmd/maktaba-server).
#[derive(Debug, Clone, Deserialize, PartialEq)]
pub struct Llm {
    #[serde(default)]
    pub up: bool,
    #[serde(default)]
    pub ready: bool,
    #[serde(default)]
    pub models: Vec<LlmModel>,
}

/// One configured model: ready, downloading (done/total bytes), missing,
/// failed or unknown.
#[derive(Debug, Clone, Deserialize, PartialEq)]
pub struct LlmModel {
    pub name: String,
    pub state: String,
    #[serde(default)]
    pub done: u64,
    #[serde(default)]
    pub total: u64,
}

/// The menu bar's line about the language model, when the server has one.
pub fn model_line(llm: Option<&Llm>) -> Option<String> {
    let llm = llm?;
    if let Some(m) = llm.models.iter().find(|m| m.state == "downloading") {
        if m.total == 0 {
            return Some(format!("Language model: downloading {}", m.name));
        }
        let pct = m.done.min(m.total) * 100 / m.total;
        let line = format!("Language model: downloading {} ({pct}%)", m.name);
        return Some(line);
    }
    if llm.ready {
        return Some("Language model: ready".into());
    }
    if !llm.up {
        return Some("Language model: starting…".into());
    }
    if let Some(m) = llm.models.iter().find(|m| m.state == "failed") {
        let line = format!("Language model: {} did not download (retrying)", m.name);
        return Some(line);
    }
    if let Some(m) = llm.models.iter().find(|m| m.state != "ready") {
        return Some(format!("Language model: {} not downloaded", m.name));
    }
    Some("Language model: ready".into())
}

/// One supervised process (supervisor.ChildStatus).
#[derive(Debug, Clone, Deserialize, PartialEq)]
pub struct Child {
    pub role: String,
    #[serde(default)]
    pub instance: String,
    /// An on-demand process (a TV slot) that was asked for.
    #[serde(default)]
    pub wanted: bool,
    pub state: String,
    #[serde(default)]
    pub crash_loop: bool,
}

impl Child {
    fn label(&self) -> String {
        if self.instance.is_empty() {
            self.role.clone()
        } else {
            format!("{} {}", self.role, self.instance)
        }
    }
}

/// What the menu bar shows.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(tag = "kind", content = "roles", rename_all = "snake_case")]
pub enum ServerStatus {
    /// First-run setup has not finished, so there is no server to start.
    SetupNeeded,
    /// The user paused it from the menu.
    Paused,
    /// Not answering and not just started.
    Stopped,
    /// Started, or some processes are still coming up / restarting.
    Starting,
    /// Every process is up.
    Running,
    /// These processes keep failing (crash loop, failing health check).
    Problem(Vec<String>),
}

/// The result of asking the supervisor.
#[derive(Debug, Clone, PartialEq)]
pub enum Probe {
    Unreachable,
    Snapshot(Snapshot),
}

/// Fold what we know into one status.
///
/// `just_started` is true for a short while after the app started or
/// resumed the server: launchd and the supervisor need some seconds
/// before /processes answers, and "stopped" would be wrong then.
pub fn derive(setup_done: bool, paused: bool, just_started: bool, probe: &Probe) -> ServerStatus {
    if !setup_done {
        return ServerStatus::SetupNeeded;
    }
    let snap = match probe {
        Probe::Unreachable if paused => return ServerStatus::Paused,
        Probe::Unreachable if just_started => return ServerStatus::Starting,
        Probe::Unreachable => return ServerStatus::Stopped,
        Probe::Snapshot(s) => s,
    };
    let mut problems = Vec::new();
    let mut coming_up = false;
    let mut counted = 0;
    for c in &snap.processes {
        match c.state.as_str() {
            "running" if !c.crash_loop => {}
            "starting" | "backoff" if !c.crash_loop => coming_up = true,
            // exited / stopped: an on-demand process nobody asked for is
            // meant to be down; one that was asked for is on its way.
            "exited" | "stopped" if !c.wanted => continue,
            "exited" | "stopped" => coming_up = true,
            // failed, unhealthy, a crash loop, or a state we do not know
            _ => problems.push(c.label()),
        }
        counted += 1;
    }
    if !problems.is_empty() {
        ServerStatus::Problem(problems)
    } else if coming_up || counted == 0 {
        ServerStatus::Starting
    } else {
        ServerStatus::Running
    }
}

/// Parse a raw HTTP/1.x response carrying the /processes JSON.
pub fn parse_response(raw: &[u8]) -> Option<Snapshot> {
    let text = std::str::from_utf8(raw).ok()?;
    let (head, body) = text.split_once("\r\n\r\n")?;
    let status_line = head.lines().next()?;
    let code = status_line.split_whitespace().nth(1)?;
    if code != "200" {
        return None;
    }
    serde_json::from_str(body).ok()
}

/// Ask the supervisor at `addr` (host:port). Loopback only, short
/// timeouts: this runs every few seconds from the menu-bar poller.
pub fn probe(addr: &str) -> Probe {
    fetch(addr)
        .map(Probe::Snapshot)
        .unwrap_or(Probe::Unreachable)
}

fn fetch(addr: &str) -> Option<Snapshot> {
    let sock: SocketAddr = addr
        .to_socket_addrs()
        .ok()?
        .find(|a| a.ip().is_loopback())?;
    let timeout = Duration::from_secs(2);
    let mut stream = TcpStream::connect_timeout(&sock, timeout).ok()?;
    stream.set_read_timeout(Some(timeout)).ok()?;
    stream.set_write_timeout(Some(timeout)).ok()?;
    let req =
        format!("GET /processes HTTP/1.0\r\nHost: {addr}\r\nAccept: application/json\r\n\r\n");
    stream.write_all(req.as_bytes()).ok()?;
    let mut raw = Vec::new();
    // 4 MB is far above a snapshot (a few dozen processes, 50 events)
    stream.take(4 << 20).read_to_end(&mut raw).ok()?;
    parse_response(&raw)
}

/// The host:port of a supervisor URL like "http://127.0.0.1:9103".
pub fn addr_of(url: &str) -> Option<String> {
    let rest = url.strip_prefix("http://")?;
    let host = rest.split('/').next()?;
    (!host.is_empty()).then(|| host.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn child(role: &str, state: &str) -> Child {
        Child {
            role: role.into(),
            instance: String::new(),
            wanted: false,
            state: state.into(),
            crash_loop: false,
        }
    }

    fn snap(children: Vec<Child>) -> Probe {
        Probe::Snapshot(Snapshot {
            processes: children,
            llm: None,
        })
    }

    fn model(name: &str, state: &str, done: u64, total: u64) -> LlmModel {
        LlmModel {
            name: name.into(),
            state: state.into(),
            done,
            total,
        }
    }

    #[test]
    fn parses_the_language_model_state() {
        let body = r#"{"processes":[{"role":"ollama","state":"running"}],
          "llm":{"url":"http://127.0.0.1:11533","bundled":true,"up":true,"ready":false,
                 "models":[{"name":"qwen2.5:7b","state":"downloading","done":2341538385,"total":4683076771}],
                 "checked_at":"2026-10-21T10:00:00Z"}}"#;
        let raw = format!("HTTP/1.1 200 OK\r\n\r\n{body}");
        let s = parse_response(raw.as_bytes()).expect("parsed");
        assert_eq!(
            model_line(s.llm.as_ref()).as_deref(),
            Some("Language model: downloading qwen2.5:7b (49%)")
        );
        // the model server is one more process: it does not hold the status
        assert_eq!(
            derive(true, false, false, &Probe::Snapshot(s)),
            ServerStatus::Running
        );
    }

    #[test]
    fn model_lines() {
        assert_eq!(model_line(None), None);
        let mut llm = Llm {
            up: true,
            ready: true,
            models: vec![model("qwen2.5:7b", "ready", 0, 0)],
        };
        assert_eq!(
            model_line(Some(&llm)).as_deref(),
            Some("Language model: ready")
        );
        llm.ready = false;
        llm.models = vec![model("qwen2.5:7b", "downloading", 0, 0)];
        assert_eq!(
            model_line(Some(&llm)).as_deref(),
            Some("Language model: downloading qwen2.5:7b")
        );
        llm.models = vec![model("qwen2.5:7b", "failed", 0, 0)];
        assert_eq!(
            model_line(Some(&llm)).as_deref(),
            Some("Language model: qwen2.5:7b did not download (retrying)")
        );
        llm.models = vec![model("qwen2.5:7b", "missing", 0, 0)];
        assert_eq!(
            model_line(Some(&llm)).as_deref(),
            Some("Language model: qwen2.5:7b not downloaded")
        );
        llm.up = false;
        llm.models = vec![model("qwen2.5:7b", "unknown", 0, 0)];
        assert_eq!(
            model_line(Some(&llm)).as_deref(),
            Some("Language model: starting…")
        );
    }

    #[test]
    fn parses_a_real_snapshot_and_ignores_unknown_fields() {
        let body = r#"{"host":"mac","started_at":"2026-10-20T10:00:00Z","policy":{"restart":"always"},
          "processes":[{"role":"api","wanted":false,"state":"running","pid":10,"restarts":0,"crash_loop":false,"uptime_sec":12.5},
                       {"role":"tv-slot","instance":"standby","wanted":false,"state":"stopped","restarts":0,"crash_loop":false,"uptime_sec":0}],
          "events":[]}"#;
        let raw = format!("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{body}");
        let s = parse_response(raw.as_bytes()).expect("parsed");
        assert_eq!(s.processes.len(), 2);
        assert_eq!(s.processes[1].instance, "standby");
        assert_eq!(
            derive(true, false, false, &Probe::Snapshot(s)),
            ServerStatus::Running
        );
    }

    #[test]
    fn a_non_200_or_garbage_response_is_not_a_snapshot() {
        assert_eq!(parse_response(b"HTTP/1.1 500 Internal\r\n\r\n{}"), None);
        assert_eq!(parse_response(b"HTTP/1.1 200 OK\r\n\r\nnot json"), None);
        assert_eq!(parse_response(b"no head"), None);
    }

    #[test]
    fn setup_needed_wins_over_everything() {
        assert_eq!(
            derive(false, false, false, &snap(vec![child("api", "running")])),
            ServerStatus::SetupNeeded
        );
    }

    #[test]
    fn unreachable_is_paused_starting_or_stopped() {
        assert_eq!(
            derive(true, true, false, &Probe::Unreachable),
            ServerStatus::Paused
        );
        assert_eq!(
            derive(true, false, true, &Probe::Unreachable),
            ServerStatus::Starting
        );
        assert_eq!(
            derive(true, false, false, &Probe::Unreachable),
            ServerStatus::Stopped
        );
    }

    #[test]
    fn coming_up_and_empty_are_starting() {
        assert_eq!(
            derive(true, false, false, &snap(vec![])),
            ServerStatus::Starting
        );
        let s = snap(vec![child("api", "running"), child("pipeline", "starting")]);
        assert_eq!(derive(true, false, false, &s), ServerStatus::Starting);
        let s = snap(vec![child("api", "running"), child("streaming", "backoff")]);
        assert_eq!(derive(true, false, false, &s), ServerStatus::Starting);
    }

    #[test]
    fn failures_name_the_processes() {
        let mut looping = child("pipeline", "running");
        looping.crash_loop = true;
        let mut slot = child("transcribe", "failed");
        slot.instance = "1".into();
        let s = snap(vec![
            child("api", "running"),
            looping,
            slot,
            child("streaming", "unhealthy"),
        ]);
        assert_eq!(
            derive(true, false, false, &s),
            ServerStatus::Problem(vec![
                "pipeline".into(),
                "transcribe 1".into(),
                "streaming".into()
            ])
        );
    }

    #[test]
    fn idle_on_demand_processes_do_not_count() {
        let mut idle = child("tv-slot", "exited");
        idle.instance = "standby".into();
        let mut asked = child("tv-slot", "stopped");
        asked.instance = "staging".into();
        asked.wanted = true;
        assert_eq!(
            derive(
                true,
                false,
                false,
                &snap(vec![child("api", "running"), idle.clone()])
            ),
            ServerStatus::Running
        );
        assert_eq!(
            derive(
                true,
                false,
                false,
                &snap(vec![child("api", "running"), idle, asked])
            ),
            ServerStatus::Starting
        );
    }

    #[test]
    fn supervisor_address_from_url() {
        assert_eq!(
            addr_of("http://127.0.0.1:9103"),
            Some("127.0.0.1:9103".into())
        );
        assert_eq!(
            addr_of("http://127.0.0.1:9200/processes"),
            Some("127.0.0.1:9200".into())
        );
        assert_eq!(addr_of("https://example.com"), None);
        assert_eq!(addr_of("http://"), None);
    }

    #[test]
    fn probe_reads_a_live_loopback_server_and_refuses_non_loopback() {
        use std::net::TcpListener;
        let ln = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = ln.local_addr().unwrap().to_string();
        let server = std::thread::spawn(move || {
            let (mut s, _) = ln.accept().unwrap();
            let mut buf = [0u8; 1024];
            let n = s.read(&mut buf).unwrap();
            assert!(String::from_utf8_lossy(&buf[..n]).starts_with("GET /processes HTTP/1.0"));
            let body = r#"{"processes":[{"role":"api","state":"running"}]}"#;
            write!(
                s,
                "HTTP/1.0 200 OK\r\nContent-Length: {}\r\n\r\n{body}",
                body.len()
            )
            .unwrap();
        });
        assert_eq!(probe(&addr), snap(vec![child("api", "running")]));
        server.join().unwrap();
        assert_eq!(probe("10.255.255.1:9103"), Probe::Unreachable);
    }
}
