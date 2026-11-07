//! The real maktaba-plugind binary, sandboxed, end to end (story 54.03): it starts in its
//! OS sandbox, answers health, runs a plugin job that calls the plugin API over the unix
//! socket, and exits 78 when a required sandbox step fails (T05). These are the positive
//! paths only; the escape probes run in CI (`plugins-sandbox`), never here.

use std::io::{BufRead, BufReader, Read, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

use plugind_core::protocol::{self, Job, Limits, Outcome, Request, Response, Run, Subject};
use plugind_core::uds;

const ID: &str = "dev.example.hostile";

fn guest(name: &str) -> Vec<u8> {
    let p = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../guests/target/wasm32-wasip2/release")
        .join(format!("{name}.wasm"));
    std::fs::read(&p).unwrap_or_else(|_| panic!("{} is missing: run `make plugind-guests` first", p.display()))
}

struct Home {
    _dir: tempfile::TempDir,
    data: PathBuf,
}

fn home() -> Home {
    let dir = tempfile::tempdir().unwrap();
    let data = dir.path().canonicalize().unwrap().join("data");
    std::fs::create_dir_all(data.join("run")).unwrap();
    let pkg = data.join("plugins").join(ID).join("1.0.0_test");
    std::fs::create_dir_all(pkg.join("i18n")).unwrap();
    std::fs::write(
        pkg.join("manifest.toml"),
        format!(
            "schema = 1\nid = \"{ID}\"\nversion = \"1.0.0\"\nplugin_api = \"1.0\"\nmaktaba = \">=0.1.0\"\nlicence = \"MIT\"\n\n[strings]\nname = \"n\"\nsummary = \"s\"\n\n[capabilities]\noptional = [\"tags.write\", \"jobs.enqueue\"]\n\n[[stage]]\nname = \"work\"\nsubject = \"video\"\nexport = \"run-work\"\nversion = 1\n"
        ),
    )
    .unwrap();
    std::fs::write(pkg.join("i18n/en.json"), r#"{"n":"Hostile","s":"Misbehaves."}"#).unwrap();
    std::fs::write(pkg.join("i18n/ar.json"), r#"{"n":"مؤذية","s":"تسيء التصرف."}"#).unwrap();
    std::fs::write(pkg.join("plugin.wasm"), guest("hostile")).unwrap();
    Home { _dir: dir, data }
}

/// A plugin API on the unix socket that answers `{}` to every call and counts them.
fn fake_api(path: &Path) -> std::sync::Arc<std::sync::atomic::AtomicUsize> {
    let n = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let l = uds::bind(path).unwrap();
    let count = n.clone();
    std::thread::spawn(move || {
        for c in l.incoming().flatten() {
            count.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
            let mut r = BufReader::new(c.try_clone().unwrap());
            let mut len = 0usize;
            loop {
                let mut line = String::new();
                if r.read_line(&mut line).unwrap_or(0) == 0 || line == "\r\n" {
                    break;
                }
                if let Some(v) = line.to_ascii_lowercase().strip_prefix("content-length:") {
                    len = v.trim().parse().unwrap_or(0);
                }
            }
            let mut body = vec![0; len];
            let _ = r.read_exact(&mut body);
            let mut c = c;
            let _ = c.write_all(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}");
        }
    });
    n
}

fn start(data: &Path, env: &[(&str, &str)]) -> Child {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_maktaba-plugind"));
    cmd.arg("--data").arg(data).arg("--pool").arg("2").stdout(Stdio::piped()).stderr(Stdio::piped());
    for (k, v) in env {
        cmd.env(k, v);
    }
    cmd.spawn().unwrap()
}

fn send(sock: &Path, req: &Request) -> Vec<Response> {
    let mut c = uds::connect(sock).unwrap();
    protocol::write_frame(&mut c, req).unwrap();
    let mut out = Vec::new();
    while let Ok(r) = protocol::read_frame::<Response>(&mut c) {
        let last = !matches!(r, Response::Progress { .. });
        out.push(r);
        if last {
            break;
        }
    }
    out
}

#[test]
#[cfg_attr(windows, ignore = "the host runs only inside an AppContainer: covered by plugins-sandbox")]
fn sandboxed_host_runs_a_job_through_the_api_socket() {
    let h = home();
    let calls = fake_api(&h.data.join("run/plugin-api.sock"));
    let mut child = start(&h.data, &[]);
    let sock = h.data.join("run/plugins/plugind.sock");
    let t = Instant::now();
    while uds::connect(&sock).is_err() {
        if let Ok(Some(st)) = child.try_wait() {
            let mut e = String::new();
            child.stderr.take().unwrap().read_to_string(&mut e).unwrap();
            panic!("the host exited {st}: {e}");
        }
        assert!(t.elapsed() < Duration::from_secs(20), "no dispatch socket");
        std::thread::sleep(Duration::from_millis(50));
    }
    let Response::Health(health) = &send(&sock, &Request::Health)[0] else { panic!() };
    assert!(health.ok);
    assert_ne!(health.sandbox, "none");
    #[cfg(target_os = "macos")]
    assert_eq!(health.sandbox, "full");

    let run = Run {
        invocation_id: "i1".into(),
        plugin_id: ID.into(),
        version: "1.0.0".into(),
        package_path: h.data.join("plugins").join(ID).join("1.0.0_test").to_string_lossy().into_owned(),
        export: "run-work".into(),
        job: Job {
            id: "j1".into(),
            stage: "request".into(),
            subject: Subject { kind: "video".into(), id: "v1".into() },
            ..Default::default()
        },
        grants: vec!["jobs.enqueue".into()],
        token: "t".into(),
        limits: Limits::default(),
        ..Default::default()
    };
    let frames = send(&sock, &Request::Run(Box::new(run)));
    let Some(Response::Result(r)) = frames.last() else { panic!("{frames:?}") };
    assert_eq!(r.outcome, Outcome::Ok, "{r:?}");
    assert_eq!(r.requests.len(), 1);
    assert!(calls.load(std::sync::atomic::Ordering::SeqCst) >= 1, "the API socket was reached");
    // the compile cache was written inside the sandbox
    assert!(std::fs::read_dir(h.data.join("plugins/cache")).unwrap().count() >= 1);
    child.kill().unwrap();
    let _ = child.wait();
}

/// T05: a required step that fails ends the host with 78 and names the step.
#[test]
fn a_failed_sandbox_step_exits_78() {
    let steps: &[&str] = if cfg!(target_os = "linux") {
        &["no_new_privs", "rlimit", "seccomp"]
    } else if cfg!(target_os = "macos") {
        &["seatbelt"]
    } else {
        &["appcontainer"]
    };
    for step in steps {
        let h = home();
        let out = start(&h.data, &[("MAKTABA_PLUGIND_FAIL_STEP", step)]).wait_with_output().unwrap();
        assert_eq!(out.status.code(), Some(78), "{step}");
        let err = String::from_utf8_lossy(&out.stderr);
        assert!(err.contains(&format!("sandbox_unavailable:{step}")), "{step}: {err}");
        // the supervisor reads the step from the run folder (its status params)
        let file = std::fs::read_to_string(h.data.join("run/plugins/sandbox_unavailable")).unwrap();
        assert_eq!(file, *step);
    }
}

/// 54.07 §4 step 4: --preload checks a package in the sandbox and answers on stdout,
/// binding no socket; a package that names another plugin is refused.
#[test]
#[cfg_attr(windows, ignore = "the host runs only inside an AppContainer: covered by plugins-sandbox")]
fn preload_checks_a_package_and_exits() {
    let h = home();
    let pkg = h.data.join("plugins").join(ID).join("1.0.0_test");
    let run = |id: &str| {
        Command::new(env!("CARGO_BIN_EXE_maktaba-plugind"))
            .arg("--data")
            .arg(&h.data)
            .arg("--preload")
            .arg(&pkg)
            .args(["--plugin-id", id, "--plugin-version", "1.0.0"])
            .output()
            .unwrap()
    };
    let ok = run(ID);
    let line = String::from_utf8_lossy(&ok.stdout);
    assert_eq!(ok.status.code(), Some(0), "{line} {}", String::from_utf8_lossy(&ok.stderr));
    assert!(line.contains(r#""ok":true"#), "{line}");
    assert!(!h.data.join("run/plugins/plugind.sock").exists(), "a preload bound the socket");
    let bad = run("dev.example.other");
    let line = String::from_utf8_lossy(&bad.stdout);
    assert_eq!(bad.status.code(), Some(1), "{line}");
    assert!(line.contains(r#""ok":false"#) && line.contains("error_code"), "{line}");
}

/// On Windows outside an AppContainer the host refuses to run at all.
#[test]
#[cfg(windows)]
fn windows_outside_an_app_container_exits_78() {
    let h = home();
    let out = start(&h.data, &[]).wait_with_output().unwrap();
    assert_eq!(out.status.code(), Some(78));
    assert!(String::from_utf8_lossy(&out.stderr).contains("sandbox_unavailable:appcontainer"));
}

/// Linux: Landlock or namespaces missing make it Degraded, never unsandboxed.
#[test]
#[cfg(target_os = "linux")]
fn linux_partial_sandbox_still_runs() {
    let h = home();
    let _calls = fake_api(&h.data.join("run/plugin-api.sock"));
    let mut child = start(&h.data, &[("MAKTABA_PLUGIND_FAIL_STEP", "landlock")]);
    let sock = h.data.join("run/plugins/plugind.sock");
    let t = Instant::now();
    while uds::connect(&sock).is_err() {
        assert!(t.elapsed() < Duration::from_secs(20), "no dispatch socket");
        std::thread::sleep(Duration::from_millis(50));
    }
    let Response::Health(health) = &send(&sock, &Request::Health)[0] else { panic!() };
    assert_eq!(health.sandbox, "partial");
    assert!(health.sandbox_missing.contains(&"landlock".to_string()));
    child.kill().unwrap();
    let _ = child.wait();
}
