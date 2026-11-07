//! The import audit (story 54.02 §6, README §5.2): before a component is compiled for a
//! home, its imports must be a subset of what the host links, and every gated
//! `maktaba:plugin` interface it imports must be covered by a capability the manifest
//! declares. The same audit runs in `mkp build` (54.12) and the store (54.16).
//!
//! The audit is the first wall; the linker is the second: an import the host does not
//! link fails instantiation even when the audit is bypassed (a test flag only).

use std::collections::BTreeSet;

use maktaba_plugin_manifest as manifest;

pub const CODE_NOT_COMPONENT: &str = "plugin_not_component";
pub const CODE_FORBIDDEN_IMPORT: &str = "plugin_forbidden_import";
pub const CODE_UNDECLARED_IMPORT: &str = "plugin_undeclared_import";
pub const CODE_MISSING_EXPORT: &str = "plugin_missing_export";
pub const CODE_API_NEWER: &str = "plugin_api_newer";

/// The plugin API this host serves (the WIT package's major and minor).
pub const PLUGIN_API_MAJOR: u64 = 1;
pub const PLUGIN_API_MINOR: u64 = 1;

/// The WASI interfaces the host links (README §5.2), without their versions. Nothing else
/// of WASI is linked: not sockets, http, filesystem, stdin, terminals or processes.
pub const LINKED_WASI: &[&str] = &[
    "wasi:clocks/monotonic-clock",
    "wasi:clocks/wall-clock",
    "wasi:random/random",
    "wasi:random/insecure",
    "wasi:random/insecure-seed",
    "wasi:io/error",
    "wasi:io/poll",
    "wasi:io/streams",
    "wasi:cli/stdout",
    "wasi:cli/stderr",
    "wasi:cli/environment",
    "wasi:cli/exit",
];

/// The `maktaba:plugin` interfaces every plugin may import without a capability.
pub const UNGATED: &[&str] = &["types", "job", "log", "data"];

/// Every `maktaba:plugin` interface the host links.
pub const PLUGIN_INTERFACES: &[&str] = &[
    "types",
    "job",
    "log",
    "data",
    "library",
    "transcript",
    "media",
    "write",
    "suggest",
    "queue",
    "notify",
    "exports",
    "compute",
];

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AuditError {
    pub code: &'static str,
    pub detail: String,
}

impl std::fmt::Display for AuditError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.code, self.detail)
    }
}

/// `package/interface` and the version of an import name ("wasi:io/streams@0.2.3").
fn split(name: &str) -> (&str, Option<&str>) {
    match name.split_once('@') {
        Some((n, v)) => (n, Some(v)),
        None => (name, None),
    }
}

/// The interfaces a declared capability set covers (capabilities.toml's `interface`;
/// `media.info` also answers to library.read).
pub fn covered_interfaces(declared: &[String]) -> BTreeSet<String> {
    let mut out = BTreeSet::new();
    for d in declared {
        if let Some(c) = manifest::lookup_capability(d) {
            out.insert(c.interface.clone());
            if c.name == "library.read" {
                out.insert("media".into());
            }
        }
    }
    out
}

/// Audits a component's import and export names against the declared capabilities.
pub fn audit(imports: &[String], exports: &[String], declared: &[String]) -> Result<(), AuditError> {
    let covered = covered_interfaces(declared);
    for name in imports {
        let (iface, version) = split(name);
        if let Some(rest) = iface.strip_prefix("maktaba:plugin/") {
            if let Some(v) = version {
                let mut parts = v.split('.').map(|p| p.parse::<u64>().unwrap_or(u64::MAX));
                let (major, minor) = (parts.next().unwrap_or(u64::MAX), parts.next().unwrap_or(u64::MAX));
                if major != PLUGIN_API_MAJOR || minor > PLUGIN_API_MINOR {
                    return Err(AuditError { code: CODE_API_NEWER, detail: name.clone() });
                }
            }
            if !PLUGIN_INTERFACES.contains(&rest) {
                return Err(AuditError { code: CODE_FORBIDDEN_IMPORT, detail: name.clone() });
            }
            if !UNGATED.contains(&rest) && !covered.contains(rest) {
                return Err(AuditError { code: CODE_UNDECLARED_IMPORT, detail: name.clone() });
            }
            continue;
        }
        if !LINKED_WASI.contains(&iface) {
            return Err(AuditError { code: CODE_FORBIDDEN_IMPORT, detail: name.clone() });
        }
    }
    if !exports.iter().any(|e| e == "run") {
        return Err(AuditError { code: CODE_MISSING_EXPORT, detail: "run".into() });
    }
    Ok(())
}

/// The import and export names of a compiled component.
pub fn names(engine: &wasmtime::Engine, c: &wasmtime::component::Component) -> (Vec<String>, Vec<String>) {
    let ty = c.component_type();
    let imports = ty.imports(engine).map(|(n, _)| n.to_string()).collect();
    let exports = ty.exports(engine).map(|(n, _)| n.to_string()).collect();
    (imports, exports)
}

/// Is `bytes` a component (not a core module)? Layer byte 0x0d 0x00 0x01 0x00 after the
/// magic means a component in the component-model encoding.
pub fn is_component(bytes: &[u8]) -> bool {
    bytes.len() >= 8 && &bytes[0..4] == b"\0asm" && bytes[6..8] == [0x01, 0x00] && bytes[4] != 0x01
}

#[cfg(test)]
mod tests {
    use super::*;

    fn s(v: &[&str]) -> Vec<String> {
        v.iter().map(|x| x.to_string()).collect()
    }

    /// T01: the linked WASI set is the allow-list: nothing of sockets, http, filesystem,
    /// stdin or terminals.
    #[test]
    fn linked_wasi_is_the_allow_list() {
        for bad in ["wasi:sockets", "wasi:http", "wasi:filesystem", "wasi:cli/stdin", "wasi:cli/terminal"] {
            assert!(!LINKED_WASI.iter().any(|l| l.starts_with(bad)), "{bad}");
        }
    }

    /// T02 (names): forbidden imports are refused.
    #[test]
    fn forbidden_imports() {
        for name in [
            "wasi:sockets/tcp@0.2.0",
            "wasi:sockets/ip-name-lookup@0.2.0",
            "wasi:http/outgoing-handler@0.2.0",
            "wasi:filesystem/types@0.2.0",
            "wasi:filesystem/preopens@0.2.0",
            "wasi:cli/stdin@0.2.0",
            "wasi:cli/terminal-input@0.2.0",
            "maktaba:plugin/network@1.0.0",
        ] {
            let e = audit(&s(&[name]), &s(&["run"]), &[]).unwrap_err();
            assert_eq!(e.code, CODE_FORBIDDEN_IMPORT, "{name}");
        }
    }

    /// T05: a gated interface without its capability is undeclared.
    #[test]
    fn undeclared_imports() {
        let e = audit(&s(&["maktaba:plugin/media@1.0.0"]), &s(&["run"]), &[]).unwrap_err();
        assert_eq!(e.code, CODE_UNDECLARED_IMPORT);
        let e = audit(&s(&["maktaba:plugin/write@1.0.0"]), &s(&["run"]), &s(&["library.read"])).unwrap_err();
        assert_eq!(e.code, CODE_UNDECLARED_IMPORT);
        audit(&s(&["maktaba:plugin/media@1.0.0"]), &s(&["run"]), &s(&["media.audio"])).unwrap();
        audit(&s(&["maktaba:plugin/write@1.0.0"]), &s(&["run"]), &s(&["tags.write"])).unwrap();
        audit(&s(&["maktaba:plugin/exports@1.0.0"]), &s(&["run"]), &s(&["plugin-data.read:dev.example.other"]))
            .unwrap();
    }

    #[test]
    fn ungated_and_wasi_subset_pass() {
        audit(
            &s(&[
                "maktaba:plugin/job@1.0.0",
                "maktaba:plugin/log@1.0.0",
                "wasi:io/streams@0.2.6",
                "wasi:cli/stdout@0.2.6",
                "wasi:clocks/monotonic-clock@0.2.0",
            ]),
            &s(&["run"]),
            &[],
        )
        .unwrap();
    }

    #[test]
    fn newer_api_and_missing_run() {
        assert_eq!(audit(&s(&["maktaba:plugin/job@1.2.0"]), &s(&["run"]), &[]).unwrap_err().code, CODE_API_NEWER);
        assert_eq!(audit(&s(&["maktaba:plugin/job@2.0.0"]), &s(&["run"]), &[]).unwrap_err().code, CODE_API_NEWER);
        assert_eq!(audit(&[], &s(&["other"]), &[]).unwrap_err().code, CODE_MISSING_EXPORT);
    }

    #[test]
    fn api_1_1_compute_is_gated_and_1_0_plugins_still_pass() {
        // a plugin built for 1.0 runs on this 1.1 host (README §4.4: a minor only adds)
        audit(&s(&["maktaba:plugin/job@1.0.0", "maktaba:plugin/media@1.0.0"]), &s(&["run"]), &s(&["media.read"]))
            .unwrap();
        let e = audit(&s(&["maktaba:plugin/compute@1.1.0"]), &s(&["run"]), &[]).unwrap_err();
        assert_eq!(e.code, CODE_UNDECLARED_IMPORT);
        audit(&s(&["maktaba:plugin/compute@1.1.0"]), &s(&["run"]), &s(&["compute.llm"])).unwrap();
        audit(&s(&["maktaba:plugin/compute@1.1.0"]), &s(&["run"]), &s(&["compute.embed"])).unwrap();
    }

    #[test]
    fn component_magic() {
        assert!(is_component(b"\0asm\x0d\x00\x01\x00"));
        assert!(!is_component(b"\0asm\x01\x00\x00\x00"));
    }
}
