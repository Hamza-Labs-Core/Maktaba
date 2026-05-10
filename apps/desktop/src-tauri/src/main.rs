// Tauri 2 desktop entry point (Stories 13.1 / 13.2 / 13.3).
//
// Wires:
//   - native menu bar (Story 13.4 prep)
//   - file dialog plugin (Story 13.6 drag-drop / open file)
//   - window state plugin (Story 13.1 AC: persist window position)
//   - updater plugin (Story 13.8)
//
// The frontend is the shared web/dist bundle; everything below is the
// native shell.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem, Submenu},
    tray::{TrayIconBuilder, TrayIconEvent},
    Manager, WindowEvent,
};

fn main() {
    maktaba_desktop_lib::run();
}

pub mod _docs {
    //! Module-level docs go here so `cargo doc` has somewhere to land
    //! without polluting the binary entry point.
}
