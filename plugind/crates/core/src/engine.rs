//! The engine: one Wasmtime engine and linker per host, a compile cache, and one fresh
//! store per job with fuel, an epoch deadline and memory caps (story 54.02 §4–§6).

use std::collections::{BTreeSet, HashMap};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

use maktaba_plugin_manifest as manifest;
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use wasmtime::component::{Component, HasData, HasSelf, Linker, ResourceTable};
use wasmtime::{Config, Engine, OptLevel, Store, Trap, UpdateDeadline, WasmBacktraceDetails};
use wasmtime_wasi::p2::pipe::MemoryOutputPipe;
use wasmtime_wasi::WasiCtxBuilder;

use crate::api::PluginApi;
use crate::audit;
use crate::host::{granted_bases, Ended, JobState, Limiter, Plugin, PluginPre, Progress};
use crate::limits::{self, Effective};
use crate::logging::{Logs, Origin};
use crate::protocol::{b64, unb64, Health, Outcome, Run, RunResult};

/// The pinned Wasmtime (plugind/Cargo.toml); a cache file built by another one is never
/// loaded (54.02 §6).
pub const WASMTIME_VERSION: &str = "50.0.1";
/// The epoch tick: a deadline ends a job within one tick plus unwind (N7: ≤ 200 ms).
pub const TICK: Duration = Duration::from_millis(100);
const MAX_SUMMARY: usize = 64 << 10;
const MAX_STDIO: usize = 64 << 10;

pub const CODE_BAD_PATH: &str = "plugin_bad_path";
pub const CODE_BAD_MANIFEST: &str = "plugin_bad_manifest";
pub const CODE_LINK: &str = "plugin_link";
pub const CODE_COMPILE: &str = "plugin_compile";
pub const CODE_NOT_LOADED: &str = "plugin_not_loaded";

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LoadError {
    pub code: &'static str,
    pub detail: String,
}

impl std::fmt::Display for LoadError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.code, self.detail)
    }
}

fn load_err(code: &'static str, detail: impl ToString) -> LoadError {
    LoadError { code, detail: detail.to_string() }
}

/// A counting semaphore (the host's pool and each plugin's concurrency).
pub struct Semaphore {
    n: Mutex<usize>,
    cv: Condvar,
}

impl Semaphore {
    pub fn new(n: usize) -> Self {
        Self { n: Mutex::new(n.max(1)), cv: Condvar::new() }
    }
    pub fn acquire(&self) -> Permit<'_> {
        let mut n = self.n.lock().unwrap();
        while *n == 0 {
            n = self.cv.wait(n).unwrap();
        }
        *n -= 1;
        Permit(self)
    }
}

pub struct Permit<'a>(&'a Semaphore);

impl Drop for Permit<'_> {
    fn drop(&mut self) {
        *self.0.n.lock().unwrap() += 1;
        self.0.cv.notify_one();
    }
}

pub struct HostConfig {
    /// `<data>/plugins`: every package path must be inside it
    pub plugins_dir: PathBuf,
    /// `<data>/plugins/cache`: compiled components
    pub cache_dir: PathBuf,
    /// the `plugins` pool size: jobs at once on this host
    pub pool_size: usize,
    pub api: Arc<dyn PluginApi>,
    pub logs: Arc<Logs>,
    /// tests only: skip the import audit, to prove the linker refuses alone (54.02 T03).
    /// The binary has no flag for it.
    pub skip_audit: bool,
    /// what health reports of the OS sandbox
    pub sandbox: String,
    pub sandbox_missing: Vec<String>,
}

struct Loaded {
    pre: PluginPre<JobState>,
    cap: Effective,
    sem: Semaphore,
}

pub struct Host {
    engine: Engine,
    linker: Linker<JobState>,
    cfg: HostConfig,
    plugins_dir: PathBuf,
    loaded: Mutex<HashMap<(String, String), Arc<Loaded>>>,
    pool: Semaphore,
    running: AtomicUsize,
    cancels: Mutex<HashMap<String, Arc<AtomicBool>>>,
    stop: Arc<AtomicBool>,
}

struct HasIo;
impl HasData for HasIo {
    type Data<'a> = &'a mut ResourceTable;
}

/// The engine's settings (54.02 §5).
pub fn engine_config() -> Config {
    let mut c = Config::new();
    c.consume_fuel(true);
    c.epoch_interruption(true);
    c.wasm_component_model(true);
    c.wasm_backtrace_details(WasmBacktraceDetails::Enable);
    c.cranelift_opt_level(OptLevel::Speed);
    c.memory_init_cow(true);
    c
}

/// The linker: the WASI subset of README §5.2 and every `maktaba:plugin` interface, nothing
/// else. No sockets, http, filesystem, stdin, terminals or processes are ever added.
pub fn build_linker(engine: &Engine) -> wasmtime::Result<Linker<JobState>> {
    use wasmtime_wasi::cli::{WasiCli, WasiCliView as _};
    use wasmtime_wasi::clocks::{WasiClocks, WasiClocksView as _};
    use wasmtime_wasi::p2::bindings::{cli, clocks, random, sync};
    use wasmtime_wasi::random::WasiRandom;

    let mut l = Linker::<JobState>::new(engine);
    l.allow_shadowing(false);
    clocks::wall_clock::add_to_linker::<JobState, WasiClocks>(&mut l, JobState::clocks)?;
    clocks::monotonic_clock::add_to_linker::<JobState, WasiClocks>(&mut l, JobState::clocks)?;
    random::random::add_to_linker::<JobState, WasiRandom>(&mut l, |t| t.wasi.random())?;
    random::insecure::add_to_linker::<JobState, WasiRandom>(&mut l, |t| t.wasi.random())?;
    random::insecure_seed::add_to_linker::<JobState, WasiRandom>(&mut l, |t| t.wasi.random())?;
    cli::exit::add_to_linker::<JobState, WasiCli>(&mut l, JobState::cli)?;
    cli::environment::add_to_linker::<JobState, WasiCli>(&mut l, JobState::cli)?;
    cli::stdout::add_to_linker::<JobState, WasiCli>(&mut l, JobState::cli)?;
    cli::stderr::add_to_linker::<JobState, WasiCli>(&mut l, JobState::cli)?;
    wasmtime_wasi_io::bindings::wasi::io::error::add_to_linker::<JobState, HasIo>(&mut l, |t| &mut t.table)?;
    sync::io::poll::add_to_linker::<JobState, HasIo>(&mut l, |t| &mut t.table)?;
    sync::io::streams::add_to_linker::<JobState, HasIo>(&mut l, |t| &mut t.table)?;
    Plugin::add_to_linker::<JobState, HasSelf<JobState>>(&mut l, |s| s)?;
    Ok(l)
}

fn sha256_hex(b: &[u8]) -> String {
    hex::encode(Sha256::digest(b))
}

impl Host {
    pub fn new(cfg: HostConfig) -> wasmtime::Result<Arc<Self>> {
        let engine = Engine::new(&engine_config()).map_err(|e| e.context("the engine"))?;
        let linker = build_linker(&engine).map_err(|e| e.context("the linker"))?;
        std::fs::create_dir_all(&cfg.cache_dir)
            .map_err(|e| wasmtime::Error::msg(format!("{}: {e}", cfg.cache_dir.display())))?;
        // resolved before the sandbox when it can be (a sandboxed process may not stat the
        // folders above its own)
        let plugins_dir = cfg.plugins_dir.canonicalize().unwrap_or_else(|_| cfg.plugins_dir.clone());
        let stop = Arc::new(AtomicBool::new(false));
        let host = Arc::new(Self {
            pool: Semaphore::new(cfg.pool_size),
            engine,
            linker,
            plugins_dir,
            cfg,
            loaded: Mutex::new(HashMap::new()),
            running: AtomicUsize::new(0),
            cancels: Mutex::new(HashMap::new()),
            stop: stop.clone(),
        });
        let engine = host.engine.clone();
        std::thread::Builder::new().name("epoch".into()).spawn(move || {
            while !stop.load(Ordering::Relaxed) {
                std::thread::sleep(TICK);
                engine.increment_epoch();
            }
        })?;
        Ok(host)
    }

    pub fn engine(&self) -> &Engine {
        &self.engine
    }

    pub fn logs(&self) -> &Arc<Logs> {
        &self.cfg.logs
    }

    /// The package folder, which must be inside the plugins folder. Checked by its words,
    /// not resolved: an absolute path with no "." or ".." under the plugins folder as given
    /// or as resolved (the packages are written by the supervisor's unpacker, which makes
    /// no links, and the OS sandbox keeps the host inside the plugins folder anyway).
    fn package_dir(&self, package_path: &str) -> Result<PathBuf, LoadError> {
        use std::path::Component;
        let p = Path::new(package_path);
        let clean = p.is_absolute()
            && p.components().all(|c| matches!(c, Component::RootDir | Component::Prefix(_) | Component::Normal(_)));
        let inside = |root: &Path| p.starts_with(root) && p != root;
        if !clean || !(inside(&self.plugins_dir) || inside(&self.cfg.plugins_dir)) {
            return Err(load_err(CODE_BAD_PATH, "the package is not inside the plugins folder"));
        }
        if !p.is_dir() {
            return Err(load_err(CODE_BAD_PATH, "the package folder does not exist"));
        }
        Ok(p.to_path_buf())
    }

    /// Compiles (or loads from the cache) a component's bytes.
    pub fn compile(&self, wasm: &[u8]) -> Result<Component, LoadError> {
        if !audit::is_component(wasm) {
            return Err(load_err(audit::CODE_NOT_COMPONENT, "plugin.wasm is not a WebAssembly component"));
        }
        let cache = self.cfg.cache_dir.join(format!("{}-{}.cwasm", sha256_hex(wasm), WASMTIME_VERSION));
        if cache.is_file() {
            // SAFETY: the cache folder is written only by this host, atomically, and the
            // name binds the component's sha256 and this Wasmtime version; Wasmtime
            // checks the engine's compatibility again and a refusal recompiles.
            if let Ok(c) = unsafe { Component::deserialize_file(&self.engine, &cache) } {
                return Ok(c);
            }
        }
        let bytes = self.engine.precompile_component(wasm).map_err(|e| load_err(CODE_COMPILE, e))?;
        let tmp = cache.with_extension(format!("cwasm.tmp-{}", std::process::id()));
        if std::fs::write(&tmp, &bytes).and_then(|_| std::fs::rename(&tmp, &cache)).is_err() {
            let _ = std::fs::remove_file(&tmp);
        }
        // SAFETY: the bytes were produced by this engine a moment ago.
        unsafe { Component::deserialize(&self.engine, &bytes) }.map_err(|e| load_err(CODE_COMPILE, e))
    }

    /// Validates, audits, compiles and pre-instantiates a package (`Preload`, 54.02 §6).
    pub fn preload(&self, plugin_id: &str, version: &str, package_path: &str) -> Result<(), LoadError> {
        let dir = self.package_dir(package_path)?;
        let src = std::fs::read(dir.join("manifest.toml")).map_err(|e| load_err(CODE_BAD_MANIFEST, e))?;
        let files = |p: &str| -> Option<Vec<u8>> {
            if p.contains("..") || p.starts_with('/') {
                return None;
            }
            std::fs::read(dir.join(p)).ok()
        };
        let m = manifest::validate(&src, Some(&files))
            .map_err(|e| load_err(CODE_BAD_MANIFEST, e.iter().map(|x| x.to_string()).collect::<Vec<_>>().join("; ")))?;
        if m["id"] != plugin_id || m["version"] != version {
            return Err(load_err(CODE_BAD_MANIFEST, "the manifest names another plugin or version"));
        }
        let wasm = std::fs::read(dir.join("plugin.wasm")).map_err(|e| load_err(audit::CODE_NOT_COMPONENT, e))?;
        let component = self.compile(&wasm)?;
        if !self.cfg.skip_audit {
            let (imports, exports) = audit::names(&self.engine, &component);
            audit::audit(&imports, &exports, &declared(&m)).map_err(|e| load_err(e.code, e.detail))?;
        }
        let pre = self.linker.instantiate_pre(&component).map_err(|e| load_err(CODE_LINK, format!("{e:#}")))?;
        let pre = PluginPre::new(pre).map_err(|e| load_err(CODE_LINK, format!("{e:#}")))?;
        let cap = limits::from_manifest(&m);
        let loaded = Arc::new(Loaded { pre, cap, sem: Semaphore::new(cap.concurrency) });
        self.loaded.lock().unwrap().insert((plugin_id.into(), version.into()), loaded);
        Ok(())
    }

    pub fn unload(&self, plugin_id: &str, version: &str) {
        self.loaded.lock().unwrap().remove(&(plugin_id.to_string(), version.to_string()));
    }

    pub fn cancel(&self, invocation_id: &str) -> bool {
        match self.cancels.lock().unwrap().get(invocation_id) {
            Some(f) => {
                f.store(true, Ordering::Relaxed);
                true
            }
            None => false,
        }
    }

    pub fn health(&self) -> Health {
        Health {
            ok: true,
            ready: true,
            wasmtime: WASMTIME_VERSION.into(),
            plugin_api: format!("{}.{}", audit::PLUGIN_API_MAJOR, audit::PLUGIN_API_MINOR),
            sandbox: self.cfg.sandbox.clone(),
            sandbox_missing: self.cfg.sandbox_missing.clone(),
            loaded: self.loaded.lock().unwrap().len(),
            running: self.running.load(Ordering::Relaxed),
        }
    }

    /// Runs one job (54.02 §2, §5). Progress and checkpoints go to `progress` as they come.
    pub fn run(&self, req: &Run, progress: Progress) -> RunResult {
        let started = Instant::now();
        let mut res = RunResult {
            invocation_id: req.invocation_id.clone(),
            outcome: Outcome::Refused,
            output_hash: String::new(),
            summary: String::new(),
            requests: vec![],
            log_dropped: 0,
            fuel_used: 0,
            peak_memory: 0,
            wall_ms: 0,
            error_code: String::new(),
            detail: String::new(),
            checkpoint: None,
            limits_clamped: false,
        };
        let key = (req.plugin_id.clone(), req.version.clone());
        let mut loaded = self.loaded.lock().unwrap().get(&key).cloned();
        if loaded.is_none() {
            if let Err(e) = self.preload(&req.plugin_id, &req.version, &req.package_path) {
                res.error_code = e.code.into();
                res.detail = e.detail;
                return res;
            }
            loaded = self.loaded.lock().unwrap().get(&key).cloned();
        }
        let Some(loaded) = loaded else {
            res.error_code = CODE_NOT_LOADED.into();
            return res;
        };
        let (lim, clamped) = limits::clamp(&loaded.cap, &req.limits);
        res.limits_clamped = clamped;
        if clamped {
            self.cfg.logs.host(
                "warn",
                "limits_clamped",
                serde_json::json!({"plugin": req.plugin_id, "job_id": req.job.id}),
            );
        }
        let _pool = self.pool.acquire();
        let _mine = loaded.sem.acquire();
        let cancel = Arc::new(AtomicBool::new(false));
        self.cancels.lock().unwrap().insert(req.invocation_id.clone(), cancel.clone());
        self.running.fetch_add(1, Ordering::Relaxed);
        let origin = Origin {
            plugin_id: req.plugin_id.clone(),
            version: req.version.clone(),
            job_id: req.job.id.clone(),
            stage: req.job.stage.clone(),
        };
        let stdout = MemoryOutputPipe::new(MAX_STDIO);
        let stderr = MemoryOutputPipe::new(MAX_STDIO);
        let mut b = WasiCtxBuilder::new();
        b.stdout(stdout.clone()).stderr(stderr.clone()).allow_tcp(false).allow_udp(false).allow_ip_name_lookup(false);
        let state = JobState {
            wasi: b.build(),
            table: ResourceTable::new(),
            limiter: Limiter { memory_cap: lim.memory_bytes, peak_memory: 0, hit: false },
            token: req.token.clone(),
            api: self.cfg.api.clone(),
            subject: req.job.subject.clone(),
            payload: req.job.payload.clone(),
            settings: req.settings.clone(),
            grants: req.grants.clone(),
            granted: granted_bases(&req.grants),
            last_checkpoint: req.job.checkpoint.as_deref().and_then(unb64),
            checkpoint: None,
            scratch: HashMap::new(),
            scratch_used: 0,
            scratch_cap: lim.scratch_bytes,
            progress,
            requests: vec![],
            refusal: None,
            local_refusals: 0,
            logs: self.cfg.logs.clone(),
            origin: origin.clone(),
            deadline: started + Duration::from_millis(lim.timeout_ms),
            cancel: cancel.clone(),
            ended: None,
        };
        let mut store = Store::new(&self.engine, state);
        store.limiter(|s| &mut s.limiter);
        let _ = store.set_fuel(lim.fuel);
        store.set_epoch_deadline(1);
        store.epoch_deadline_callback(|mut ctx| {
            let s = ctx.data_mut();
            if s.cancel.load(Ordering::Relaxed) {
                s.ended = Some(Ended::Cancelled);
                return Err(wasmtime::Error::msg("cancelled"));
            }
            if Instant::now() >= s.deadline {
                s.ended = Some(Ended::Timeout);
                return Err(wasmtime::Error::msg("plugin_timeout"));
            }
            Ok(UpdateDeadline::Continue(1))
        });
        // the start function runs under the same fuel and deadline as the job
        let outcome = loaded.pre.instantiate(&mut store).and_then(|p| p.call_run(&mut store, &req.job.stage));
        let fuel_left = store.get_fuel().unwrap_or(0);
        let state = store.into_data();
        res.fuel_used = lim.fuel.saturating_sub(fuel_left);
        res.peak_memory = state.limiter.peak_memory;
        res.requests = state.requests.clone();
        match outcome {
            Ok(Ok(out)) => {
                res.outcome = Outcome::Ok;
                res.output_hash = out.output_hash;
                res.summary = truncate(out.summary, MAX_SUMMARY);
            }
            Ok(Err(e)) => match state.refusal {
                Some(code) => {
                    res.outcome = Outcome::Refused;
                    res.error_code = code.into();
                    res.detail = truncate(format!("{}: {}", e.code, e.message), 4096);
                }
                None => {
                    res.outcome = Outcome::Failed;
                    res.error_code = "plugin_failed".into();
                    res.detail = truncate(format!("{}: {}", e.code, e.message), 4096);
                }
            },
            Err(err) => {
                let (o, code) = match (state.ended, state.limiter.hit, err.downcast_ref::<Trap>()) {
                    (Some(Ended::Cancelled), _, _) => (Outcome::Refused, "cancelled"),
                    (Some(Ended::Timeout), _, _) => (Outcome::Timeout, "plugin_timeout"),
                    (_, true, _) => (Outcome::Memory, "plugin_memory"),
                    (_, _, Some(Trap::OutOfFuel)) => (Outcome::Timeout, "plugin_timeout"),
                    _ => (Outcome::Trap, "plugin_trap"),
                };
                res.outcome = o;
                res.error_code = code.into();
                res.detail = truncate(format!("{err:#}"), 4096);
            }
        }
        if res.outcome != Outcome::Ok {
            res.checkpoint = state.checkpoint.as_deref().map(b64);
            res.requests.clear();
        }
        for (pipe, level) in [(stdout, "info"), (stderr, "warn")] {
            let text = pipe.contents();
            for line in String::from_utf8_lossy(&text).lines().filter(|l| !l.trim().is_empty()) {
                let fields = serde_json::json!({"text": line}).to_string();
                self.cfg.logs.plugin(&origin, level, if level == "info" { "stdout" } else { "stderr" }, &fields);
            }
        }
        self.cfg.logs.flush_dropped(&origin);
        res.log_dropped = self.cfg.logs.dropped(&req.plugin_id);
        res.wall_ms = started.elapsed().as_millis() as u64;
        self.cancels.lock().unwrap().remove(&req.invocation_id);
        self.running.fetch_sub(1, Ordering::Relaxed);
        res
    }
}

impl Drop for Host {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Relaxed);
    }
}

/// The capabilities a manifest declares (required ∪ optional).
pub fn declared(m: &Map<String, Value>) -> Vec<String> {
    let mut out = BTreeSet::new();
    for list in ["required", "optional"] {
        if let Some(a) = m["capabilities"][list].as_array() {
            out.extend(a.iter().filter_map(Value::as_str).map(str::to_string));
        }
    }
    out.into_iter().collect()
}

fn truncate(mut s: String, max: usize) -> String {
    if s.len() > max {
        let mut cut = max;
        while !s.is_char_boundary(cut) {
            cut -= 1;
        }
        s.truncate(cut);
    }
    s
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A plugin built for plugin API 1.0 links on this 1.1 host (story 54.21: a minor only
    /// adds; the linker matches a 1.0 import against the 1.1 interfaces by semver).
    #[test]
    fn a_1_0_import_links_on_the_1_1_host() {
        let engine = Engine::new(&engine_config()).unwrap();
        let linker = build_linker(&engine).unwrap();
        for version in ["1.0.0", "1.1.0"] {
            let wat = format!(
                r#"(component (import "maktaba:plugin/job@{version}" (instance (export "progress" (func (param "pct" u8))))))"#
            );
            let c = Component::new(&engine, wat).unwrap();
            linker.instantiate_pre(&c).unwrap_or_else(|e| panic!("{version}: {e}"));
        }
        // a newer minor links by semver too: the import audit refuses it first
        // (plugin_api_newer, audit::tests::newer_api_and_missing_run)
    }
}
