//! mkp: the Maktaba plugin SDK's CLI (Epic 54, story 54.12).
//!
//!   mkp new rust <id> <folder>        a project from the Rust template (no_std, wit-bindgen)
//!   mkp check <folder>                the home's manifest validation (54.01) and import audit (54.02)
//!   mkp pack <folder> <out.mkplugin>  files.json and the package, every file's sha256 listed
//!   mkp test <folder> --stage <name> [--subject kind:id] [--fixtures api.json] [--settings json]
//!                                     run a stage in the harness: the same host code as
//!                                     maktaba-plugind (plugind-core), a mock plugin API
//!                                     answering from fixtures, every call recorded
//!
//! The package folder holds manifest.toml, i18n/en.json, i18n/ar.json and plugin.wasm (and
//! ui/settings.schema.json when the manifest names one). `mkp pack` signs nothing: signing
//! is Phase 2 (54.14).

mod harness;
mod pack;
mod template;

use std::path::{Path, PathBuf};
use std::process::exit;

fn usage() -> ! {
    eprintln!(
        "usage:\n  mkp new rust <id> <folder>\n  mkp check <folder>\n  mkp pack <folder> <out.mkplugin>\n  \
         mkp test <folder> --stage <name> [--subject kind:id] [--fixtures api.json] [--settings json]"
    );
    exit(2)
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let code = match args.first().map(String::as_str) {
        Some("new") if args.len() == 4 && args[1] == "rust" => template::new_rust(&args[2], Path::new(&args[3])),
        Some("check") if args.len() == 2 => check(Path::new(&args[1])),
        Some("pack") if args.len() == 3 => pack::pack(Path::new(&args[1]), Path::new(&args[2])),
        Some("test") if args.len() >= 2 => test(&args[1..]),
        Some("--version") => {
            println!("mkp {} (plugin API {}.{})", env!("CARGO_PKG_VERSION"), 1, 1);
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

fn check(dir: &Path) -> Result<(), String> {
    let m = validate(dir)?;
    let report = harness::preload(dir, &m)?;
    println!("{report}");
    Ok(())
}

fn test(args: &[String]) -> Result<(), String> {
    let dir = PathBuf::from(&args[0]);
    let mut opts = harness::Options::default();
    let mut it = args[1..].iter();
    while let Some(a) = it.next() {
        let mut val = || it.next().cloned().ok_or_else(|| format!("{a} needs a value"));
        match a.as_str() {
            "--stage" => opts.stage = val()?,
            "--subject" => opts.subject = val()?,
            "--fixtures" => opts.fixtures = Some(PathBuf::from(val()?)),
            "--settings" => opts.settings = val()?,
            other => return Err(format!("unknown flag {other}")),
        }
    }
    if opts.stage.is_empty() {
        return Err("--stage is required".into());
    }
    let m = validate(&dir)?;
    let out = harness::run(&dir, &m, &opts)?;
    println!("{}", serde_json::to_string_pretty(&out).unwrap());
    if out["result"]["outcome"] != "ok" {
        exit(1)
    }
    Ok(())
}
