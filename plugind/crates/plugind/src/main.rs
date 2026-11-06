//! maktaba-plugind: the plugin host (Epic 54, stories 54.02 and 54.03).
//!
//! Started by the supervisor as role `plugins` (one per home) only while a plugin is
//! enabled. It binds its dispatch socket, puts itself in the OS sandbox, and then runs
//! plugin jobs as WebAssembly components for the pipeline's dispatcher. It opens no
//! socket but the dispatch listener and connections to the plugin API's unix socket.
//!
//!   maktaba-plugind --data <dir> [--plugins-dir <dir>] [--cache-dir <dir>] [--run-dir <dir>]
//!                   [--api-socket <path>] [--dispatch-socket <path>] [--pool <n>]
//!
//! Exit 78 with `sandbox_unavailable:<step>` on stderr: the sandbox could not be applied
//! and no plugin ran. A test build (`--features escape-probes`) also takes
//! `--escape-probes` and `--no-sandbox` (the positive control).

#[cfg(feature = "escape-probes")]
mod probes;
mod sandbox;

use std::path::PathBuf;
use std::sync::Arc;

use plugind_core::api::UnixApi;
use plugind_core::logging::{Logs, StdoutSink};
use plugind_core::{Host, HostConfig};

struct Flags {
    data: PathBuf,
    plugins_dir: Option<PathBuf>,
    cache_dir: Option<PathBuf>,
    run_dir: Option<PathBuf>,
    api_socket: Option<PathBuf>,
    dispatch_socket: Option<PathBuf>,
    pool: usize,
    probes: bool,
    no_sandbox: bool,
}

fn usage(msg: &str) -> ! {
    eprintln!("maktaba-plugind: {msg}");
    eprintln!("usage: maktaba-plugind --data <dir> [--plugins-dir d] [--cache-dir d] [--run-dir d] [--api-socket p] [--dispatch-socket p] [--pool n]");
    std::process::exit(2)
}

fn flags() -> Flags {
    let mut f = Flags {
        data: PathBuf::new(),
        plugins_dir: None,
        cache_dir: None,
        run_dir: None,
        api_socket: None,
        dispatch_socket: None,
        pool: 1,
        probes: false,
        no_sandbox: false,
    };
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        let mut val = || args.next().unwrap_or_else(|| usage(&format!("{a} needs a value")));
        match a.as_str() {
            "--data" => f.data = val().into(),
            "--plugins-dir" => f.plugins_dir = Some(val().into()),
            "--cache-dir" => f.cache_dir = Some(val().into()),
            "--run-dir" => f.run_dir = Some(val().into()),
            "--api-socket" => f.api_socket = Some(val().into()),
            "--dispatch-socket" => f.dispatch_socket = Some(val().into()),
            "--pool" => f.pool = val().parse().unwrap_or_else(|_| usage("--pool takes a number")),
            "--version" => {
                println!(
                    "maktaba-plugind {} (wasmtime {})",
                    env!("CARGO_PKG_VERSION"),
                    plugind_core::engine::WASMTIME_VERSION
                );
                std::process::exit(0)
            }
            #[cfg(feature = "escape-probes")]
            "--escape-probes" => f.probes = true,
            #[cfg(feature = "escape-probes")]
            "--no-sandbox" => f.no_sandbox = true,
            other => usage(&format!("unknown flag {other}")),
        }
    }
    if f.data.as_os_str().is_empty() {
        usage("--data is required");
    }
    f
}

fn main() {
    let f = flags();
    let plugins_dir = f.plugins_dir.clone().unwrap_or_else(|| f.data.join("plugins"));
    let cache_dir = f.cache_dir.clone().unwrap_or_else(|| plugins_dir.join("cache"));
    let run_dir = f.run_dir.clone().unwrap_or_else(|| f.data.join("run").join("plugins"));
    let api_socket = f.api_socket.clone().unwrap_or_else(|| f.data.join("run").join("plugin-api.sock"));
    let dispatch_socket = f.dispatch_socket.clone().unwrap_or_else(|| run_dir.join("plugind.sock"));
    for d in [&plugins_dir, &cache_dir, &run_dir] {
        if let Err(e) = std::fs::create_dir_all(d) {
            eprintln!("maktaba-plugind: {}: {e}", d.display());
            std::process::exit(1);
        }
    }
    // resolved now: once sandboxed, the host may not stat the folders above its own
    let real = |p: &PathBuf| p.canonicalize().unwrap_or_else(|_| p.clone());
    let (plugins_dir, cache_dir, run_dir) = (real(&plugins_dir), real(&cache_dir), real(&run_dir));
    // the only listener, bound before the sandbox closes everything else
    let listener = match plugind_core::uds::bind(&dispatch_socket) {
        Ok(l) => l,
        Err(e) => {
            eprintln!("maktaba-plugind: bind {}: {e}", dispatch_socket.display());
            std::process::exit(1);
        }
    };
    let paths = sandbox::Paths {
        plugins_dir: plugins_dir.clone(),
        cache_dir: cache_dir.clone(),
        run_dir,
        api_socket: api_socket.clone(),
        dispatch_socket,
        pool_size: f.pool.max(1),
    };
    let applied = if f.no_sandbox {
        sandbox::Applied { level: "none".into(), missing: vec!["all".into()] }
    } else {
        match sandbox::apply(&paths) {
            Ok(a) => a,
            Err(e) => {
                eprintln!("{e}");
                std::process::exit(sandbox::EXIT_UNAVAILABLE);
            }
        }
    };
    #[cfg(feature = "escape-probes")]
    if f.probes {
        println!("SANDBOX {} missing={}", applied.level, applied.missing.join(","));
        probes::print(&probes::run());
        std::process::exit(0);
    }
    let _ = f.probes;
    let logs = Arc::new(Logs::new(Arc::new(StdoutSink)));
    let host = match Host::new(HostConfig {
        plugins_dir,
        cache_dir,
        pool_size: f.pool.max(1),
        api: Arc::new(UnixApi::new(api_socket)),
        logs: logs.clone(),
        skip_audit: false,
        sandbox: applied.level.clone(),
        sandbox_missing: applied.missing.clone(),
    }) {
        Ok(h) => h,
        Err(e) => {
            eprintln!("maktaba-plugind: {e:#}");
            std::process::exit(1);
        }
    };
    let level = if applied.missing.is_empty() { "info" } else { "warn" };
    logs.host(
        level,
        if applied.missing.is_empty() { "started" } else { "sandbox_partial" },
        serde_json::json!({"sandbox": applied.level, "missing": applied.missing, "pool": f.pool, "wasmtime": plugind_core::engine::WASMTIME_VERSION}),
    );
    if let Err(e) = plugind_core::serve::serve(host, listener) {
        eprintln!("maktaba-plugind: serve: {e}");
        std::process::exit(1);
    }
}
