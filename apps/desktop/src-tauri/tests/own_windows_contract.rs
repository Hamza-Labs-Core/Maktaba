// Story 13.13: the setup and status windows are a contract across four files. Each command
// their pages call is declared (build.rs COMMANDS, which makes it a permission), registered
// (src/lib.rs, generate_handler!), granted to its own window only (capabilities/), and named
// the same in the page's module (web/src/lib/statusWindow.ts, desktopSetup.ts). A command
// missing from one of them is a button that does nothing, or one any window may press. This
// test reads them all.
use std::collections::BTreeSet;
use std::path::{Path, PathBuf};

use serde_json::Value;

fn root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).to_path_buf()
}

fn read(p: &Path) -> String {
    std::fs::read_to_string(p)
        .unwrap_or_else(|e| panic!("{}: {e}", p.display()))
        .replace("\r\n", "\n")
}

/// Every double-quoted string in `text`.
fn quoted(text: &str) -> BTreeSet<String> {
    text.split('"')
        .skip(1)
        .step_by(2)
        .map(str::to_string)
        .collect()
}

/// The commands build.rs declares.
fn declared() -> BTreeSet<String> {
    let src = read(&root().join("build.rs"));
    let start = src.find("const COMMANDS").expect("COMMANDS in build.rs");
    let end = start + src[start..].find("];").expect("its end");
    let body: String = src[start..end]
        .lines()
        .filter(|l| !l.trim_start().starts_with("//"))
        .collect::<Vec<_>>()
        .join("\n");
    quoted(&body)
}

/// The commands src/lib.rs registers (the last path segment of each handler).
fn registered() -> BTreeSet<String> {
    let src = read(&root().join("src/lib.rs"));
    let start = src
        .find("tauri::generate_handler![")
        .expect("generate_handler! in lib.rs");
    let end = start + src[start..].find("])").expect("its end");
    src[start + "tauri::generate_handler![".len()..end]
        .split(',')
        .filter_map(|s| s.trim().rsplit("::").next().map(str::to_string))
        .filter(|s| !s.is_empty())
        .collect()
}

fn capability(name: &str) -> Value {
    serde_json::from_str(&read(&root().join("capabilities").join(name)))
        .unwrap_or_else(|e| panic!("{name}: {e}"))
}

fn windows(cap: &Value) -> Vec<String> {
    cap["windows"]
        .as_array()
        .expect("windows")
        .iter()
        .map(|w| w.as_str().unwrap_or_default().to_string())
        .collect()
}

/// The app's own commands a capability grants (`allow-<command>`, dashes back to underscores).
fn granted(cap: &Value) -> BTreeSet<String> {
    cap["permissions"]
        .as_array()
        .expect("permissions")
        .iter()
        .filter_map(|p| p.as_str())
        .filter_map(|p| p.strip_prefix("allow-"))
        .map(|c| c.replace('-', "_"))
        .collect()
}

#[test]
fn the_status_window_s_commands_are_declared_registered_and_its_own() {
    let declared = declared();
    let registered = registered();
    for cmd in ["status_window_state", "status_window_action"] {
        assert!(declared.contains(cmd), "build.rs does not declare {cmd}");
        assert!(registered.contains(cmd), "lib.rs does not register {cmd}");
    }
    let status = capability("status-window.json");
    assert_eq!(windows(&status), ["status"]);
    let mine = granted(&status);
    assert!(mine.contains("status_window_state") && mine.contains("status_window_action"));
    // nothing of the setup's, the main window's files or its menu
    for not in [
        "setup_step",
        "finish_setup",
        "use_remote_server",
        "frame_menu",
    ] {
        assert!(!mine.contains(not), "the status window may not {not}");
    }
    // and no other window gets the status window's commands
    for other in ["default.json", "local-server.json", "setup-window.json"] {
        let theirs = granted(&capability(other));
        assert!(
            !theirs.contains("status_window_action"),
            "{other} grants the status window's buttons"
        );
    }
}

#[test]
fn the_setup_window_gets_the_setup_s_commands_and_draws_its_frame() {
    let setup = capability("setup-window.json");
    assert_eq!(windows(&setup), ["setup"]);
    let mine = granted(&setup);
    for cmd in [
        "app_state",
        "setup_step",
        "finish_setup",
        "open_local_server",
        "use_remote_server",
        "frame_claim",
        "frame_action",
    ] {
        assert!(mine.contains(cmd), "the setup window cannot {cmd}");
    }
    // the menu bar's items are the main window's
    assert!(!mine.contains("frame_menu"));
}

#[test]
fn every_command_a_window_is_granted_is_declared_and_registered() {
    let declared = declared();
    let registered = registered();
    for name in ["setup-window.json", "status-window.json"] {
        for cmd in granted(&capability(name)) {
            assert!(declared.contains(&cmd), "{name}: {cmd} is not in build.rs");
            assert!(registered.contains(&cmd), "{name}: {cmd} is not in lib.rs");
        }
    }
}

#[test]
fn the_pages_call_the_commands_by_the_shell_s_names() {
    let ts = read(&root().join("../../../web/src/lib/statusWindow.ts"));
    let names = quoted(&ts);
    for cmd in ["status_window_state", "status_window_action"] {
        assert!(names.contains(cmd), "statusWindow.ts does not call {cmd}");
    }
    // the event the shell sends (ownwindows::STATUS_EVENT) and the page's mark
    assert!(names.contains("status-window"));
    let rs = read(&root().join("src/ownwindows.rs"));
    assert!(rs.contains(r#"pub const STATUS_EVENT: &str = "status-window";"#));
    let mark = read(&root().join("../../../web/src/lib/ownWindow.ts"));
    assert!(mark.contains("__MAKTABA_PANEL__"));
    assert!(rs.contains("__MAKTABA_PANEL__"));
}
