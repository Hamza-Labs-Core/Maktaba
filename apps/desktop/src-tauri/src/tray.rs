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
/// `detail` is what the server waits on (status::waiting_on): the venv being
/// built, the backup before an update, or why that failed. A server that
/// does not answer has no processes to wait on: its reason (from server.log,
/// cut to fit the tooltip) is the line.
pub fn status_line(status: &ServerStatus, detail: Option<&str>) -> String {
    match (status, detail) {
        (ServerStatus::SetupNeeded, _) => "Maktaba: finish setup to start the server".into(),
        (ServerStatus::Paused, _) => "Maktaba is paused".into(),
        (ServerStatus::Stopped, _) => "Maktaba is stopped".into(),
        (ServerStatus::Starting, Some(d)) => format!("Maktaba is starting: {d}"),
        (ServerStatus::Starting, None) => "Maktaba is starting…".into(),
        (ServerStatus::Running, _) => "Maktaba is running".into(),
        (ServerStatus::Problem { roles }, Some(d)) => {
            format!("Maktaba: {} cannot start ({d})", roles.join(", "))
        }
        (ServerStatus::Problem { roles }, None) => {
            format!("Maktaba: {} keeps stopping", roles.join(", "))
        }
        (ServerStatus::NotAnswering { reason }, _) => format!("Maktaba has a problem: {reason}"),
    }
}

/// The longest tooltip Windows shows (NOTIFYICONDATA's 128 UTF-16 units,
/// with its terminator).
const TOOLTIP_MAX: usize = 127;

/// The tooltip: the status line, shortened with an ellipsis to what the
/// tray can show.
pub fn tooltip(line: &str) -> String {
    if line.encode_utf16().count() <= TOOLTIP_MAX {
        return line.to_string();
    }
    let mut out = String::new();
    let mut units = 0;
    for c in line.chars() {
        units += c.len_utf16();
        if units > TOOLTIP_MAX - 1 {
            break;
        }
        out.push(c);
    }
    out.push('…');
    out
}

/// The status badge on the tray icon (Windows and Linux; the Mac's menu-bar
/// item keeps its plain icon): green running, amber starting, red a
/// problem (processes that keep failing, or a server whose agent runs but
/// that does not answer: the tray says "Maktaba has a problem" for both),
/// grey paused or stopped, blue an update ready; none before setup.
pub fn badge_color(status: &ServerStatus, update_ready: bool) -> Option<[u8; 3]> {
    match status {
        ServerStatus::SetupNeeded => None,
        _ if update_ready => Some([0x2f, 0x6f, 0xd6]),
        ServerStatus::Running => Some([0x2e, 0x9d, 0x4f]),
        ServerStatus::Starting => Some([0xe0, 0xa1, 0x00]),
        ServerStatus::Problem { .. } | ServerStatus::NotAnswering { .. } => {
            Some([0xd3, 0x3a, 0x2c])
        }
        ServerStatus::Paused | ServerStatus::Stopped => Some([0x8a, 0x8a, 0x8a]),
    }
}

/// The icon (RGBA, `width` × `height`) with a round badge of `color` in its
/// bottom-right corner, ringed in white so it reads on a dark or light
/// taskbar, its edge anti-aliased. Drawn at run time: no icon file per state.
pub fn badge(rgba: &[u8], width: u32, height: u32, color: [u8; 3]) -> Vec<u8> {
    let mut out = rgba.to_vec();
    let (w, h) = (width as f32, height as f32);
    let r = (w.min(h) * 0.22).max(2.0);
    let ring = (r / 4.0).max(1.0);
    let (cx, cy) = (w - r - ring, h - r - ring);
    let px = |x: u32, y: u32| ((y * width + x) * 4) as usize;
    for y in 0..height {
        for x in 0..width {
            let d = ((x as f32 + 0.5 - cx).powi(2) + (y as f32 + 0.5 - cy).powi(2)).sqrt();
            // coverage of the badge (with its ring) and of its colored disc
            let outer = (r + ring + 0.5 - d).clamp(0.0, 1.0);
            if outer == 0.0 {
                continue;
            }
            let inner = (r + 0.5 - d).clamp(0.0, 1.0);
            let i = px(x, y);
            if i + 3 >= out.len() {
                continue;
            }
            for (k, c) in color.iter().enumerate() {
                let fill = *c as f32 * inner + 255.0 * (1.0 - inner);
                out[i + k] = (fill * outer + out[i + k] as f32 * (1.0 - outer)).round() as u8;
            }
            out[i + 3] = (255.0 * outer + out[i + 3] as f32 * (1.0 - outer)).round() as u8;
        }
    }
    out
}

/// The tray menu. `server` is false in client mode (the window connects
/// to another Maktaba and this Mac runs no server): then the menu has no
/// server items and Quit is a plain Quit.
pub fn entries(server: bool, status: &ServerStatus, update_ready: bool) -> Vec<Entry> {
    entries_with_model(server, status, update_ready, None, None)
}

/// The tray menu with what the server waits on in its status line (`detail`,
/// status::waiting_on: the venv being built, the backup before an update)
/// and the language model's line (status::model_line) under it: ready, or
/// downloading with its progress.
pub fn entries_with_model(
    server: bool,
    status: &ServerStatus,
    update_ready: bool,
    model: Option<&str>,
    detail: Option<&str>,
) -> Vec<Entry> {
    let mut out = Vec::new();
    if server {
        out.push(note(status_line(status, detail)));
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
            ServerStatus::Problem {
                roles: vec!["pipeline".into()],
            },
            ServerStatus::NotAnswering {
                reason: "the server does not answer".into(),
            },
        ] {
            let got = ids(&entries(true, &s, false));
            assert!(got.contains(&"server-pause") && got.contains(&"open-logs"));
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
        let e = entries_with_model(true, &ServerStatus::Running, true, Some(line), None);
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
        let e = entries_with_model(false, &ServerStatus::Stopped, false, Some(line), None);
        assert!(!ids(&e).contains(&"model-status"));
    }

    #[test]
    fn update_ready_adds_the_install_item_under_the_status() {
        let e = entries(true, &ServerStatus::Running, true);
        assert_eq!(ids(&e)[..2], ["status", "update"]);
    }

    #[test]
    fn status_lines() {
        assert_eq!(
            status_line(&ServerStatus::Starting, None),
            "Maktaba is starting…"
        );
        assert_eq!(
            status_line(
                &ServerStatus::Problem {
                    roles: vec!["pipeline".into(), "streaming".into()]
                },
                None
            ),
            "Maktaba: pipeline, streaming keeps stopping"
        );
        assert_eq!(
            status_line(
                &ServerStatus::NotAnswering {
                    reason: "another launcher runs this install".into()
                },
                None
            ),
            "Maktaba has a problem: another launcher runs this install"
        );
    }

    #[test]
    fn status_lines_say_what_the_server_waits_on() {
        let d = Some("Installing the transcription tools (40%)");
        assert_eq!(
            status_line(&ServerStatus::Starting, d),
            "Maktaba is starting: Installing the transcription tools (40%)"
        );
        assert_eq!(
            status_line(
                &ServerStatus::Problem {
                    roles: vec!["api".into()]
                },
                Some("the backup before the update failed")
            ),
            "Maktaba: api cannot start (the backup before the update failed)"
        );
        assert_eq!(status_line(&ServerStatus::Running, d), "Maktaba is running");
        let e = entries_with_model(true, &ServerStatus::Starting, false, None, d);
        assert_eq!(
            e[0],
            Entry::Item {
                id: "status",
                label: "Maktaba is starting: Installing the transcription tools (40%)".into(),
                enabled: false
            }
        );
    }

    #[test]
    fn a_server_that_does_not_answer_says_why_in_the_menu_and_the_tooltip() {
        let s = ServerStatus::NotAnswering {
            reason: "another launcher runs this install".into(),
        };
        // it has no processes to wait on: the reason is the line, whatever the detail
        assert_eq!(
            status_line(&s, Some("Installing")),
            "Maktaba has a problem: another launcher runs this install"
        );
        let e = entries_with_model(true, &s, false, None, None);
        assert_eq!(
            e[0],
            Entry::Item {
                id: "status",
                label: "Maktaba has a problem: another launcher runs this install".into(),
                enabled: false
            }
        );
        assert!(ids(&e).contains(&"server-pause") && ids(&e).contains(&"open-logs"));
        // the longest reason the log gives fits the tooltip whole, in any script
        for word in ["x", "مكتبة"] {
            let log = format!("Error: {}", [word; 300].join(" "));
            let reason = crate::status::failure_in_log(&log).expect("a reason");
            let line = status_line(&ServerStatus::NotAnswering { reason }, None);
            assert_eq!(tooltip(&line), line, "cut twice: {line}");
            assert!(line.encode_utf16().count() <= TOOLTIP_MAX);
        }
    }

    #[test]
    fn tooltips_fit_the_tray() {
        assert_eq!(tooltip("Maktaba is running"), "Maktaba is running");
        let long = format!("Maktaba is starting: {}", "مكتبة ".repeat(40));
        let t = tooltip(&long);
        assert!(t.encode_utf16().count() <= TOOLTIP_MAX, "{t}");
        assert!(t.ends_with('…') && long.starts_with(t.trim_end_matches('…')));
    }

    #[test]
    fn badge_colors_follow_the_status() {
        assert_eq!(badge_color(&ServerStatus::SetupNeeded, true), None);
        let running = badge_color(&ServerStatus::Running, false);
        let starting = badge_color(&ServerStatus::Starting, false);
        let problem = badge_color(
            &ServerStatus::Problem {
                roles: vec!["api".into()],
            },
            false,
        );
        let not_answering = ServerStatus::NotAnswering {
            reason: "the server does not answer".into(),
        };
        assert_eq!(
            badge_color(&not_answering, false),
            problem,
            "a server that does not answer is a problem"
        );
        assert_eq!(
            badge_color(&not_answering, true),
            badge_color(&ServerStatus::Running, true),
            "an update ready shows over it, as over a problem"
        );
        let stopped = badge_color(&ServerStatus::Stopped, false);
        assert_eq!(stopped, badge_color(&ServerStatus::Paused, false));
        let update = badge_color(&ServerStatus::Running, true);
        let all = [running, starting, problem, stopped, update];
        for (i, a) in all.iter().enumerate() {
            assert!(a.is_some());
            for b in &all[i + 1..] {
                assert_ne!(a, b, "two states share a badge");
            }
        }
    }

    #[test]
    fn the_badge_is_drawn_in_the_bottom_right_corner() {
        let (w, h) = (32u32, 32u32);
        let icon: Vec<u8> = (0..w * h).flat_map(|_| [10u8, 20, 30, 255]).collect();
        let out = badge(&icon, w, h, [200, 0, 0]);
        assert_eq!(out.len(), icon.len());
        let at = |x: u32, y: u32| {
            let i = ((y * w + x) * 4) as usize;
            [out[i], out[i + 1], out[i + 2], out[i + 3]]
        };
        assert_eq!(
            at(0, 0),
            [10, 20, 30, 255],
            "the icon is untouched away from the badge"
        );
        assert_eq!(at(w / 2, h / 2), [10, 20, 30, 255]);
        // the disc's middle is the color, its rim white
        let r = 32.0f32 * 0.22;
        let ring = (r / 4.0).max(1.0);
        let c = (32.0 - r - ring) as u32;
        assert_eq!(at(c, c), [200, 0, 0, 255]);
        let rim = (32.0 - ring / 2.0 - 0.5) as u32;
        let p = at(c, rim.min(h - 1));
        assert!(p[0] > 200 && p[1] > 150 && p[2] > 150, "rim {p:?}");
        // a transparent icon gets an opaque badge
        let clear = vec![0u8; (w * h * 4) as usize];
        let out = badge(&clear, w, h, [0, 0, 200]);
        let i = ((c * w + c) * 4) as usize;
        assert_eq!(&out[i..i + 4], &[0, 0, 200, 255]);
        assert_eq!(&out[..4], &[0, 0, 0, 0]);
    }
}
