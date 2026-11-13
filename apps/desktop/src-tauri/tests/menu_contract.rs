// The menu ids are a contract between two languages: the shell declares each
// custom menu item with an id (src/lib.rs and the modules it names), and the
// web app keeps the list it knows how to act on (web/src/lib/desktop.ts,
// KNOWN_MENU_IDS). An id on one side only is a menu item that does nothing, or
// an action nothing can send. This test reads both sources.
use std::collections::BTreeSet;
use std::path::{Path, PathBuf};

fn root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).to_path_buf()
}

fn read(p: &Path) -> String {
    std::fs::read_to_string(p).unwrap_or_else(|e| panic!("{}: {e}", p.display()))
}

/// Every double-quoted string in `text`.
fn quoted(text: &str) -> Vec<String> {
    text.split('"')
        .skip(1)
        .step_by(2)
        .map(str::to_string)
        .collect()
}

/// The ids the web app lists in KNOWN_MENU_IDS.
fn web_ids() -> BTreeSet<String> {
    let ts = read(&root().join("../../../web/src/lib/desktop.ts"));
    let start = ts
        .find("export const KNOWN_MENU_IDS = [")
        .expect("KNOWN_MENU_IDS is in desktop.ts");
    let list = &ts[start..];
    let end = list.find("] as const").expect("the list ends");
    // comments in the list name menu paths, not ids: drop them first
    let body: String = list[..end]
        .lines()
        .skip(1)
        .filter(|l| !l.trim_start().starts_with("//"))
        .collect::<Vec<_>>()
        .join("\n");
    quoted(&body).into_iter().collect()
}

/// Every string literal in the shell's sources.
fn shell_literals() -> BTreeSet<String> {
    let mut out = BTreeSet::new();
    for entry in std::fs::read_dir(root().join("src")).expect("src") {
        let path = entry.expect("an entry").path();
        if path.extension().is_some_and(|e| e == "rs") {
            out.extend(quoted(&read(&path)));
        }
    }
    out
}

#[test]
fn every_menu_id_the_web_app_knows_is_declared_by_the_shell() {
    let web = web_ids();
    assert!(web.len() >= 10, "the list was read: {web:?}");
    let shell = shell_literals();
    let missing: Vec<_> = web.iter().filter(|id| !shell.contains(*id)).collect();
    assert!(
        missing.is_empty(),
        "the web app lists menu ids the shell never declares: {missing:?}"
    );
}

#[test]
fn the_view_menu_has_the_way_into_big_screen_and_the_way_out() {
    let lib = read(&root().join("src/lib.rs"));
    let web = web_ids();
    for (constant, id, label) in [
        ("bigscreen::MENU_ID", "big-screen", "\"Big Screen\""),
        (
            "bigscreen::LEAVE_MENU_ID",
            "big-screen-leave",
            "\"Leave Big Screen\"",
        ),
    ] {
        assert!(
            lib.contains(constant),
            "{constant} is not a menu item in lib.rs"
        );
        assert!(lib.contains(label), "{label} is not a menu label in lib.rs");
        assert!(web.contains(id), "the web app does not know {id}");
    }
    // both are in the View menu's own list of items
    let view = &lib[lib.find("\"View\"").expect("the View menu")..];
    let items = &view[..view.find(")?;").expect("the menu's end")];
    assert!(items.contains("&big_screen,") && items.contains("&big_screen_leave,"));
}
