//! The import audit (story 54.02 §6): crates/audit, shared with the store's checks (54.16
//! C4), and the names Wasmtime reads from a compiled component.

pub use maktaba_plugin_audit::*;

/// The import and export names of a compiled component.
pub fn names(engine: &wasmtime::Engine, c: &wasmtime::component::Component) -> (Vec<String>, Vec<String>) {
    let ty = c.component_type();
    let imports = ty.imports(engine).map(|(n, _)| n.to_string()).collect();
    let exports = ty.exports(engine).map(|(n, _)| n.to_string()).collect();
    (imports, exports)
}
