// The app's commands are declared three times: build.rs (each one a permission,
// `allow-<command>`), lib.rs's invoke handler (what runs), and the capabilities (which window
// may call which). A command in one list only is a permission that grants nothing, or a
// command nothing may call. And the tray's panel (story 13.12) is a window of its own, which
// may call its three commands and nothing else.
use std::collections::BTreeSet;
use std::path::{Path, PathBuf};

fn root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).to_path_buf()
}

/// The file's text, its line ends as `\n` (a Windows checkout has `\r\n`).
fn read(p: &Path) -> String {
    std::fs::read_to_string(p)
        .unwrap_or_else(|e| panic!("{}: {e}", p.display()))
        .replace("\r\n", "\n")
}

/// build.rs's COMMANDS.
fn declared() -> BTreeSet<String> {
    let build = read(&root().join("build.rs"));
    let start = build.find("const COMMANDS").expect("COMMANDS in build.rs");
    let list = &build[start..];
    let list = &list[..list.find("];").expect("the list's end")];
    list.lines()
        // a comment, whole-line or after the code, names no command
        .map(|l| l.split("//").next().unwrap_or(""))
        .flat_map(|l| l.split('"').skip(1).step_by(2).map(str::to_string))
        .collect()
}

/// lib.rs's generate_handler!: each command's name (its last path segment).
fn registered() -> BTreeSet<String> {
    let lib = read(&root().join("src/lib.rs"));
    let start = lib
        .find("generate_handler![")
        .expect("the invoke handler in lib.rs");
    let list = &lib[start + "generate_handler![".len()..];
    let list = &list[..list.find(']').expect("the handler's end")];
    list.split(',')
        .map(|s| s.trim())
        .filter(|s| !s.is_empty())
        .map(|s| s.rsplit("::").next().unwrap_or(s).to_string())
        .collect()
}

fn capability(name: &str) -> serde_json::Value {
    serde_json::from_str(&read(&root().join("capabilities").join(name))).expect("a capability")
}

/// Every capability file's name.
fn capabilities() -> Vec<String> {
    let mut out: Vec<String> = std::fs::read_dir(root().join("capabilities"))
        .expect("capabilities/")
        .filter_map(|e| e.ok()?.file_name().into_string().ok())
        .filter(|n| n.ends_with(".json"))
        .collect();
    out.sort();
    out
}

fn permissions(cap: &serde_json::Value) -> BTreeSet<String> {
    cap["permissions"]
        .as_array()
        .expect("permissions")
        .iter()
        .filter_map(|p| p.as_str().map(str::to_string))
        .collect()
}

#[test]
fn every_declared_command_runs_and_every_running_command_is_declared() {
    let (d, r) = (declared(), registered());
    assert!(d.len() >= 20, "build.rs was read: {d:?}");
    assert_eq!(
        d, r,
        "build.rs's COMMANDS and lib.rs's invoke handler differ"
    );
}

#[test]
fn every_app_permission_a_capability_grants_is_a_declared_command() {
    let d = declared();
    for name in capabilities() {
        for p in permissions(&capability(&name)) {
            if let Some(cmd) = p.strip_prefix("allow-") {
                assert!(
                    d.contains(&cmd.replace('-', "_")),
                    "{name} grants {p}, which build.rs does not declare"
                );
            }
        }
    }
}

#[test]
fn maktaba_s_dialog_may_close_itself_open_the_logs_and_be_dragged_nothing_else() {
    let cap = capability("shell-dialog.json");
    assert_eq!(cap["windows"], serde_json::json!(["shell-dialog"]));
    assert!(cap.get("remote").is_none(), "the app's own pages only");
    let want: BTreeSet<String> = [
        "core:event:allow-listen",
        "core:event:allow-unlisten",
        "core:window:allow-start-dragging",
        "allow-shell-dialog-choose",
    ]
    .into_iter()
    .map(String::from)
    .collect();
    assert_eq!(permissions(&cap), want);
}

#[test]
fn the_tray_panel_may_call_its_own_commands_and_nothing_else() {
    let cap = capability("tray-panel.json");
    assert_eq!(cap["windows"], serde_json::json!(["tray-panel"]));
    assert!(cap.get("remote").is_none(), "the app's own pages only");
    let want: BTreeSet<String> = [
        "core:event:allow-listen",
        "core:event:allow-unlisten",
        "allow-tray-panel-state",
        "allow-tray-panel-choose",
        "allow-tray-panel-hide",
    ]
    .into_iter()
    .map(String::from)
    .collect();
    assert_eq!(permissions(&cap), want);
    // and no other capability gives the panel's or the dialog's window anything, by name or by
    // a pattern: each is named by its own capability only
    for name in capabilities() {
        let windows = capability(&name)["windows"].clone();
        for w in windows.as_array().expect("windows") {
            let w = w.as_str().unwrap_or_default();
            assert!(!w.contains('*'), "{name} names windows by a pattern: {w:?}");
            for (own, file) in [
                ("tray-panel", "tray-panel.json"),
                ("shell-dialog", "shell-dialog.json"),
            ] {
                assert!(
                    w != own || name == file,
                    "{name} gives the window {w:?} its permissions"
                );
            }
        }
    }
}
