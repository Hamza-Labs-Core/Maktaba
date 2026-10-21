// Server status for the menu-bar item (Epic 36, story 36.5).
//
// The supervisor (`maktaba-server serve`) serves its state as JSON at
// GET http://127.0.0.1:9103/processes (supervisor.Snapshot in
// cmd/maktaba-server/internal/supervisor/state.go). This module reads it
// with a tiny loopback HTTP/1.0 client and folds it into one status:
// running / starting / problem / not answering / stopped / paused / setup
// needed.
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream, ToSocketAddrs};
use std::time::{Duration, Instant};

use serde::{Deserialize, Serialize};

/// The supervisor's default admin address (config.SupervisorDefaults).
pub const DEFAULT_SUPERVISOR_ADDR: &str = "127.0.0.1:9103";

/// How long "unreachable" reads as starting after the app started or resumed
/// the server, when no agent is known to run (on a Mac launchd knows, the app
/// does not).
pub const START_GRACE: Duration = Duration::from_secs(90);

/// How long a server whose agent runs (Windows, Linux) may go without
/// answering before the tray says it has a problem, and why. A healthy start
/// answers well within it: the longest step before the supervisor serves
/// /processes is the bundled Postgres's first-start bootstrap, which pgbundle
/// allows 2 minutes (readyTimeout), after serve's wait of up to 10 s for busy
/// ports (servePortWait).
pub const NO_ANSWER_LIMIT: Duration = Duration::from_secs(180);

/// The reason when the log does not tell one.
pub const NO_ANSWER: &str = "the server does not answer";

/// A reason is cut to this many characters, so the status line ("Maktaba has
/// a problem: …") fits a Windows tray tooltip (127 characters).
const REASON_MAX: usize = 100;

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
    /// What it waits on before it starts (its Prepare: the venv being built,
    /// the backup before an update) or why that failed (Epic 38 story 38.12).
    #[serde(default)]
    pub detail: String,
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

/// What the menu bar shows ({"kind": "running"}, {"kind": "problem",
/// "roles": […]}, {"kind": "not_answering", "reason": "…"}).
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
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
    Problem { roles: Vec<String> },
    /// Its agent runs, but the supervisor has not answered for
    /// NO_ANSWER_LIMIT: the launcher keeps failing, a port is taken, another
    /// install holds the launcher's lock…
    NotAnswering { reason: String },
}

/// The status as the window's starting screen reads it (`server_status`):
/// the status, and what the server waits on while it starts.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct StatusView {
    #[serde(flatten)]
    pub status: ServerStatus,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub detail: Option<String>,
}

/// The result of asking the supervisor.
#[derive(Debug, Clone, PartialEq)]
pub enum Probe {
    Unreachable,
    Snapshot(Snapshot),
}

/// What an unanswered probe means.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Waiting {
    /// Nothing says the server is on its way: it is stopped.
    Stopped,
    /// Just started, or its agent runs and it has not been quiet for long.
    Starting,
    /// Its agent runs but it has not answered for NO_ANSWER_LIMIT: why.
    NoAnswer(String),
}

/// What an unanswered probe means, from how long ago the app started or
/// resumed the server, whether its agent runs (None: the OS does not say),
/// and how long it has been quiet (Quiet). `reason` is read only when it is
/// needed.
pub fn waiting(
    since_start: Duration,
    alive: Option<bool>,
    quiet_for: Duration,
    reason: impl FnOnce() -> Option<String>,
) -> Waiting {
    match alive {
        Some(true) if quiet_for < NO_ANSWER_LIMIT => Waiting::Starting,
        Some(true) => Waiting::NoAnswer(reason().unwrap_or_else(|| NO_ANSWER.into())),
        _ if since_start < START_GRACE => Waiting::Starting,
        _ => Waiting::Stopped,
    }
}

/// When the server last answered, or its agent was seen starting: the
/// poller's measure of how long the server has been quiet.
#[derive(Debug, Clone, Copy)]
pub struct Quiet {
    since: Instant,
    alive: Option<bool>,
}

impl Quiet {
    pub fn new(now: Instant) -> Self {
        Self {
            since: now,
            alive: None,
        }
    }

    /// One poll. An answer, or an agent that was not running at the last
    /// poll (a new start: the watchdog, a sign-in), starts the clock again.
    /// Returns how long the server has been quiet.
    pub fn poll(&mut self, now: Instant, answered: bool, alive: Option<bool>) -> Duration {
        if answered || (alive == Some(true) && self.alive == Some(false)) {
            self.since = now;
        }
        self.alive = alive;
        now.saturating_duration_since(self.since)
    }
}

/// Fold what we know into one status. `waiting` says what an unanswered
/// probe means: launchd or the agent and the supervisor need some seconds
/// before /processes answers, and "stopped" would be wrong then.
pub fn derive(setup_done: bool, paused: bool, waiting: &Waiting, probe: &Probe) -> ServerStatus {
    if !setup_done {
        return ServerStatus::SetupNeeded;
    }
    let snap = match probe {
        Probe::Unreachable if paused => return ServerStatus::Paused,
        Probe::Unreachable => {
            return match waiting {
                Waiting::Stopped => ServerStatus::Stopped,
                Waiting::Starting => ServerStatus::Starting,
                Waiting::NoAnswer(reason) => ServerStatus::NotAnswering {
                    reason: reason.clone(),
                },
            }
        }
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
        ServerStatus::Problem { roles: problems }
    } else if coming_up || counted == 0 {
        ServerStatus::Starting
    } else {
        ServerStatus::Running
    }
}

/// What the server waits on: the detail of the first process that is not
/// running and says why (the venv being built, the backup before an update,
/// or why that failed); None when every process is up or none says.
pub fn waiting_on(probe: &Probe) -> Option<String> {
    let Probe::Snapshot(s) = probe else {
        return None;
    };
    s.processes
        .iter()
        .filter(|c| c.state != "running" && !c.detail.trim().is_empty())
        .map(|c| c.detail.trim().to_string())
        .next()
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

/// Why the server does not answer, from the end of server.log, when it can
/// tell. The latest of: the launcher's fatal line (`maktaba-launcher: …`),
/// maktaba-server's (`Error: …`), a startup step of serve that failed
/// (`[serve] … failed …`), a server the launcher could not start, or the
/// agent's `cannot start …`. It looks back no further than the launcher's
/// last two starts (the run in progress and the one before it) or a serve that
/// got as far as serving its state; else, when the agent says the launcher
/// keeps exiting, that.
pub fn failure_in_log(tail: &str) -> Option<String> {
    let mut starts = 0;
    let mut exited = None;
    for line in tail.lines().rev() {
        let line = line.trim();
        // the agent's lines: "<RFC 3339 time> [agent] …"
        if let Some(said) = line
            .split_once(" [agent] ")
            .filter(|(at, _)| !at.contains(' '))
            .map(|(_, said)| said)
        {
            if said.starts_with("started the launcher") {
                starts += 1;
                if starts == 2 {
                    break;
                }
            } else if said.starts_with("cannot start ") {
                return Some(cut(said));
            } else if let Some(how) = said
                .strip_prefix("the launcher exited (")
                .and_then(|r| r.strip_suffix(')'))
            {
                exited.get_or_insert_with(|| format!("the launcher keeps stopping ({how})"));
            }
            continue;
        }
        if line.starts_with("[serve] supervisor state on") {
            break;
        }
        if let Some(why) = line
            .strip_prefix("maktaba-launcher: ")
            .or_else(|| line.strip_prefix("Error: "))
        {
            return Some(cut(why));
        }
        if let Some(why) = line
            .strip_prefix("[serve] ")
            .filter(|s| s.contains(" failed"))
        {
            return Some(cut(why));
        }
        if let Some(why) = line
            .strip_prefix("[launcher] ")
            .and_then(launcher_start_error)
        {
            return Some(cut(&why));
        }
    }
    exited.map(|e| cut(&e))
}

/// A launcher event line's reason when it could not start the server
/// (`{"event":"launcher_start_error","detail":{"err":"…"}}`).
fn launcher_start_error(json: &str) -> Option<String> {
    let v: serde_json::Value = serde_json::from_str(json).ok()?;
    if v.get("event")?.as_str()? != "launcher_start_error" {
        return None;
    }
    let err = v.get("detail")?.get("err")?.as_str()?;
    Some(format!("cannot start maktaba-server: {err}"))
}

/// A reason no longer than REASON_MAX characters.
fn cut(s: &str) -> String {
    let s = s.trim();
    if s.chars().count() <= REASON_MAX {
        return s.to_string();
    }
    let mut out: String = s.chars().take(REASON_MAX - 1).collect();
    out.push('…');
    out
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
            detail: String::new(),
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
            derive(true, false, &Waiting::Stopped, &Probe::Snapshot(s)),
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
            derive(true, false, &Waiting::Stopped, &Probe::Snapshot(s)),
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
            derive(
                false,
                false,
                &Waiting::Stopped,
                &snap(vec![child("api", "running")])
            ),
            ServerStatus::SetupNeeded
        );
    }

    #[test]
    fn unreachable_is_paused_starting_or_stopped() {
        assert_eq!(
            derive(true, true, &Waiting::Stopped, &Probe::Unreachable),
            ServerStatus::Paused
        );
        assert_eq!(
            derive(true, false, &Waiting::Starting, &Probe::Unreachable),
            ServerStatus::Starting
        );
        assert_eq!(
            derive(true, false, &Waiting::Stopped, &Probe::Unreachable),
            ServerStatus::Stopped
        );
        let why = Waiting::NoAnswer("port 8080 is taken".into());
        assert_eq!(
            derive(true, false, &why, &Probe::Unreachable),
            ServerStatus::NotAnswering {
                reason: "port 8080 is taken".into()
            }
        );
        // paused wins; an answer wins
        assert_eq!(
            derive(true, true, &why, &Probe::Unreachable),
            ServerStatus::Paused
        );
        assert_eq!(
            derive(true, false, &why, &snap(vec![child("api", "running")])),
            ServerStatus::Running
        );
    }

    #[test]
    fn a_live_agent_whose_server_stays_quiet_turns_into_a_problem() {
        let s = Duration::from_secs;
        let none = || None;
        // a live agent: starting, until NO_ANSWER_LIMIT of quiet
        assert_eq!(waiting(s(600), Some(true), s(60), none), Waiting::Starting);
        assert_eq!(
            waiting(s(600), Some(true), NO_ANSWER_LIMIT, none),
            Waiting::NoAnswer(NO_ANSWER.into())
        );
        assert_eq!(
            waiting(s(600), Some(true), NO_ANSWER_LIMIT, || Some(
                "another launcher runs this install".into()
            )),
            Waiting::NoAnswer("another launcher runs this install".into())
        );
        // no agent (or none known, a Mac): starting for START_GRACE, then stopped
        assert_eq!(waiting(s(10), Some(false), s(10), none), Waiting::Starting);
        assert_eq!(
            waiting(START_GRACE, Some(false), s(600), none),
            Waiting::Stopped
        );
        assert_eq!(waiting(START_GRACE, None, s(600), none), Waiting::Stopped);
        // the reason is read only when it is shown
        let read = std::cell::Cell::new(false);
        let _ = waiting(s(600), Some(true), s(1), || {
            read.set(true);
            None
        });
        assert!(!read.get());
    }

    #[test]
    fn the_quiet_clock_restarts_on_an_answer_or_a_new_agent() {
        let t0 = Instant::now();
        let at = |secs| t0 + Duration::from_secs(secs);
        let mut q = Quiet::new(t0);
        assert_eq!(q.poll(at(3), false, Some(true)), Duration::from_secs(3));
        assert_eq!(q.poll(at(6), true, Some(true)), Duration::ZERO, "an answer");
        assert_eq!(q.poll(at(9), false, Some(true)), Duration::from_secs(3));
        // the agent died, then the watchdog started a new one
        assert_eq!(q.poll(at(12), false, Some(false)), Duration::from_secs(6));
        assert_eq!(q.poll(at(15), false, Some(true)), Duration::ZERO);
        assert_eq!(q.poll(at(200), false, Some(true)), Duration::from_secs(185));
        // a window that opens on a running agent does not restart it
        let mut q = Quiet::new(t0);
        assert_eq!(q.poll(at(3), false, Some(true)), Duration::from_secs(3));
    }

    #[test]
    fn the_log_tells_why_the_server_does_not_answer() {
        let agent = |s: &str| format!("2026-10-21T10:00:00Z [agent] {s}");
        // an old install's launcher holds the lock: the launcher's own line
        let lock = [
            agent("started the launcher (pid 10)"),
            r"maktaba-launcher: another launcher runs this install (C:\Users\me\AppData\Local\Maktaba): already_running".into(),
            agent("the launcher exited (exit code: 1)"),
            agent("starting it again in 2000 ms"),
            agent("started the launcher (pid 11)"),
        ]
        .join("\n");
        assert_eq!(
            failure_in_log(&lock).as_deref(),
            Some(
                r"another launcher runs this install (C:\Users\me\AppData\Local\Maktaba): already_running"
            )
        );
        // serve refuses a taken port (cobra's "Error: "), the launcher restarts it
        let port = [
            agent("started the launcher (pid 10)"),
            r#"[launcher] {"ts":"2026-10-21T10:00:00Z","event":"launcher_start","version":"app"}"#.into(),
            "Error: web UI cannot listen on 127.0.0.1:8088 (web_listen in server.toml): bind: address in use".into(),
            r#"[launcher] {"ts":"2026-10-21T10:00:01Z","event":"launcher_child_exit","version":"app","detail":{"exit":1}}"#.into(),
        ]
        .join("\n");
        assert_eq!(
            failure_in_log(&port).as_deref(),
            Some("web UI cannot listen on 127.0.0.1:8088 (web_listen in server.toml): bind: address in use")
        );
        // the state endpoint itself could not bind
        let admin = "[serve] supervisor state on http://127.0.0.1:9103/processes\n\
                     [serve] supervisor admin endpoint on 127.0.0.1:9103 failed (continuing): bind";
        assert_eq!(
            failure_in_log(admin).as_deref(),
            Some("supervisor admin endpoint on 127.0.0.1:9103 failed (continuing): bind")
        );
        // the launcher could not start the server
        let start = r#"[launcher] {"event":"launcher_start_error","version":"app","detail":{"err":"file does not exist"}}"#;
        assert_eq!(
            failure_in_log(start).as_deref(),
            Some("cannot start maktaba-server: file does not exist")
        );
        // no line says why, but the launcher keeps exiting
        let exits = [
            agent("started the launcher (pid 10)"),
            agent("the launcher exited (exit code: 3)"),
            agent("starting it again in 1000 ms"),
        ]
        .join("\n");
        assert_eq!(
            failure_in_log(&exits).as_deref(),
            Some("the launcher keeps stopping (exit code: 3)")
        );
        // an error from before the last two starts, or before serve got going, is old news
        let old = [
            "Error: an old problem".to_string(),
            agent("started the launcher (pid 10)"),
            agent("started the launcher (pid 11)"),
        ]
        .join("\n");
        assert_eq!(failure_in_log(&old), None);
        let served = "Error: an old problem\n[serve] supervisor state on http://127.0.0.1:9103/processes\nsome role's line";
        assert_eq!(failure_in_log(served), None);
        assert_eq!(failure_in_log(""), None);
        // long reasons are cut for the tooltip
        let long = format!("Error: {}", "x".repeat(300));
        let got = failure_in_log(&long).unwrap();
        assert_eq!(got.chars().count(), REASON_MAX);
        assert!(got.ends_with('…'));
    }

    #[test]
    fn statuses_serialize_for_the_starting_screen() {
        let json = |s: &ServerStatus| serde_json::to_string(s).unwrap();
        assert_eq!(json(&ServerStatus::Running), r#"{"kind":"running"}"#);
        assert_eq!(
            json(&ServerStatus::Problem {
                roles: vec!["pipeline".into()]
            }),
            r#"{"kind":"problem","roles":["pipeline"]}"#
        );
        assert_eq!(
            json(&ServerStatus::NotAnswering {
                reason: "the server does not answer".into()
            }),
            r#"{"kind":"not_answering","reason":"the server does not answer"}"#
        );
    }

    #[test]
    fn coming_up_and_empty_are_starting() {
        assert_eq!(
            derive(true, false, &Waiting::Stopped, &snap(vec![])),
            ServerStatus::Starting
        );
        let s = snap(vec![child("api", "running"), child("pipeline", "starting")]);
        assert_eq!(
            derive(true, false, &Waiting::Stopped, &s),
            ServerStatus::Starting
        );
        let s = snap(vec![child("api", "running"), child("streaming", "backoff")]);
        assert_eq!(
            derive(true, false, &Waiting::Stopped, &s),
            ServerStatus::Starting
        );
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
            derive(true, false, &Waiting::Stopped, &s),
            ServerStatus::Problem {
                roles: vec!["pipeline".into(), "transcribe 1".into(), "streaming".into()]
            }
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
                &Waiting::Stopped,
                &snap(vec![child("api", "running"), idle.clone()])
            ),
            ServerStatus::Running
        );
        assert_eq!(
            derive(
                true,
                false,
                &Waiting::Stopped,
                &snap(vec![child("api", "running"), idle, asked])
            ),
            ServerStatus::Starting
        );
    }

    #[test]
    fn waiting_on_is_the_first_detail_of_a_process_not_up() {
        let mut venv = child("pipeline", "starting");
        venv.detail = "Installing the transcription tools (40%)".into();
        let mut backup = child("api", "failed");
        backup.detail = "the backup before the update failed: disk full".into();
        let mut done = child("streaming", "running");
        done.detail = "stale".into();
        assert_eq!(
            waiting_on(&snap(vec![done.clone(), venv.clone(), backup])),
            Some("Installing the transcription tools (40%)".into())
        );
        assert_eq!(
            waiting_on(&snap(vec![done, child("api", "starting")])),
            None
        );
        assert_eq!(waiting_on(&Probe::Unreachable), None);
        // the detail rides in /processes beside the state
        let body = r#"{"processes":[{"role":"pipeline","state":"starting","detail":"Building"}]}"#;
        let raw = format!("HTTP/1.1 200 OK\r\n\r\n{body}");
        let s = parse_response(raw.as_bytes()).expect("parsed");
        assert_eq!(waiting_on(&Probe::Snapshot(s)), Some("Building".into()));
    }

    #[test]
    fn the_starting_screen_reads_the_status_with_its_detail() {
        let v = StatusView {
            status: ServerStatus::Starting,
            detail: Some("Installing".into()),
        };
        assert_eq!(
            serde_json::to_value(&v).unwrap(),
            serde_json::json!({"kind": "starting", "detail": "Installing"})
        );
        let v = StatusView {
            status: ServerStatus::Problem {
                roles: vec!["api".into()],
            },
            detail: None,
        };
        assert_eq!(
            serde_json::to_value(&v).unwrap(),
            serde_json::json!({"kind": "problem", "roles": ["api"]})
        );
        let v = StatusView {
            status: ServerStatus::Problem {
                roles: vec!["api".into()],
            },
            detail: Some("the backup before the update failed".into()),
        };
        assert_eq!(
            serde_json::to_value(&v).unwrap(),
            serde_json::json!({
                "kind": "problem",
                "roles": ["api"],
                "detail": "the backup before the update failed"
            })
        );
        // a server that does not answer carries its reason beside the kind
        let v = StatusView {
            status: ServerStatus::NotAnswering {
                reason: "another launcher runs this install".into(),
            },
            detail: None,
        };
        assert_eq!(
            serde_json::to_value(&v).unwrap(),
            serde_json::json!({
                "kind": "not_answering",
                "reason": "another launcher runs this install"
            })
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
