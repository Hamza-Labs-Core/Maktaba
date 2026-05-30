// Tauri 2 desktop entry point (Stories 13.1 / 13.2 / 13.3).
//
// All native wiring (menu bar + handler, tray, close-to-tray, mDNS
// discovery, updater) lives in `lib.rs` so the mobile shim can reuse
// it. This binary is a thin trampoline; keep it import-free to avoid
// the dead-import drift the gap audit flagged.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    maktaba_desktop_lib::run();
}

pub mod _docs {
    //! Module-level docs go here so `cargo doc` has somewhere to land
    //! without polluting the binary entry point.
}
