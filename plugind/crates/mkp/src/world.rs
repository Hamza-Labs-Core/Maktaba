//! `mkp world`: a project's WIT, with the `plugin` world narrowed to what its manifest may
//! import (story 54.12 §2).
//!
//! The home's import audit (54.02 §6) refuses a component that imports a gated
//! `maktaba:plugin` interface no declared capability covers. Rust's bindings import only
//! the functions a plugin calls, but componentize-py and jco import every interface of the
//! world they are built against. So a Python or JS project is built against the SDK's WIT
//! with the world's imports cut to the ungated interfaces and the ones its capabilities
//! cover, read from the same tables the audit reads: the two cannot disagree.

use std::collections::BTreeSet;
use std::path::Path;

use plugind_core::audit::{covered_interfaces, UNGATED};

/// The SDK's WIT, every interface and the whole `plugin` world.
pub const WIT: &str = include_str!("../../../../shared/plugins/wit/maktaba-plugin.wit");

/// The interfaces a manifest's world imports: the ungated ones and those its declared
/// capabilities cover.
pub fn allowed(declared: &[String]) -> BTreeSet<String> {
    let mut out: BTreeSet<String> = UNGATED.iter().map(|s| s.to_string()).collect();
    out.extend(covered_interfaces(declared));
    out
}

/// `wit` with the `plugin` world's `import <iface>;` lines kept only for `keep`. Everything
/// else (the interfaces, the world's records and its export) is unchanged.
pub fn narrow(wit: &str, keep: &BTreeSet<String>) -> Result<String, String> {
    let mut out = String::with_capacity(wit.len());
    let mut in_world = false;
    let mut seen_world = false;
    for line in wit.split_inclusive('\n') {
        let t = line.trim();
        if t.starts_with("world plugin ") && t.ends_with('{') {
            in_world = true;
            seen_world = true;
        } else if in_world && t == "}" && !line.starts_with(' ') {
            in_world = false;
        } else if in_world {
            if let Some(iface) = t.strip_prefix("import ").and_then(|r| r.strip_suffix(';')) {
                if !keep.contains(iface.trim()) {
                    continue;
                }
            }
        }
        out.push_str(line);
    }
    if !seen_world {
        return Err("the SDK's WIT has no `world plugin`".into());
    }
    Ok(out)
}

/// Writes `<project>/wit/maktaba-plugin.wit` for the manifest in `<project>/package`: what
/// `mkp build` does first, and `mkp world` alone.
pub fn write(project: &Path) -> Result<BTreeSet<String>, String> {
    let m = crate::validate(&project.join("package"))?;
    let keep = allowed(&plugind_core::engine::declared(&m));
    let wit = narrow(WIT, &keep)?;
    let dir = project.join("wit");
    std::fs::create_dir_all(&dir).map_err(|e| format!("{}: {e}", dir.display()))?;
    let p = dir.join("maktaba-plugin.wit");
    std::fs::write(&p, wit).map_err(|e| format!("{}: {e}", p.display()))?;
    Ok(keep)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn imports(wit: &str) -> Vec<String> {
        let start = wit.find("world plugin {").unwrap();
        wit[start..]
            .lines()
            .filter_map(|l| l.trim().strip_prefix("import ").and_then(|r| r.strip_suffix(';')))
            .map(String::from)
            .collect()
    }

    #[test]
    fn a_manifest_with_no_capability_imports_only_the_ungated() {
        let w = narrow(WIT, &allowed(&[])).unwrap();
        assert_eq!(imports(&w), ["job", "log", "data", "events"]);
        // the interfaces themselves and the export stay
        assert!(w.contains("interface write {") && w.contains("export run: func"));
    }

    #[test]
    fn a_capability_adds_its_interface() {
        let w = narrow(WIT, &allowed(&["tags.write".to_string()])).unwrap();
        let got = imports(&w);
        assert!(got.contains(&"write".to_string()), "{got:?}");
        assert!(!got.contains(&"compute".to_string()), "{got:?}");
    }

    #[test]
    fn every_interface_when_every_capability_is_declared() {
        let all: Vec<String> = maktaba_plugin_manifest::capabilities().map(|c| c.name.to_string()).collect();
        let w = narrow(WIT, &allowed(&all)).unwrap();
        assert_eq!(imports(&w), imports(WIT));
    }
}
