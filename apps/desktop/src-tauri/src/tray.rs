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
    let mut out = Vec::new();
    if server {
        out.push(note(status_line(status)));
        if update_ready {
            out.push(item("update", "Update ready: restart to install"));
        }
        out.push(Entry::Separator);
    }
    out.push(item("show", "Open Maktaba"));
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
            ["show", "media-play", "media-pause", "tray-settings", "quit"]
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
}
