//! `mkp new rust|python|js`: a plugin project from a template (story 54.12 §2).
//!
//! - Rust: no_std (Rust std on wasm32-wasip2 imports stdin and the terminal, which the host
//!   never links: #683), with wit-bindgen from the WIT the SDK ships beside it.
//! - Python (componentize-py) and JavaScript (jco componentize): `mkp build` makes the
//!   component, against a world narrowed to the manifest (crate::world), with WASI stubbed
//!   (Python) or turned off (JS), so it imports only what the home links.

use std::path::Path;

const LIB_RS: &str = include_str!("../template/rust/src/lib.rs");
const CARGO_TOML: &str = include_str!("../template/rust/Cargo.toml.in");
const MANIFEST: &str = include_str!("../template/rust/package/manifest.toml.in");
const EN: &str = include_str!("../template/rust/package/i18n/en.json");
const AR: &str = include_str!("../template/rust/package/i18n/ar.json");
const README: &str = include_str!("../template/rust/README.md.in");
const WIT: &str = crate::world::WIT;
const PY_APP: &str = include_str!("../template/python/app.py");
const PY_README: &str = include_str!("../template/python/README.md.in");
const JS_PLUGIN: &str = include_str!("../template/js/plugin.js");
const JS_README: &str = include_str!("../template/js/README.md.in");

pub fn new_rust(id: &str, dir: &Path) -> Result<(), String> {
    if !maktaba_plugin_manifest::valid_id(id) {
        return Err(format!("{id:?} is not a plugin id (reverse DNS, at least three labels: dev.example.hello)"));
    }
    if dir.exists() {
        return Err(format!("{} exists", dir.display()));
    }
    let crate_name = id.rsplit('.').next().unwrap_or("plugin").replace('-', "_");
    let fill = |s: &str| s.replace("{{id}}", id).replace("{{crate}}", &crate_name);
    let files: [(&str, String); 7] = [
        ("Cargo.toml", fill(CARGO_TOML)),
        ("src/lib.rs", fill(LIB_RS)),
        ("wit/maktaba-plugin.wit", WIT.to_string()),
        ("package/manifest.toml", fill(MANIFEST)),
        ("package/i18n/en.json", EN.to_string()),
        ("package/i18n/ar.json", AR.to_string()),
        ("README.md", fill(README)),
    ];
    for (rel, body) in files {
        let p = dir.join(rel);
        std::fs::create_dir_all(p.parent().unwrap()).map_err(|e| e.to_string())?;
        std::fs::write(&p, body).map_err(|e| format!("{}: {e}", p.display()))?;
    }
    println!("{}: a Rust plugin {id}; see its README.md", dir.display());
    Ok(())
}

/// A project for `lang` (python, js): its source, the package and README, and its world
/// narrowed to the template's manifest.
pub fn new_lang(lang: &str, id: &str, dir: &Path) -> Result<(), String> {
    if !maktaba_plugin_manifest::valid_id(id) {
        return Err(format!("{id:?} is not a plugin id (reverse DNS, at least three labels: dev.example.hello)"));
    }
    if dir.exists() {
        return Err(format!("{} exists", dir.display()));
    }
    let fill = |s: &str| s.replace("{{id}}", id);
    let (source, body, readme, name) = match lang {
        "python" => ("app.py", PY_APP, PY_README, "Python"),
        "js" => ("plugin.js", JS_PLUGIN, JS_README, "JavaScript"),
        other => return Err(format!("no template for {other:?} (rust, python, js)")),
    };
    let files: [(&str, String); 5] = [
        (source, fill(body)),
        ("package/manifest.toml", fill(MANIFEST)),
        ("package/i18n/en.json", EN.to_string()),
        ("package/i18n/ar.json", AR.to_string()),
        ("README.md", fill(readme)),
    ];
    for (rel, body) in files {
        let p = dir.join(rel);
        std::fs::create_dir_all(p.parent().unwrap()).map_err(|e| e.to_string())?;
        std::fs::write(&p, body).map_err(|e| format!("{}: {e}", p.display()))?;
    }
    crate::world::write(dir)?;
    println!("{}: a {name} plugin {id}; see its README.md", dir.display());
    Ok(())
}
