// The menu-bar (tray) menu as data (Epic 36, story 36.5).
//
// `entries` decides what the menu shows for the app's mode and the
// server's status; lib.rs turns the list into a tauri Menu and rebuilds it
// only when the list changes. Keeping the decision pure keeps it testable
// without a running app.
//
// The ids are matched in lib.rs `on_tray_event`; `media-play`,
// `media-pause` and `preferences` are also forwarded to the web shell
// (web/src/lib/desktop.ts KNOWN_MENU_IDS).
use crate::status::ServerStatus;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Entry {
    Item {
        id: &'static str,
        label: String,
        enabled: bool,
    },
    Separator,
}

fn item(id: &'static str, label: impl Into<String>) -> Entry {
    Entry::Item {
        id,
        label: label.into(),
        enabled: true,
    }
}

fn note(label: impl Into<String>) -> Entry {
    Entry::Item {
        id: "status",
        label: label.into(),
        enabled: false,
    }
}

/// The one-line status shown at the top of the menu and as the tooltip.
pub fn status_line(status: &ServerStatus) -> String {
    match status {
        ServerStatus::SetupNeeded => "Maktaba: finish setup to start the server".into(),
        ServerStatus::Paused => "Maktaba is paused".into(),
        ServerStatus::Stopped => "Maktaba is stopped".into(),
        ServerStatus::Starting => "Maktaba is starting…".into(),
        ServerStatus::Running => "Maktaba is running".into(),
        ServerStatus::Problem(roles) => format!("Maktaba: {} keeps stopping", roles.join(", ")),
    }
}

/// The tray menu. `server` is false in client mode (the window connects
/// to another Maktaba and this Mac runs no server): then the menu has no
/// server items and Quit is a plain Quit.
pub fn entries(server: bool, status: &ServerStatus, update_ready: bool) -> Vec<Entry> {
    entries_with_model(server, status, update_ready, None)
}

/// The tray menu with the language model's line (status::model_line) under
/// the server's status: ready, or downloading with its progress.
pub fn entries_with_model(
    server: bool,
    status: &ServerStatus,
    update_ready: bool,
    model: Option<&str>,
) -> Vec<Entry> {
    let mut out = Vec::new();
    if server {
        out.push(note(status_line(status)));
        if let Some(line) = model {
            out.push(Entry::Item {
                id: "model-status",
                label: line.to_string(),
                enabled: false,
            });
        }
        if update_ready {
            out.push(item("update", "Update ready: restart to install"));
        }
        out.push(Entry::Separator);
    }
    out.push(item("show", "Open Maktaba"));
    if !server {
        // client mode: the way back to the setup screens (another address,
        // or this Mac when the build carries the server)
        out.push(item("change-server", "Connect to another Maktaba…"));
    }
    if server {
        match status {
            ServerStatus::SetupNeeded => {}
            ServerStatus::Paused | ServerStatus::Stopped => {
                out.push(item("server-resume", "Resume server"))
            }
            _ => out.push(item("server-pause", "Pause server")),
        }
        out.push(item("open-logs", "Open logs"));
    }
    out.push(Entry::Separator);
    out.push(item("media-play", "Play"));
    out.push(item("media-pause", "Pause playback"));
    out.push(Entry::Separator);
    out.push(item("tray-settings", "Settings"));
    out.push(Entry::Separator);
    if server && *status != ServerStatus::SetupNeeded {
        out.push(item("quit", "Quit (server keeps running)"));
        out.push(item("stop-and-quit", "Stop server and quit"));
    } else {
        out.push(item("quit", "Quit"));
    }
    out
}

/// The window menu's Server submenu (Linux without a tray, Epic 39 story
/// 39.8; lib.rs builds it, shell.rs keeps it in step): the tray's server
/// items under ids prefixed with WINDOW_PREFIX. Every menu's events reach
/// both the tray's and the app's handler, so the ids must differ.
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub const WINDOW_PREFIX: &str = "window-";
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub const WINDOW_MENU_ID: &str = "window-server";
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub const WINDOW_STATUS_ID: &str = "window-status";

/// The submenu's items as (tray id, label), in order; None is a separator.
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub const WINDOW_ITEMS: &[Option<(&str, &str)>] = &[
    Some(("update", "Install update and restart")),
    None,
    Some(("server-pause", "Pause server")),
    Some(("server-resume", "Resume server")),
    Some(("open-logs", "Open logs")),
    None,
    Some(("stop-and-quit", "Stop server and quit")),
];

/// A window item is enabled when the tray menu would offer it.
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub fn window_enabled(entries: &[Entry], id: &str) -> bool {
    entries
        .iter()
        .any(|e| matches!(e, Entry::Item { id: i, enabled: true, .. } if *i == id))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ids(e: &[Entry]) -> Vec<&'static str> {
        e.iter()
            .filter_map(|x| match x {
                Entry::Item { id, .. } => Some(*id),
                Entry::Separator => None,
            })
            .collect()
    }

    #[test]
    fn client_mode_has_no_server_items() {
        let e = entries(false, &ServerStatus::Stopped, true);
        assert_eq!(
            ids(&e),
            [
                "show",
                "change-server",
                "media-play",
                "media-pause",
                "tray-settings",
                "quit"
            ]
        );
    }

    #[test]
    fn running_server_offers_pause_and_both_quits() {
        let e = entries(true, &ServerStatus::Running, false);
        assert_eq!(
            ids(&e),
            [
                "status",
                "show",
                "server-pause",
                "open-logs",
                "media-play",
                "media-pause",
                "tray-settings",
                "quit",
                "stop-and-quit"
            ]
        );
        assert_eq!(
            e[0],
            Entry::Item {
                id: "status",
                label: "Maktaba is running".into(),
                enabled: false
            }
        );
    }

    #[test]
    fn stopped_or_paused_server_offers_resume() {
        for s in [ServerStatus::Stopped, ServerStatus::Paused] {
            let e = entries(true, &s, false);
            assert!(ids(&e).contains(&"server-resume"));
            assert!(!ids(&e).contains(&"server-pause"));
        }
    }

    #[test]
    fn starting_and_problem_can_still_be_paused() {
        for s in [
            ServerStatus::Starting,
            ServerStatus::Problem(vec!["pipeline".into()]),
        ] {
            assert!(ids(&entries(true, &s, false)).contains(&"server-pause"));
        }
    }

    #[test]
    fn setup_needed_has_no_pause_or_stop() {
        let e = entries(true, &ServerStatus::SetupNeeded, false);
        let got = ids(&e);
        assert!(!got.contains(&"server-pause") && !got.contains(&"server-resume"));
        assert!(!got.contains(&"stop-and-quit"));
        assert!(e.contains(&Entry::Item {
            id: "quit",
            label: "Quit".into(),
            enabled: true
        }));
    }

    #[test]
    fn the_language_model_line_sits_under_the_status() {
        let line = "Language model: downloading qwen2.5:7b (42%)";
        let e = entries_with_model(true, &ServerStatus::Running, true, Some(line));
        assert_eq!(ids(&e)[..3], ["status", "model-status", "update"]);
        assert_eq!(
            e[1],
            Entry::Item {
                id: "model-status",
                label: line.into(),
                enabled: false
            }
        );
        // client mode shows no server lines
        let e = entries_with_model(false, &ServerStatus::Stopped, false, Some(line));
        assert!(!ids(&e).contains(&"model-status"));
    }

    #[test]
    fn update_ready_adds_the_install_item_under_the_status() {
        let e = entries(true, &ServerStatus::Running, true);
        assert_eq!(ids(&e)[..2], ["status", "update"]);
    }

    #[test]
    fn status_lines() {
        assert_eq!(status_line(&ServerStatus::Starting), "Maktaba is starting…");
        assert_eq!(
            status_line(&ServerStatus::Problem(vec![
                "pipeline".into(),
                "streaming".into()
            ])),
            "Maktaba: pipeline, streaming keeps stopping"
        );
    }

    #[test]
    fn the_window_menu_offers_what_the_tray_offers() {
        let window_ids: Vec<&str> = WINDOW_ITEMS.iter().flatten().map(|(id, _)| *id).collect();
        assert_eq!(
            window_ids,
            [
                "update",
                "server-pause",
                "server-resume",
                "open-logs",
                "stop-and-quit"
            ]
        );
        let running = entries(true, &ServerStatus::Running, false);
        assert!(
            window_enabled(&running, "server-pause") && window_enabled(&running, "stop-and-quit")
        );
        assert!(!window_enabled(&running, "server-resume") && !window_enabled(&running, "update"));
        let paused = entries(true, &ServerStatus::Paused, true);
        assert!(window_enabled(&paused, "server-resume") && window_enabled(&paused, "update"));
        assert!(!window_enabled(&paused, "server-pause"));
        // client mode: no server items at all
        let client = entries(false, &ServerStatus::SetupNeeded, false);
        assert!(window_ids.iter().all(|id| !window_enabled(&client, id)));
        // the disabled status line is never "offered"
        assert!(!window_enabled(&running, "status"));
        // the window's ids never collide with the tray's (both handlers see every event)
        for id in &window_ids {
            assert!(!ids(&running).contains(&format!("{WINDOW_PREFIX}{id}").as_str()));
        }
    }
}
