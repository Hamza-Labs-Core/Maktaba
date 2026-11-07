//! `mkp build <project> [--entry <file>]`: the language's toolchain makes
//! `package/plugin.wasm`, then the home's checks run on it (story 54.12 §2: "runs the
//! language toolchain, then the same validation and import audit as the home").
//!
//! - Python (`app.py`): `componentize-py --stub-wasi`, against the project's world narrowed
//!   to its manifest. Every WASI import becomes a stub that fails when called, so the
//!   component imports none (the host links no filesystem, socket or stdin).
//! - JavaScript (`plugin.js`, or `--entry`): `jco componentize --disable all`, against the
//!   same narrowed world: no WASI feature is built in, so none is imported.
//! - Go (`go.mod`): the bindings from the narrowed world (`wit-bindgen-go`), TinyGo's
//!   `wasm-unknown` target (it links no WASI: TinyGo's wasip2 runtime asks for stdin at every
//!   start, which the host never links), and `wasm-tools component embed` and `new` into
//!   the component.
//! - Rust (`Cargo.toml`): `cargo build --release --target wasm32-wasip2`.
//!
//! The toolchains are found on PATH, or named by MKP_COMPONENTIZE_PY, MKP_JCO, MKP_GO,
//! MKP_TINYGO, MKP_WIT_BINDGEN_GO and MKP_WASM_TOOLS. Whatever the language, the home's
//! audit runs last: a component that imports anything the home refuses fails the build.

use std::path::{Path, PathBuf};
use std::process::Command;

enum Lang {
    Python,
    Js(PathBuf),
    Go,
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
    if project.join("go.mod").is_file() {
        return Ok(Lang::Go);
    }
    if project.join("Cargo.toml").is_file() {
        return Ok(Lang::Rust);
    }
    Err(format!("{}: no app.py, plugin.js, go.mod or Cargo.toml (a project from `mkp new`)", project.display()))
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
        Lang::Python | Lang::Js(_) | Lang::Go => {
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
        Lang::Go => go(project, &out)?,
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

/// Go: bindings, TinyGo's wasm-unknown core module, and the component around it.
fn go(project: &Path, out: &Path) -> Result<(), String> {
    let wasm_tools = tool("MKP_WASM_TOOLS", "wasm-tools");
    // wit-bindgen-go reads the WIT through wasm-tools: put the one named on its PATH
    let mut path = std::env::var_os("PATH").unwrap_or_default();
    if let Some(dir) = Path::new(&wasm_tools).parent().filter(|d| !d.as_os_str().is_empty()) {
        let mut dirs = vec![dir.to_path_buf()];
        dirs.extend(std::env::split_paths(&path));
        path = std::env::join_paths(dirs).map_err(|e| e.to_string())?;
    }
    // the bindings of interfaces the world no longer imports go with them
    let gen = project.join("internal").join("maktaba");
    if gen.exists() {
        std::fs::remove_dir_all(&gen).map_err(|e| format!("{}: {e}", gen.display()))?;
    }
    run(
        Command::new(tool("MKP_WIT_BINDGEN_GO", "wit-bindgen-go"))
            .current_dir(project)
            .env("PATH", &path)
            .args(["generate", "--world", "plugin", "--out", "internal", "./wit"]),
        "wit-bindgen-go",
    )?;
    run(Command::new(tool("MKP_GO", "go")).current_dir(project).args(["mod", "tidy"]), "go mod tidy")?;
    let build = project.join("build");
    std::fs::create_dir_all(&build).map_err(|e| format!("{}: {e}", build.display()))?;
    run(
        Command::new(tool("MKP_TINYGO", "tinygo")).current_dir(project).env("PATH", &path).args([
            "build",
            "-target=wasm-unknown",
            "--no-debug",
            "-o",
            "build/core.wasm",
            ".",
        ]),
        "tinygo build",
    )?;
    run(
        Command::new(&wasm_tools).current_dir(project).args([
            "component",
            "embed",
            "wit",
            "--world",
            "plugin",
            "build/core.wasm",
            "-o",
            "build/embedded.wasm",
        ]),
        "wasm-tools component embed",
    )?;
    run(
        Command::new(&wasm_tools).current_dir(project).args(["component", "new", "build/embedded.wasm", "-o"]).arg(out),
        "wasm-tools component new",
    )
}
