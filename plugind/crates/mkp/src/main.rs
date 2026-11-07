//! mkp: the Maktaba plugin SDK's CLI (Epic 54, story 54.12).
//!
//!   mkp new rust|python|js <id> <folder>
//!                                     a project from a template (Rust: no_std, wit-bindgen;
//!                                     Python: componentize-py; JavaScript: jco componentize)
//!   mkp build <project> [--entry <file.js>]
//!                                     the language's toolchain makes package/plugin.wasm,
//!                                     then the home's checks (as `mkp check`)
//!   mkp world <project>               the project's WIT with the `plugin` world narrowed to
//!                                     what its manifest may import (Python, JS)
//!   mkp check <folder>                the home's manifest validation (54.01) and import audit (54.02)
//!   mkp pack <folder> <out.mkplugin> [--key <id>]
//!                                     files.json and the package, every file's sha256 listed;
//!                                     with --key, signed (signatures/developer.sig, 54.14)
//!   mkp run <folder> --home <http://home:port> --user <admin>
//!                                     installs it on a home in developer mode (Development,
//!                                     turned on) over the admin API; again to update it
//!   mkp keys new | list | export-public [id]
//!                                     the developer's Ed25519 signing key (54.14 §1)
//!   mkp test <folder> --stage <name> [--subject kind:id] [--fixtures api.json] [--settings json]
//!                                     run a stage in the harness: the same host code as
//!                                     maktaba-plugind (plugind-core), a mock plugin API
//!                                     answering from fixtures, every call recorded
//!
//! The package folder holds manifest.toml, i18n/en.json, i18n/ar.json and plugin.wasm (and
//! ui/settings.schema.json when the manifest names one). The store countersigns a package
//! after review (store.sig); `mkp pack --key` makes only the developer's signature.

mod buildcmd;
mod flow;
mod harness;
mod keys;
mod pack;
mod run;
mod template;
mod world;

use std::path::{Path, PathBuf};
use std::process::exit;

fn usage() -> ! {
    eprintln!(
        "usage:\n  mkp new rust|python|js <id> <folder>\n  mkp build <project> [--entry <file.js>]\n  \
         mkp world <project>\n  mkp check <folder>\n  mkp pack <folder> <out.mkplugin> [--key <id>]\n  \
         mkp keys new | list | export-public [id]\n  \
         mkp run <folder> --home <http://home:port> --user <admin>\n  \
         mkp test <folder> --stage <name> [--subject kind:id] [--fixtures api.json] [--settings json] [--with <folder>]\n  \
         mkp flow <folder> [<folder>...]"
    );
    exit(2)
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let code = match args.first().map(String::as_str) {
        Some("new") if args.len() == 4 && args[1] == "rust" => template::new_rust(&args[2], Path::new(&args[3])),
        Some("new") if args.len() == 4 && (args[1] == "python" || args[1] == "js") => {
            template::new_lang(&args[1], &args[2], Path::new(&args[3]))
        }
        Some("build") if args.len() == 2 => buildcmd::build(Path::new(&args[1]), None),
        Some("build") if args.len() == 4 && args[2] == "--entry" => {
            buildcmd::build(Path::new(&args[1]), Some(Path::new(&args[3])))
        }
        Some("world") if args.len() == 2 => world::write(Path::new(&args[1])).map(|keep| {
            println!("world: {}", keep.into_iter().collect::<Vec<_>>().join(", "));
        }),
        Some("check") if args.len() == 2 => check(Path::new(&args[1])),
        Some("pack") if args.len() == 3 => pack::pack(Path::new(&args[1]), Path::new(&args[2]), None),
        Some("pack") if args.len() == 5 && args[3] == "--key" => {
            keys::load(&args[4]).and_then(|k| pack::pack(Path::new(&args[1]), Path::new(&args[2]), Some(&k)))
        }
        Some("keys") if args.len() == 2 && args[1] == "new" => keys::new_key(),
        Some("keys") if args.len() == 2 && args[1] == "list" => keys::list(),
        Some("keys") if (2..=3).contains(&args.len()) && args[1] == "export-public" => {
            keys::export_public(args.get(2).map(String::as_str))
        }
        Some("test") if args.len() >= 2 => test(&args[1..]),
        Some("run") if args.len() >= 2 => run_cmd(&args[1..]),
        Some("flow") if args.len() >= 2 => flow_cmd(&args[1..]),
        Some("--version") => {
            println!("mkp {} (plugin API {}.{})", env!("CARGO_PKG_VERSION"), 1, 2);
            Ok(())
        }
        _ => usage(),
    };
    if let Err(e) = code {
        eprintln!("mkp: {e}");
        exit(1)
    }
}

/// The manifest as the home reads it, with the package's files.
pub fn validate(dir: &Path) -> Result<serde_json::Map<String, serde_json::Value>, String> {
    let src = std::fs::read(dir.join("manifest.toml")).map_err(|e| format!("manifest.toml: {e}"))?;
    let files = |p: &str| -> Option<Vec<u8>> {
        if p.contains("..") || p.starts_with('/') {
            return None;
        }
        std::fs::read(dir.join(p)).ok()
    };
    maktaba_plugin_manifest::validate(&src, Some(&files)).map_err(|errs| {
        errs.iter().map(|e| format!("{} {}: {}", e.code, e.path, e.detail)).collect::<Vec<_>>().join("\n")
    })
}

pub fn check(dir: &Path) -> Result<(), String> {
    let m = validate(dir)?;
    let report = harness::preload(dir, &m)?;
    println!("{report}");
    Ok(())
}

fn test(args: &[String]) -> Result<(), String> {
    let dir = PathBuf::from(&args[0]);
    let mut opts = harness::Options::default();
    let mut with: Option<PathBuf> = None;
    let mut it = args[1..].iter();
    while let Some(a) = it.next() {
        let mut val = || it.next().cloned().ok_or_else(|| format!("{a} needs a value"));
        match a.as_str() {
            "--stage" => opts.stage = val()?,
            "--subject" => opts.subject = val()?,
            "--fixtures" => opts.fixtures = Some(PathBuf::from(val()?)),
            "--settings" => opts.settings = val()?,
            "--with" => with = Some(PathBuf::from(val()?)),
            other => return Err(format!("unknown flag {other}")),
        }
    }
    if opts.stage.is_empty() {
        return Err("--stage is required".into());
    }
    let m = validate(&dir)?;
    if let Some(other) = with {
        return chain(&dir, &m, &other, opts);
    }
    let out = harness::run(&dir, &m, &opts)?;
    println!("{}", serde_json::to_string_pretty(&out).unwrap());
    if out["result"]["outcome"] != "ok" {
        exit(1)
    }
    Ok(())
}

/// `mkp flow <folder>...`: the manifests' graph as a text chart, and flow.mmd beside the
/// first folder (story 54.41 §3).
fn flow_cmd(dirs: &[String]) -> Result<(), String> {
    let ms = dirs.iter().map(|d| validate(Path::new(d))).collect::<Result<Vec<_>, _>>()?;
    let es = flow::edges(&ms);
    println!("{}", flow::text(&es));
    let mmd = Path::new(&dirs[0]).join("flow.mmd");
    std::fs::write(&mmd, flow::mermaid(&es) + "\n").map_err(|e| format!("{}: {e}", mmd.display()))?;
    Ok(())
}

/// `mkp test <a> --stage s --with <b>`: runs a's stage, then each of b's stages that
/// subscribes to an event a emitted, with the event's payload and a lineage of 2 (story
/// 54.41 §3, 54.40). The report has both results and the chain drawn.
fn chain(
    dir: &Path,
    a: &serde_json::Map<String, serde_json::Value>,
    other: &Path,
    mut opts: harness::Options,
) -> Result<(), String> {
    use serde_json::{json, Value};
    let b = validate(other)?;
    let a_id = a["id"].as_str().unwrap_or_default().to_string();
    let b_id = b["id"].as_str().unwrap_or_default().to_string();
    // b's stages by the published event of a they follow
    let mut follows: Vec<(String, String)> = Vec::new();
    for st in b["stage"].as_array().into_iter().flatten() {
        for t in st["trigger"].as_array().into_iter().flatten() {
            if let Some(ev) = t["event"].as_str().and_then(|e| e.strip_prefix(&format!("plugin:{a_id}/"))) {
                follows.push((ev.to_string(), st["name"].as_str().unwrap_or_default().to_string()));
            }
        }
    }
    opts.subscribers = follows.iter().map(|(_, s)| maktaba_plugin_manifest::queue_name(&b_id, s)).collect();
    let first = harness::run(dir, a, &opts)?;
    let mut ok = first["result"]["outcome"] == "ok";
    let mut ran = Vec::new();
    for ev in first["result"]["events"].as_array().cloned().unwrap_or_default() {
        let name = ev["name"].as_str().unwrap_or_default();
        let payload: Value = serde_json::from_str(ev["payload"].as_str().unwrap_or("{}")).unwrap_or(json!({}));
        for (_, stage) in follows.iter().filter(|(e, _)| e == name) {
            let o = harness::Options {
                stage: stage.clone(),
                subject: format!(
                    "{}:{}",
                    ev["subject"]["kind"].as_str().unwrap_or("video"),
                    ev["subject"]["id"].as_str().unwrap_or("fixture")
                ),
                fixtures: opts.fixtures.clone(),
                settings: String::new(),
                payload: json!({"event": name, "from": a_id, "payload": payload}).to_string(),
                subscribers: Vec::new(),
            };
            let out = harness::run(other, &b, &o)?;
            ok &= out["result"]["outcome"] == "ok";
            ran.push(json!({"plugin": b_id, "stage": stage, "event": name, "hops": 2, "run": out}));
        }
    }
    let es = flow::edges(&[a.clone(), b]);
    let report = json!({"first": first, "chain": ran, "flow": flow::text(&es)});
    println!("{}", serde_json::to_string_pretty(&report).unwrap());
    if !ok {
        exit(1)
    }
    Ok(())
}

/// A manifest's id and version (for the developer signature).
pub fn manifest_of(src: &[u8]) -> Result<(String, String), String> {
    let m = maktaba_plugin_manifest::validate(src, None).map_err(|errs| {
        errs.iter().map(|e| format!("{} {}: {}", e.code, e.path, e.detail)).collect::<Vec<_>>().join("\n")
    })?;
    let s = |k: &str| m.get(k).and_then(|v| v.as_str()).map(str::to_string).ok_or(format!("the manifest has no {k}"));
    Ok((s("id")?, s("version")?))
}

/// `mkp run <folder> --home <url> [--user <admin>]` (story 54.12 §2).
fn run_cmd(args: &[String]) -> Result<(), String> {
    let mut home = String::new();
    let mut user = std::env::var("MKP_HOME_USER").unwrap_or_default();
    let mut i = 1;
    while i < args.len() {
        match (args[i].as_str(), args.get(i + 1)) {
            ("--home", Some(v)) => home = v.clone(),
            ("--user", Some(v)) => user = v.clone(),
            _ => usage(),
        }
        i += 2;
    }
    if home.is_empty() || user.is_empty() {
        return Err("mkp run needs --home <http://home:port> and --user <admin> (or MKP_HOME_USER)".into());
    }
    run::run(Path::new(&args[0]), &home, &user)
}
