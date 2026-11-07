//! `mkp new rust`: a plugin project from the Rust template (story 54.12 §2). The guest is
//! no_std (Rust std on wasm32-wasip2 imports stdin and the terminal, which the host never
//! links: #683), with wit-bindgen from the WIT the SDK ships beside it.

use std::path::Path;

const LIB_RS: &str = include_str!("../template/rust/src/lib.rs");
const CARGO_TOML: &str = include_str!("../template/rust/Cargo.toml.in");
const MANIFEST: &str = include_str!("../template/rust/package/manifest.toml.in");
const EN: &str = include_str!("../template/rust/package/i18n/en.json");
const AR: &str = include_str!("../template/rust/package/i18n/ar.json");
const README: &str = include_str!("../template/rust/README.md.in");
const WIT: &str = include_str!("../../../../shared/plugins/wit/maktaba-plugin.wit");

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
