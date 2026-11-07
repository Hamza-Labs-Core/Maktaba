//! `mkp build <project> [--entry <file>]`: the language's toolchain makes
//! `package/plugin.wasm`, then the home's checks run on it (story 54.12 §2: "runs the
//! language toolchain, then the same validation and import audit as the home").
//!
//! - Python (`app.py`): `componentize-py --stub-wasi`, against the project's world narrowed
//!   to its manifest. Every WASI import becomes a stub that fails when called, so the
//!   component imports none (the host links no filesystem, socket or stdin).
//! - JavaScript (`plugin.js`, or `--entry`): `jco componentize --disable all`, against the
//!   same narrowed world: no WASI feature is built in, so none is imported.
//! - Rust (`Cargo.toml`): `cargo build --release --target wasm32-wasip2`.
//!
//! The toolchains are found on PATH, or named by MKP_COMPONENTIZE_PY and MKP_JCO.

use std::path::{Path, PathBuf};
use std::process::Command;

enum Lang {
    Python,
    Js(PathBuf),
    Rust,
}

fn detect(project: &Path, entry: Option<&Path>) -> Result<Lang, String> {
    if let Some(e) = entry {
        return Ok(Lang::Js(e.to_path_buf()));
    }
    if project.join("app.py").is_file() {
        return Ok(Lang::Python);
    }
    if project.join("plugin.js").is_file() {
        return Ok(Lang::Js(PathBuf::from("plugin.js")));
    }
    if project.join("Cargo.toml").is_file() {
        return Ok(Lang::Rust);
    }
    Err(format!("{}: no app.py, plugin.js or Cargo.toml (a project from `mkp new`)", project.display()))
}

fn tool(env: &str, default: &str) -> String {
    std::env::var(env).ok().filter(|v| !v.is_empty()).unwrap_or_else(|| default.to_string())
}

fn run(cmd: &mut Command, what: &str) -> Result<(), String> {
    let status = cmd.status().map_err(|e| format!("{what}: {e} (is it installed? see the project's README)"))?;
    if !status.success() {
        return Err(format!("{what} failed ({status})"));
    }
    Ok(())
}

/// The crate name of a Rust project (its Cargo.toml's [package] name, as cargo names the
/// .wasm).
fn crate_name(project: &Path) -> Result<String, String> {
    let toml = std::fs::read_to_string(project.join("Cargo.toml")).map_err(|e| format!("Cargo.toml: {e}"))?;
    toml.lines()
        .filter_map(|l| l.trim().strip_prefix("name"))
        .filter_map(|r| r.trim().strip_prefix('='))
        .map(|v| v.trim().trim_matches('"').replace('-', "_"))
        .next()
        .ok_or_else(|| "Cargo.toml has no package name".into())
}

pub fn build(project: &Path, entry: Option<&Path>) -> Result<(), String> {
    let lang = detect(project, entry)?;
    let out = project.join("package").join("plugin.wasm");
    match &lang {
        Lang::Python | Lang::Js(_) => {
            let keep = crate::world::write(project)?;
            println!("world: {}", keep.into_iter().collect::<Vec<_>>().join(", "));
        }
        Lang::Rust => {}
    }
    match lang {
        Lang::Python => run(
            Command::new(tool("MKP_COMPONENTIZE_PY", "componentize-py"))
                .current_dir(project)
                .args(["-d", "wit", "-w", "plugin", "componentize", "--stub-wasi", "app", "-o"])
                .arg("package/plugin.wasm"),
            "componentize-py",
        )?,
        Lang::Js(e) => run(
            Command::new(tool("MKP_JCO", "jco"))
                .current_dir(project)
                .arg("componentize")
                .arg(&e)
                .args(["--wit", "wit", "--world-name", "plugin", "--disable", "all", "-o"])
                .arg("package/plugin.wasm"),
            "jco componentize",
        )?,
        Lang::Rust => {
            run(
                Command::new("cargo").current_dir(project).args(["build", "--release", "--target", "wasm32-wasip2"]),
                "cargo build",
            )?;
            let name = crate_name(project)?;
            let built = project.join(format!("target/wasm32-wasip2/release/{name}.wasm"));
            std::fs::copy(&built, &out).map_err(|e| format!("{}: {e}", built.display()))?;
        }
    }
    // the home's checks on what was built: a refused or undeclared import fails the build
    crate::check(&project.join("package"))
}
