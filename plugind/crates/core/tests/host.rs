//! The plugin host against real components (story 54.02 T02–T10, 54.09 T04 host side).
//!
//! The guests are built by `make plugind-guests` (plugind/guests, wasm32-wasip2) before
//! `cargo test`; a missing guest fails the test with that instruction, never skips it.

use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use plugind_core::api::{Answer, ApiError, PluginApi};
use plugind_core::logging::{Logs, MemorySink, BURST, LINES_PER_SECOND};
use plugind_core::protocol::{self, Job, Limits, Outcome, Request, Response, Run, Subject};
use plugind_core::{Host, HostConfig};
use serde_json::{json, Value};

const ID: &str = "dev.example.hostile";
const VERSION: &str = "1.0.0";

fn guest(name: &str) -> Vec<u8> {
    let p = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../guests/target/wasm32-wasip2/release")
        .join(format!("{name}.wasm"));
    std::fs::read(&p).unwrap_or_else(|_| panic!("{} is missing: run `make plugind-guests` first", p.display()))
}

/// A component in WAT whose `run` grows a table without end (54.02 T04's table bomb).
const TABLE_BOMB: &str = r#"
(component
  (core module $m
    (memory (export "memory") 1)
    (table $t 1 funcref)
    (func (export "cabi_realloc") (param i32 i32 i32 i32) (result i32) i32.const 1024)
    (func (export "run") (param i32 i32) (result i32)
      (drop (table.grow $t (ref.null func) (i32.const 1000000)))
      i32.const 0))
  (core instance $i (instantiate $m))
  (type $ro (record (field "output-hash" string) (field "summary" string)))
  (export $ro2 "run-output" (type $ro))
  (type $re (record (field "code" string) (field "message" string)))
  (export $re2 "run-error" (type $re))
  (func $run (param "stage" string) (result (result $ro2 (error $re2)))
    (canon lift (core func $i "run") (memory (core memory $i "memory")) (realloc (core func $i "cabi_realloc"))))
  (export "run" (func $run)))
"#;

/// A component that only imports wasi:http (story 54.02 T02).
const HTTP_IMPORT: &str = r#"(component (import "wasi:http/outgoing-handler@0.2.0" (instance)))"#;

#[derive(Default)]
struct MockApi {
    calls: Mutex<Vec<(String, String, Value)>>,
}

impl PluginApi for MockApi {
    fn call(&self, token: &str, interface: &str, function: &str, body: &Value) -> Result<Answer, ApiError> {
        assert_eq!(token, "secret-token", "the host must attach the job's token");
        self.calls.lock().unwrap().push((interface.into(), function.into(), body.clone()));
        // a request of a stage for itself would loop (54.40): the API refuses it
        if (interface, function) == ("queue", "request") && body["stage"] == "loop_self" {
            return Err(ApiError::Loop);
        }
        Ok(Answer::Json(match (interface, function) {
            ("data", "get") => json!({"value": null}),
            ("events", "emit") => json!({"stages": ["p_0123abcd_hear"]}),
            _ => json!({}),
        }))
    }
}

struct Fixture {
    _dir: tempfile::TempDir,
    root: PathBuf,
    host: Arc<Host>,
    api: Arc<MockApi>,
    sink: Arc<MemorySink>,
}

fn manifest(caps: &[&str]) -> String {
    let caps = caps.iter().map(|c| format!("\"{c}\"")).collect::<Vec<_>>().join(", ");
    format!(
        r#"schema = 1
id = "{ID}"
version = "{VERSION}"
plugin_api = "1.0"
maktaba = ">=0.1.0"
licence = "MIT"

[strings]
name = "plugin.name"
summary = "plugin.summary"

[capabilities]
optional = [{caps}]

[resources]
memory_mb = 64
timeout_s = 30
fuel = 1000000000000

[[stage]]
name = "work"
subject = "video"
export = "run-work"
version = 1
"#
    )
}

impl Fixture {
    fn new(skip_audit: bool) -> Self {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().join("plugins");
        std::fs::create_dir_all(&root).unwrap();
        let api = Arc::new(MockApi::default());
        let sink = Arc::new(MemorySink::default());
        let host = Host::new(HostConfig {
            plugins_dir: root.clone(),
            cache_dir: root.join("cache"),
            pool_size: 4,
            api: api.clone(),
            logs: Arc::new(Logs::new(sink.clone())),
            skip_audit,
            sandbox: "none".into(),
            sandbox_missing: vec![],
        })
        .unwrap();
        Self { _dir: dir, root, host, api, sink }
    }

    /// Writes a package and answers its path.
    fn package(&self, folder: &str, wasm: &[u8], caps: &[&str]) -> String {
        let p = self.root.join(ID).join(folder);
        std::fs::create_dir_all(p.join("i18n")).unwrap();
        std::fs::write(p.join("manifest.toml"), manifest(caps)).unwrap();
        std::fs::write(p.join("i18n/en.json"), r#"{"plugin.name":"Hostile","plugin.summary":"Misbehaves."}"#).unwrap();
        std::fs::write(p.join("i18n/ar.json"), r#"{"plugin.name":"مؤذية","plugin.summary":"تسيء التصرف."}"#).unwrap();
        std::fs::write(p.join("plugin.wasm"), wasm).unwrap();
        p.to_string_lossy().into_owned()
    }

    fn run(&self, path: &str, stage: &str, grants: &[&str], limits: Limits) -> protocol::RunResult {
        self.run_with(path, stage, grants, limits, Box::new(|_, _| {}))
    }

    fn run_with(
        &self,
        path: &str,
        stage: &str,
        grants: &[&str],
        limits: Limits,
        progress: plugind_core::host::Progress,
    ) -> protocol::RunResult {
        self.host.run(&req(path, stage, grants, limits), progress)
    }
}

fn req(path: &str, stage: &str, grants: &[&str], limits: Limits) -> Run {
    Run {
        invocation_id: format!("inv-{stage}"),
        plugin_id: ID.into(),
        version: VERSION.into(),
        package_path: path.into(),
        export: format!("run-{stage}"),
        job: Job {
            id: "job-1".into(),
            stage: stage.into(),
            subject: Subject { kind: "video".into(), id: "v1".into() },
            payload: "{\"a\":1}".into(),
            attempt: 1,
            checkpoint: Some(protocol::b64(b"earlier")),
        },
        settings: "{\"threshold\":3}".into(),
        grants: grants.iter().map(|g| g.to_string()).collect(),
        token: "secret-token".into(),
        limits,
    }
}

const ALL: &[&str] = &["tags.write", "jobs.enqueue"];

#[test]
fn hello_runs() {
    let f = Fixture::new(false);
    let p = f.package("hello", &guest("hello"), &[]);
    let r = f.run(&p, "work", &[], Limits::default());
    assert_eq!(r.outcome, Outcome::Ok, "{r:?}");
    assert_eq!(r.output_hash, "h-v1");
    let lines = f.sink.0.lock().unwrap();
    let line: Value = serde_json::from_str(lines.iter().find(|l| l.contains("\"hello\"")).unwrap()).unwrap();
    assert_eq!(line["service"], format!("plugin.{ID}"));
    assert_eq!(line["job_id"], "job-1");
    assert_eq!(line["attrs"]["stage"], "work");
}

/// T02: components importing sockets, filesystem, stdin or http are refused at preload.
#[test]
fn forbidden_imports_are_refused_by_the_audit() {
    let f = Fixture::new(false);
    let p = f.package("std", &guest("hostile_std"), &[]);
    let e = f.host.preload(ID, VERSION, &p).unwrap_err();
    assert_eq!(e.code, "plugin_forbidden_import", "{e}");
    let p = f.package("http", &wat::parse_str(HTTP_IMPORT).unwrap(), &[]);
    let e = f.host.preload(ID, VERSION, &p).unwrap_err();
    assert_eq!(e.code, "plugin_forbidden_import", "{e}");
    assert!(e.detail.starts_with("wasi:http/"), "{e}");
}

/// T03: with the audit bypassed, the linker alone refuses them: they cannot instantiate.
#[test]
fn forbidden_imports_cannot_link_without_the_audit() {
    let f = Fixture::new(true);
    for (folder, wasm) in [("std", guest("hostile_std")), ("http", wat::parse_str(HTTP_IMPORT).unwrap())] {
        let p = f.package(folder, &wasm, &[]);
        let e = f.host.preload(ID, VERSION, &p).unwrap_err();
        assert_eq!(e.code, "plugin_link", "{folder}: {e}");
    }
}

/// T04: a loop, a memory bomb, a recursion bomb and a table bomb end their job, not the host.
#[test]
fn runaway_jobs_end_and_the_host_serves_the_next() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    f.host.preload(ID, VERSION, &p).unwrap();
    // timed from the job's start: the preload (compiling the guest) is not part of how fast
    // a runaway job ends, and on a slow runner it alone pushed this past the bound
    let t = Instant::now();
    // fuel well above what a second of looping burns, so the wall deadline ends it
    let r = f.run(&p, "loop", &[], Limits { timeout_ms: 1_000, fuel: 1_000_000_000_000, ..Default::default() });
    let took = t.elapsed();
    assert_eq!(r.outcome, Outcome::Timeout, "{r:?}");
    assert_eq!(r.error_code, "plugin_timeout");
    // N7: within the deadline plus 200 ms (a tick and the unwind) in a release build; a
    // debug build sharing the machine with the other tests gets more room
    // (CI's release step checks the 200 ms; a debug build on a busy runner only ends)
    let most = if cfg!(debug_assertions) { 15_000 } else { 1_250 };
    assert!(took >= Duration::from_millis(1_000) && took < Duration::from_millis(most), "took {took:?}");
    let r = f.run(&p, "memory", &[], Limits { memory_mb: 32, ..Default::default() });
    assert_eq!(r.outcome, Outcome::Memory, "{r:?}");
    assert!(r.peak_memory <= 32 << 20);
    let r = f.run(&p, "recurse", &[], Limits::default());
    assert_eq!(r.outcome, Outcome::Trap, "{r:?}");
    let r = f.run(&p, "trap", &[], Limits::default());
    assert_eq!(r.outcome, Outcome::Trap, "{r:?}");
    assert_eq!(r.error_code, "plugin_trap");
    let r = f.run(&p, "ok", &[], Limits::default());
    assert_eq!(r.outcome, Outcome::Ok, "{r:?}");

    // a host loads one build per (plugin, version): the table bomb gets its own
    let g = Fixture::new(false);
    let tb = g.package("table", &wat::parse_str(TABLE_BOMB).unwrap(), &[]);
    let r = g.run(&tb, "x", &[], Limits::default());
    assert_eq!(r.outcome, Outcome::Memory, "{r:?}");
}

/// The fuel budget ends a job too, deterministically.
#[test]
fn fuel_ends_a_job() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    let r = f.run(&p, "loop", &[], Limits { fuel: 50_000_000, ..Default::default() });
    assert_eq!(r.outcome, Outcome::Timeout, "{r:?}");
    assert!(r.fuel_used >= 49_000_000, "{r:?}");
}

/// T05: a gated interface imported without a declared capability.
#[test]
fn undeclared_imports_are_refused() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), &[]);
    let e = f.host.preload(ID, VERSION, &p).unwrap_err();
    assert_eq!(e.code, "plugin_undeclared_import", "{e}");
}

/// T06: an ungranted call answers capability inside the host; the API is never called.
#[test]
fn ungranted_calls_never_leave_the_host() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    let r = f.run(&p, "deny", &[], Limits::default());
    assert_eq!(r.outcome, Outcome::Refused, "{r:?}");
    assert_eq!(r.error_code, "plugin_capability");
    assert!(f.api.calls.lock().unwrap().iter().all(|(i, _, _)| i != "write"));
}

/// T08: the compile cache is used, and a stale or foreign file is recompiled.
#[test]
fn compile_cache() {
    let f = Fixture::new(false);
    let wasm = guest("hello");
    f.host.compile(&wasm).unwrap();
    let cache = f.root.join("cache");
    let files: Vec<_> = std::fs::read_dir(&cache).unwrap().map(|e| e.unwrap().path()).collect();
    assert_eq!(files.len(), 1);
    let name = files[0].file_name().unwrap().to_string_lossy().into_owned();
    assert!(name.ends_with(&format!("-{}.cwasm", plugind_core::engine::WASMTIME_VERSION)), "{name}");
    std::fs::write(&files[0], b"not a compiled component").unwrap();
    f.host.compile(&wasm).unwrap();
    assert!(std::fs::read(&files[0]).unwrap().len() > 100, "the stale file was rewritten");
}

/// T09: limits above the manifest's are clamped and said so.
#[test]
fn limits_are_clamped() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    let r = f.run(&p, "ok", &[], Limits { memory_mb: 4096, ..Default::default() });
    assert_eq!(r.outcome, Outcome::Ok);
    assert!(r.limits_clamped);
    assert!(f.sink.0.lock().unwrap().iter().any(|l| l.contains("limits_clamped")));
}

/// T10: Cancel stops a job at the next tick and hands back its checkpoint.
#[test]
fn cancel_returns_the_checkpoint() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    let host = f.host.clone();
    let t = std::thread::spawn(move || host.run(&req(&p, "cancel", &[], Limits::default()), Box::new(|_, _| {})));
    // the job registers once it starts (after the first compile)
    let started = Instant::now();
    while !f.host.cancel("inv-cancel") {
        assert!(started.elapsed() < Duration::from_secs(30), "the job never started");
        std::thread::sleep(Duration::from_millis(20));
    }
    let r = t.join().unwrap();
    assert_eq!(r.outcome, Outcome::Refused, "{r:?}");
    assert_eq!(r.error_code, "cancelled");
    assert_eq!(r.checkpoint.as_deref().and_then(protocol::unb64).as_deref(), Some(&b"before-cancel"[..]));
}

#[test]
fn progress_requests_and_job_data_reach_their_places() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    let seen = Arc::new(Mutex::new(Vec::new()));
    let s = seen.clone();
    let r = f.run_with(
        &p,
        "progress",
        &[],
        Limits::default(),
        Box::new(move |pct, cp| s.lock().unwrap().push((pct, cp.map(|c| c.to_vec())))),
    );
    assert_eq!(r.outcome, Outcome::Ok, "{r:?}");
    assert_eq!(*seen.lock().unwrap(), vec![(Some(50), None), (None, Some(b"half".to_vec()))]);

    let r = f.run(&p, "request", &["jobs.enqueue"], Limits::default());
    assert_eq!(r.outcome, Outcome::Ok, "{r:?}");
    assert_eq!(r.requests.len(), 1);
    assert_eq!(r.requests[0].stage, "other");

    let r = f.run(&p, "echo", &["jobs.enqueue", "tags.write"], Limits::default());
    let s: Value = serde_json::from_str(&r.summary).unwrap();
    assert_eq!(s, json!({"payload": {"a": 1}, "settings": {"threshold": 3}, "grants": 2, "last": 7}));
}

/// 54.40 T02, T06 (host side): an emitted event comes back in the result with the stages
/// the API named; a guest that keeps asking for itself after the API refused the loop
/// still fails with plugin_loop, and nothing it asked for is kept.
#[test]
fn events_and_loops() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    let r = f.run(&p, "emit", &[], Limits::default());
    assert_eq!(r.outcome, Outcome::Ok, "{r:?}");
    assert_eq!(r.events.len(), 1);
    assert_eq!(r.events[0].name, "hostile.done");
    assert_eq!(r.events[0].stages, vec!["p_0123abcd_hear".to_string()]);

    let r = f.run(&p, "loop_self", &["jobs.enqueue"], Limits::default());
    assert_eq!(r.outcome, Outcome::Refused, "{r:?}");
    assert_eq!(r.error_code, "plugin_loop");
    assert!(r.requests.is_empty() && r.events.is_empty());
}

/// 54.09 T04 (host side): a log flood is cut to the limit and counted; the job finishes.
#[test]
fn log_flood_is_throttled() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    let t = Instant::now();
    let r = f.run(&p, "flood_log", &[], Limits::default());
    let elapsed = t.elapsed().as_secs_f64();
    assert_eq!(r.outcome, Outcome::Ok);
    assert!(r.log_dropped >= 9_000, "{r:?}");
    let lines = f.sink.0.lock().unwrap();
    // the burst, what the rate refilled while the job ran (a slow runner refills more), and
    // the host's own few lines (log_dropped among them)
    let most = BURST + elapsed * LINES_PER_SECOND + 10.0;
    assert!((lines.len() as f64) <= most, "{} lines in {elapsed:.2}s, at most {most:.0}", lines.len());
    assert!(lines.iter().any(|l| l.contains("log_dropped")));
}

/// A flood of API calls is forwarded (the API's token bucket answers plugin_rate, 54.04);
/// the job still finishes.
#[test]
fn api_flood_finishes() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    let r = f.run(&p, "flood_api", &[], Limits::default());
    assert_eq!(r.outcome, Outcome::Ok);
    assert_eq!(f.api.calls.lock().unwrap().iter().filter(|(i, fu, _)| i == "data" && fu == "get").count(), 300);
}

#[test]
fn packages_outside_the_plugins_folder_are_refused() {
    let f = Fixture::new(false);
    let e = f.host.preload(ID, VERSION, "/etc").unwrap_err();
    assert_eq!(e.code, "plugin_bad_path");
    let p = f.package("../../escape", &guest("hello"), &[]);
    let e = f.host.preload(ID, VERSION, &p).unwrap_err();
    assert_eq!(e.code, "plugin_bad_path", "{p}");
}

#[test]
fn a_manifest_for_another_plugin_is_refused() {
    let f = Fixture::new(false);
    let p = f.package("hello", &guest("hello"), &[]);
    let e = f.host.preload("dev.example.other", VERSION, &p).unwrap_err();
    assert_eq!(e.code, "plugin_bad_manifest");
}

/// The dispatch protocol over a real unix socket: progress frames, then the result.
#[test]
fn dispatch_over_the_socket() {
    let f = Fixture::new(false);
    let p = f.package("hostile", &guest("hostile"), ALL);
    let sock = f.root.parent().unwrap().join("plugind.sock");
    let listener = plugind_core::uds::bind(&sock).unwrap();
    let host = f.host.clone();
    std::thread::spawn(move || plugind_core::serve::serve(host, listener));

    let mut c = plugind_core::uds::connect(&sock).unwrap();
    protocol::write_frame(&mut c, &Request::Health).unwrap();
    let Response::Health(h) = protocol::read_frame(&mut c).unwrap() else { panic!() };
    assert!(h.ok && h.ready);
    assert_eq!(h.plugin_api, "1.1");

    let mut c = plugind_core::uds::connect(&sock).unwrap();
    protocol::write_frame(&mut c, &Request::Run(Box::new(req(&p, "progress", &[], Limits::default())))).unwrap();
    let mut frames = Vec::new();
    loop {
        let r: Response = protocol::read_frame(&mut c).unwrap();
        let done = matches!(r, Response::Result(_));
        frames.push(r);
        if done {
            break;
        }
    }
    assert!(matches!(frames[0], Response::Progress { pct: 50, .. }));
    let Response::Result(r) = frames.last().unwrap() else { panic!() };
    assert_eq!(r.outcome, Outcome::Ok);
}

/// N1 (bench, release only): instantiate + run of a compiled, cached component.
#[test]
#[ignore = "bench: cargo test --release -- --ignored"]
fn bench_job_start() {
    let f = Fixture::new(false);
    let p = f.package("hello", &guest("hello"), &[]);
    f.host.preload(ID, VERSION, &p).unwrap();
    let mut times: Vec<Duration> = (0..1000)
        .map(|_| {
            let t = Instant::now();
            let r = f.run(&p, "work", &[], Limits::default());
            assert_eq!(r.outcome, Outcome::Ok);
            t.elapsed()
        })
        .collect();
    times.sort();
    let p95 = times[949];
    println!("N1 job start p95 {p95:?}, p50 {:?}", times[499]);
    assert!(p95 < Duration::from_millis(20), "N1: p95 {p95:?}");
}
