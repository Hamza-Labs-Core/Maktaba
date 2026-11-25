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
        "allow-shell-dialog-content",
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

/// The Rust source of `name`, with its comments (which may name anything) left out.
fn code(name: &str) -> String {
    read(&root().join("src").join(name))
        .lines()
        .filter(|l| !l.trim_start().starts_with("//"))
        .collect::<Vec<_>>()
        .join("\n")
}

/// The modules that make a window of their own (the main one is tauri.conf.json's).
fn window_makers() -> Vec<String> {
    let mut out: Vec<String> = std::fs::read_dir(root().join("src"))
        .expect("src/")
        .filter_map(|e| e.ok()?.file_name().into_string().ok())
        .filter(|n| n.ends_with(".rs") && code(n).contains("WebviewWindowBuilder::new("))
        .collect();
    out.sort();
    out
}

/// The window-state plugin shows a window it has no state of as it is made, and gives one it
/// has a state of its physical size back: no window of the shell's own is left to it.
#[test]
fn the_window_state_plugin_leaves_the_shell_s_own_windows_alone() {
    let lib = code("lib.rs");
    let start = lib
        .find("const UNSAVED_WINDOWS")
        .expect("UNSAVED_WINDOWS in lib.rs");
    let list = &lib[start..start + lib[start..].find("];").expect("its end")];
    let makers = window_makers();
    for file in ["traypanel.rs", "shelldialog.rs"] {
        assert!(makers.contains(&file.to_string()), "{makers:?}");
    }
    for file in makers {
        let module = file.trim_end_matches(".rs");
        assert!(
            list.contains(&format!("{module}::")),
            "{file} makes a window whose state would be kept: {list}"
        );
    }
    let builder = &lib[lib
        .find("tauri_plugin_window_state::Builder")
        .expect("the window-state plugin")..];
    let builder = &builder[..builder.find(';').expect("its statement")];
    assert!(
        builder.contains(".with_denylist(&UNSAVED_WINDOWS)"),
        "{builder}"
    );
}

/// A window is made with the app's menu bar: on Linux it is drawn inside the window, on Windows
/// its keys act there. And the panel outlives Alt+F4 and Command-W.
#[test]
fn the_shell_s_own_windows_have_no_menu_bar_and_the_panel_is_never_closed() {
    for file in window_makers() {
        let src = code(&file);
        assert!(
            src.contains(".remove_menu()") || src.contains(".hide_menu()"),
            "{file} leaves the app's menu bar on its window"
        );
    }
    let panel = code("traypanel.rs");
    let close = &panel[panel
        .find("WindowEvent::CloseRequested")
        .expect("the panel's close")..];
    assert!(close[..close.find('}').unwrap_or(0) + 120].contains("prevent_close()"));
}

/// An event sent to one window still reaches any page that listens for every window's: the
/// panel's and the dialog's carry no words.
#[test]
fn the_panel_s_and_the_dialog_s_events_carry_nothing() {
    let panel = code("traypanel.rs") + &code("shelldialog.rs");
    let sent: Vec<&str> = panel
        .match_indices("emit_to(")
        .map(|(i, _)| &panel[i..])
        .collect();
    assert!(sent.len() >= 3);
    for s in sent {
        let call = &s[..s.find(';').expect("the call's end")];
        assert!(call.trim_end().ends_with(", ())"), "{call}");
    }
}
