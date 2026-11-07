//! The test harness (story 54.12 §3): the same host code as maktaba-plugind (plugind-core's
//! engine, limits, import audit and forwarders) with a mock plugin API in-process. The mock
//! answers each `interface/function` from a fixtures file, refuses a call no capability of
//! the manifest unlocks (the host refuses an ungranted one before it leaves), and records
//! every call; the run's result, the calls and the plugin's log lines are the output.

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use plugind_core::api::{Answer, ApiError, PluginApi};
use plugind_core::logging::{Logs, MemorySink};
use plugind_core::protocol::{Job, Limits, Run, Subject};
use plugind_core::{Host, HostConfig};
use serde_json::{json, Map, Value};

#[derive(Default)]
pub struct Options {
    pub stage: String,
    /// kind:id ("catalog" when empty)
    pub subject: String,
    pub fixtures: Option<PathBuf>,
    /// JSON text of the plugin's settings
    pub settings: String,
    /// JSON text of the job's payload ("{}" when empty)
    pub payload: String,
    /// the stages an emitted event goes to (a chain, `mkp test --with`)
    pub subscribers: Vec<String>,
}

const TOKEN: &str = "mkp-harness";

/// The mock plugin API: fixtures by `interface/function`, every call recorded.
struct Mock {
    fixtures: BTreeMap<String, Value>,
    calls: Mutex<Vec<Value>>,
    subscribers: Vec<String>,
}

impl PluginApi for Mock {
    fn call(&self, token: &str, interface: &str, function: &str, body: &Value) -> Result<Answer, ApiError> {
        if token != TOKEN {
            return Err(ApiError::Ended);
        }
        let key = format!("{interface}/{function}");
        self.calls.lock().unwrap().push(json!({"call": key, "body": body}));
        match self.fixtures.get(&key) {
            Some(Value::Object(o)) if o.contains_key("refuse") => {
                Err(ApiError::from_code(o["refuse"].as_str().unwrap_or("plugin_invalid")))
            }
            Some(v) => Ok(Answer::Json(v.clone())),
            // an emitted event goes to the chain's subscribers (54.40)
            None if key == "events/emit" => Ok(Answer::Json(json!({"stages": self.subscribers}))),
            None => Ok(Answer::Json(json!({}))),
        }
    }
}

/// A host on a scratch folder, the package copied under it (the host takes packages only
/// inside its plugins folder).
struct Harness {
    _dir: tempfile_dir::Dir,
    host: Arc<Host>,
    package: String,
    sink: Arc<MemorySink>,
    mock: Arc<Mock>,
}

mod tempfile_dir {
    use std::path::{Path, PathBuf};

    /// A folder removed when dropped.
    pub struct Dir(pub PathBuf);
    impl Dir {
        pub fn new() -> std::io::Result<Self> {
            let p = std::env::temp_dir().join(format!("mkp-{}-{}", std::process::id(), nanos()));
            std::fs::create_dir_all(&p)?;
            Ok(Self(p))
        }
        pub fn path(&self) -> &Path {
            &self.0
        }
    }
    impl Drop for Dir {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }
    fn nanos() -> u128 {
        std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_nanos()).unwrap_or(0)
    }
}

fn copy_dir(from: &Path, to: &Path) -> Result<(), String> {
    for (rel, body) in crate::pack::files_of(from)? {
        let p = to.join(&rel);
        std::fs::create_dir_all(p.parent().unwrap()).map_err(|e| e.to_string())?;
        std::fs::write(&p, body).map_err(|e| e.to_string())?;
    }
    Ok(())
}

fn harness(
    dir: &Path,
    m: &Map<String, Value>,
    fixtures: BTreeMap<String, Value>,
    subscribers: Vec<String>,
) -> Result<Harness, String> {
    let tmp = tempfile_dir::Dir::new().map_err(|e| e.to_string())?;
    let root = tmp.path().canonicalize().map_err(|e| e.to_string())?.join("plugins");
    let id = m["id"].as_str().unwrap_or_default();
    let pkg = root.join(id).join("harness");
    copy_dir(dir, &pkg)?;
    let sink = Arc::new(MemorySink::default());
    let mock = Arc::new(Mock { fixtures, calls: Mutex::new(Vec::new()), subscribers });
    let host = Host::new(HostConfig {
        plugins_dir: root.clone(),
        cache_dir: root.join(".cache"),
        pool_size: 1,
        api: mock.clone(),
        logs: Arc::new(Logs::new(sink.clone())),
        skip_audit: false,
        sandbox: "none".into(),
        sandbox_missing: vec!["harness".into()],
    })
    .map_err(|e| format!("{e:#}"))?;
    Ok(Harness { _dir: tmp, host, package: pkg.to_string_lossy().into_owned(), sink, mock })
}

/// The import audit and compile, as the home's preload does.
pub fn preload(dir: &Path, m: &Map<String, Value>) -> Result<String, String> {
    let h = harness(dir, m, BTreeMap::new(), Vec::new())?;
    let id = m["id"].as_str().unwrap_or_default();
    let version = m["version"].as_str().unwrap_or_default();
    h.host.preload(id, version, &h.package).map_err(|e| format!("{}: {}", e.code, e.detail))?;
    Ok(format!("{id} {version}: the manifest is valid and the component passes the import audit"))
}

/// Runs one stage; answers {result, calls, logs}.
pub fn run(dir: &Path, m: &Map<String, Value>, o: &Options) -> Result<Value, String> {
    let fixtures: BTreeMap<String, Value> = match &o.fixtures {
        Some(p) => {
            let raw = std::fs::read(p).map_err(|e| format!("{}: {e}", p.display()))?;
            serde_json::from_slice(&raw).map_err(|e| format!("{}: {e}", p.display()))?
        }
        None => BTreeMap::new(),
    };
    let stage = m["stage"]
        .as_array()
        .and_then(|l| l.iter().find(|s| s["name"] == o.stage.as_str()))
        .ok_or_else(|| format!("the manifest has no stage {:?}", o.stage))?;
    let (kind, id) = match o.subject.split_once(':') {
        Some((k, i)) => (k.to_string(), i.to_string()),
        None if o.subject.is_empty() => {
            (stage["subject"].as_str().unwrap_or("catalog").to_string(), "fixture".to_string())
        }
        None => return Err("--subject is kind:id".into()),
    };
    // every capability the manifest asks for is granted in the harness
    let caps = &m["capabilities"];
    let grants: Vec<String> = ["required", "optional"]
        .iter()
        .flat_map(|k| caps[*k].as_array().cloned().unwrap_or_default())
        .filter_map(|v| v.as_str().map(String::from))
        .collect();
    let h = harness(dir, m, fixtures, o.subscribers.clone())?;
    let req = Run {
        invocation_id: "inv-harness".into(),
        plugin_id: m["id"].as_str().unwrap_or_default().into(),
        version: m["version"].as_str().unwrap_or_default().into(),
        package_path: h.package.clone(),
        export: stage["export"].as_str().unwrap_or_default().into(),
        job: Job {
            id: "job-harness".into(),
            stage: o.stage.clone(),
            subject: Subject { kind, id },
            payload: if o.payload.is_empty() { "{}".into() } else { o.payload.clone() },
            attempt: 1,
            checkpoint: None,
        },
        settings: if o.settings.is_empty() { "{}".into() } else { o.settings.clone() },
        grants,
        token: TOKEN.into(),
        limits: Limits::default(),
    };
    let result = h.host.run(&req, Box::new(|_, _| {}));
    let logs: Vec<Value> = h.sink.0.lock().unwrap().iter().filter_map(|l| serde_json::from_str(l).ok()).collect();
    Ok(json!({
        "result": serde_json::to_value(&result).unwrap(),
        "calls": *h.mock.calls.lock().unwrap(),
        "logs": logs,
    }))
}
