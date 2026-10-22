// The menu-bar (tray) menu as data (Epic 36, story 36.5).
//
// `entries` decides what the menu shows for the app's mode and the
// server's status; lib.rs turns the list into a tauri Menu and rebuilds it
// only when the list changes. Keeping the decision pure keeps it testable
// without a running app.
//
// The ids are matched in lib.rs `on_tray_event`; `media-play`,
// `media-pause` and `preferences` are also forwarded to the web shell
// (web/src/lib/desktop.ts KNOWN_MENU_IDS). The words are the system's UI
// language's, English or Arabic (lang.rs).
use crate::lang::{fill, Lang};
use crate::status::{ServerStatus, NO_ANSWER};

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
/// `detail` is what the server reports it waits on (status::waiting_on: the
/// venv being built, the backup before an update, or why that failed), in
/// its English: the English tray shows it, a tray in another language says
/// the status in its own words without it. A server that does not answer has
/// no processes to wait on: the line of server.log that says why (cut to fit
/// the tooltip), as the log has it, is the reason.
pub fn status_line(lang: Lang, status: &ServerStatus, detail: Option<&str>) -> String {
    let w = lang.words();
    match status {
        ServerStatus::SetupNeeded => w.setup_needed.into(),
        ServerStatus::Paused => w.paused.into(),
        ServerStatus::Stopped => w.stopped.into(),
        ServerStatus::Running => w.running.into(),
        ServerStatus::Starting => match (detail, w.starting_on) {
            (Some(d), Some(line)) => fill(line, &[("detail", d)]),
            _ => w.starting.into(),
        },
        ServerStatus::Problem { roles } => {
            let roles = roles.join(w.list_sep);
            match (detail, w.cannot_start) {
                (Some(d), Some(line)) => fill(line, &[("roles", &roles), ("detail", d)]),
                (Some(_), None) => fill(w.cannot_start_plain, &[("roles", &roles)]),
                (None, _) => fill(w.keeps_stopping, &[("roles", &roles)]),
            }
        }
        ServerStatus::NotAnswering { reason } => {
            let reason = if reason == NO_ANSWER {
                w.no_answer
            } else {
                reason
            };
            fill(w.has_a_problem, &[("reason", reason)])
        }
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

/// The badge of a problem: processes that keep failing, or a server whose
/// agent runs but that does not answer (the tray says "Maktaba has a problem"
/// for both).
const BADGE_PROBLEM: [u8; 3] = [0xd3, 0x3a, 0x2c];
/// The badge of an update ready: violet, far in hue from the icon's blue
/// (#1E5AD8, which a blue badge vanished into at 16 px) and from the other
/// badges.
const BADGE_UPDATE: [u8; 3] = [0xc7, 0x52, 0xe8];
const BADGE_RUNNING: [u8; 3] = [0x2e, 0x9d, 0x4f];
/// A deep amber: a light one (#E0A100) was 2:1 on a light taskbar.
const BADGE_STARTING: [u8; 3] = [0xb9, 0x7d, 0x00];
const BADGE_STOPPED: [u8; 3] = [0x8a, 0x8a, 0x8a];

/// The status badge on the tray icon (Windows and Linux; the Mac's menu-bar
/// item keeps its plain icon): red a problem, then violet an update ready,
/// else green running, amber starting, grey paused or stopped; none before
/// setup. A problem outranks the update: it is what needs the user first.
/// Every badge has 3:1 contrast or more on a light and on a dark taskbar.
pub fn badge_color(status: &ServerStatus, update_ready: bool) -> Option<[u8; 3]> {
    match status {
        ServerStatus::SetupNeeded => None,
        ServerStatus::Problem { .. } | ServerStatus::NotAnswering { .. } => Some(BADGE_PROBLEM),
        _ if update_ready => Some(BADGE_UPDATE),
        ServerStatus::Running => Some(BADGE_RUNNING),
        ServerStatus::Starting => Some(BADGE_STARTING),
        ServerStatus::Paused | ServerStatus::Stopped => Some(BADGE_STOPPED),
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

/// The error dialog's sentence when the tray item `id` failed, in `lang`.
pub fn action_failed(lang: Lang, id: &str) -> &'static str {
    let w = lang.words();
    match id {
        "server-pause" => w.pause_failed,
        "server-resume" => w.resume_failed,
        "stop-and-quit" => w.stop_failed,
        "open-logs" => w.logs_failed,
        "change-server" => w.setup_failed,
        _ => w.action_failed,
    }
}

/// The tray menu, in `lang`. `server` is false in client mode (the window
/// connects to another Maktaba and this Mac runs no server): then the menu
/// has no server items and Quit is a plain Quit.
pub fn entries(lang: Lang, server: bool, status: &ServerStatus, update_ready: bool) -> Vec<Entry> {
    entries_with_model(lang, server, status, update_ready, None, None)
}

/// The tray menu with what the server waits on in its status line (`detail`,
/// status::waiting_on: the venv being built, the backup before an update)
/// and the language model's line (status::model_line) under it: ready, or
/// downloading with its progress.
pub fn entries_with_model(
    lang: Lang,
    server: bool,
    status: &ServerStatus,
    update_ready: bool,
    model: Option<&str>,
    detail: Option<&str>,
) -> Vec<Entry> {
    let w = lang.words();
    let mut out = Vec::new();
    if server {
        out.push(note(status_line(lang, status, detail)));
        if let Some(line) = model {
            out.push(Entry::Item {
                id: "model-status",
                label: line.to_string(),
                enabled: false,
            });
        }
        if update_ready {
            out.push(item("update", w.update_ready));
        }
        out.push(Entry::Separator);
    }
    out.push(item("show", w.open));
    if !server {
        // client mode: the way back to the setup screens (another address,
        // or this Mac when the build carries the server)
        out.push(item("change-server", w.change_server));
    }
    if server {
        match status {
            ServerStatus::SetupNeeded => {}
            ServerStatus::Paused | ServerStatus::Stopped => {
                out.push(item("server-resume", w.resume))
            }
            _ => out.push(item("server-pause", w.pause)),
        }
        out.push(item("open-logs", w.open_logs));
    }
    out.push(Entry::Separator);
    out.push(item("media-play", w.play));
    out.push(item("media-pause", w.pause_playback));
    out.push(Entry::Separator);
    out.push(item("tray-settings", w.settings));
    out.push(Entry::Separator);
    if server && *status != ServerStatus::SetupNeeded {
        out.push(item("quit", w.quit_server_runs));
        out.push(item("stop-and-quit", w.stop_and_quit));
    } else {
        out.push(item("quit", w.quit));
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

/// The submenu's items, in order: a tray item's id (its label is
/// window_label's), or None for a separator.
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub const WINDOW_ITEMS: &[Option<&str>] = &[
    Some("update"),
    None,
    Some("server-pause"),
    Some("server-resume"),
    Some("open-logs"),
    None,
    Some("stop-and-quit"),
];

/// The submenu's title in `lang`.
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub fn window_menu_title(lang: Lang) -> &'static str {
    lang.words().window_menu
}

/// A window item's label in `lang`: the tray item's, the update said as what
/// it does from a menu.
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub fn window_label(lang: Lang, id: &str) -> Option<&'static str> {
    let w = lang.words();
    Some(match id {
        "update" => w.install_update,
        "server-pause" => w.pause,
        "server-resume" => w.resume,
        "open-logs" => w.open_logs,
        "stop-and-quit" => w.stop_and_quit,
        _ => return None,
    })
}

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
        let e = entries(Lang::En, false, &ServerStatus::Stopped, true);
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
        let e = entries(Lang::En, true, &ServerStatus::Running, false);
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
            let e = entries(Lang::En, true, &s, false);
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
            let got = ids(&entries(Lang::En, true, &s, false));
            assert!(got.contains(&"server-pause") && got.contains(&"open-logs"));
        }
    }

    #[test]
    fn setup_needed_has_no_pause_or_stop() {
        let e = entries(Lang::En, true, &ServerStatus::SetupNeeded, false);
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
        let e = entries_with_model(
            Lang::En,
            true,
            &ServerStatus::Running,
            true,
            Some(line),
            None,
        );
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
        let e = entries_with_model(
            Lang::En,
            false,
            &ServerStatus::Stopped,
            false,
            Some(line),
            None,
        );
        assert!(!ids(&e).contains(&"model-status"));
    }

    #[test]
    fn update_ready_adds_the_install_item_under_the_status() {
        let e = entries(Lang::En, true, &ServerStatus::Running, true);
        assert_eq!(ids(&e)[..2], ["status", "update"]);
    }

    #[test]
    fn the_window_menu_speaks_both_languages() {
        for lang in [Lang::En, Lang::Ar] {
            for id in WINDOW_ITEMS.iter().flatten() {
                let label = window_label(lang, id).unwrap_or_default();
                assert!(!label.is_empty(), "{lang:?} {id}");
            }
            assert!(!window_menu_title(lang).is_empty());
        }
        assert_eq!(window_menu_title(Lang::Ar), "الخادم");
        assert_eq!(
            window_label(Lang::En, "update"),
            Some("Install update and restart")
        );
        assert_eq!(
            window_label(Lang::Ar, "server-resume"),
            Some("استئناف الخادم")
        );
        assert_eq!(window_label(Lang::En, "show"), None);
        // the status line lists the roles with Arabic's comma
        assert!(status_line(
            Lang::Ar,
            &ServerStatus::Problem {
                roles: vec!["pipeline".into(), "streaming".into()]
            },
            None
        )
        .contains("pipeline، streaming"));
        assert!(status_line(
            Lang::Ar,
            &ServerStatus::NotAnswering {
                reason: "another launcher runs this install".into()
            },
            None
        )
        .ends_with("another launcher runs this install"));
    }

    #[test]
    fn status_lines() {
        assert_eq!(
            status_line(Lang::En, &ServerStatus::Starting, None),
            "Maktaba is starting…"
        );
        assert_eq!(
            status_line(
                Lang::En,
                &ServerStatus::Problem {
                    roles: vec!["pipeline".into(), "streaming".into()]
                },
                None
            ),
            "Maktaba: pipeline, streaming keeps stopping"
        );
        assert_eq!(
            status_line(
                Lang::En,
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
            status_line(Lang::En, &ServerStatus::Starting, d),
            "Maktaba is starting: Installing the transcription tools (40%)"
        );
        assert_eq!(
            status_line(
                Lang::En,
                &ServerStatus::Problem {
                    roles: vec!["api".into()]
                },
                Some("the backup before the update failed")
            ),
            "Maktaba: api cannot start (the backup before the update failed)"
        );
        assert_eq!(
            status_line(Lang::En, &ServerStatus::Running, d),
            "Maktaba is running"
        );
        let e = entries_with_model(Lang::En, true, &ServerStatus::Starting, false, None, d);
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
            status_line(Lang::En, &s, Some("Installing")),
            "Maktaba has a problem: another launcher runs this install"
        );
        let e = entries_with_model(Lang::En, true, &s, false, None, None);
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
            let line = status_line(Lang::En, &ServerStatus::NotAnswering { reason }, None);
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
    fn a_problem_outranks_an_update_which_outranks_the_rest() {
        let problems = [
            ServerStatus::Problem {
                roles: vec!["api".into()],
            },
            ServerStatus::NotAnswering {
                reason: "the server does not answer".into(),
            },
        ];
        for s in &problems {
            assert_eq!(badge_color(s, true), Some(BADGE_PROBLEM), "{s:?}");
            assert_eq!(badge_color(s, false), Some(BADGE_PROBLEM), "{s:?}");
        }
        for s in [
            ServerStatus::Running,
            ServerStatus::Starting,
            ServerStatus::Paused,
            ServerStatus::Stopped,
        ] {
            assert_eq!(badge_color(&s, true), Some(BADGE_UPDATE), "{s:?}");
            assert_ne!(badge_color(&s, false), Some(BADGE_UPDATE), "{s:?}");
        }
        assert_eq!(badge_color(&ServerStatus::SetupNeeded, true), None);
    }

    /// WCAG relative luminance.
    fn luminance([r, g, b]: [u8; 3]) -> f64 {
        let lin = |c: u8| {
            let c = c as f64 / 255.0;
            if c <= 0.04045 {
                c / 12.92
            } else {
                ((c + 0.055) / 1.055).powf(2.4)
            }
        };
        0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
    }

    fn contrast(a: [u8; 3], b: [u8; 3]) -> f64 {
        let (x, y) = (luminance(a), luminance(b));
        (x.max(y) + 0.05) / (x.min(y) + 0.05)
    }

    fn hue([r, g, b]: [u8; 3]) -> f64 {
        let (r, g, b) = (r as f64, g as f64, b as f64);
        let (max, min) = (r.max(g).max(b), r.min(g).min(b));
        let d = max - min;
        let h = if d == 0.0 {
            0.0
        } else if max == r {
            ((g - b) / d).rem_euclid(6.0)
        } else if max == g {
            (b - r) / d + 2.0
        } else {
            (r - g) / d + 4.0
        };
        h * 60.0
    }

    fn hue_apart(a: [u8; 3], b: [u8; 3]) -> f64 {
        let d = (hue(a) - hue(b)).abs();
        d.min(360.0 - d)
    }

    #[test]
    fn the_update_badge_stands_out_on_the_icon_and_either_taskbar() {
        // the app icon's blue (icons/*.png)
        let icon = [0x1e, 0x5a, 0xd8];
        // the old blue sat 4 degrees from the icon at 1.24:1
        assert!(
            hue_apart(BADGE_UPDATE, icon) >= 60.0,
            "{}",
            hue(BADGE_UPDATE)
        );
        assert!(contrast(BADGE_UPDATE, icon) >= 1.5);
        // and it is not read as a problem
        assert!(hue_apart(BADGE_UPDATE, BADGE_PROBLEM) >= 60.0);
    }

    #[test]
    fn every_badge_reads_on_either_taskbar_and_apart_from_the_others() {
        // Windows 11's light and dark taskbars
        let (light, dark) = ([0xf3, 0xf3, 0xf3], [0x20, 0x20, 0x20]);
        let all = [
            ("problem", BADGE_PROBLEM),
            ("update", BADGE_UPDATE),
            ("running", BADGE_RUNNING),
            ("starting", BADGE_STARTING),
            ("stopped", BADGE_STOPPED),
        ];
        for (name, c) in all {
            let (l, d) = (contrast(c, light), contrast(c, dark));
            assert!(l >= 3.0, "{name} on a light taskbar: {l:.2}:1");
            assert!(d >= 3.0, "{name} on a dark taskbar: {d:.2}:1");
        }
        // the coloured ones 30 degrees of hue apart or more (grey has none)
        let coloured = &all[..4];
        for (i, (a, ca)) in coloured.iter().enumerate() {
            for (b, cb) in &coloured[i + 1..] {
                let apart = hue_apart(*ca, *cb);
                assert!(apart >= 30.0, "{a} and {b}: {apart:.0} degrees");
            }
        }
        // amber stays amber, between the red and the green
        let h = hue(BADGE_STARTING);
        assert!((30.0..=50.0).contains(&h), "starting's hue {h:.0}");
        assert!(hue_apart(BADGE_STARTING, BADGE_UPDATE) >= 90.0);
    }

    #[test]
    fn a_failed_tray_item_says_which_in_the_trays_language() {
        assert_eq!(
            action_failed(Lang::En, "server-pause"),
            "The server could not be paused."
        );
        assert_eq!(
            action_failed(Lang::Ar, "server-resume"),
            "تعذّر استئناف الخادم."
        );
        assert_eq!(action_failed(Lang::Ar, "open-logs"), "تعذّر فتح السجلات.");
        // every item that can fail has its own sentence; the rest share one
        let e = entries(Lang::En, true, &ServerStatus::Running, true);
        let own = ["server-pause", "stop-and-quit", "open-logs"];
        for id in ids(&e) {
            let s = action_failed(Lang::En, id);
            assert_eq!(own.contains(&id), s != EN_ACTION_FAILED, "{id}");
        }
        assert_ne!(action_failed(Lang::En, "change-server"), EN_ACTION_FAILED);
        assert_ne!(action_failed(Lang::En, "server-resume"), EN_ACTION_FAILED);
    }

    const EN_ACTION_FAILED: &str = "That did not work.";

    #[test]
    fn an_arabic_tray_says_it_in_arabic() {
        let ar = Lang::Ar;
        let e = entries(ar, true, &ServerStatus::Running, true);
        let labels: Vec<&str> = e
            .iter()
            .filter_map(|x| match x {
                Entry::Item { label, .. } => Some(label.as_str()),
                Entry::Separator => None,
            })
            .collect();
        assert_eq!(
            labels,
            [
                "مكتبة تعمل",
                "التحديث جاهز: أعد التشغيل لتثبيته",
                "فتح مكتبة",
                "إيقاف الخادم مؤقتًا",
                "فتح السجلات",
                "تشغيل",
                "إيقاف التشغيل مؤقتًا",
                "الإعدادات",
                "خروج (يبقى الخادم عاملًا)",
                "إيقاف الخادم والخروج",
            ]
        );
        // the same ids as in English: lib.rs and the web shell match them
        assert_eq!(
            ids(&e),
            ids(&entries(Lang::En, true, &ServerStatus::Running, true))
        );
        let client = entries(ar, false, &ServerStatus::Stopped, false);
        assert!(client.contains(&item("change-server", "الاتصال بمكتبة أخرى…")));
        assert!(client.contains(&item("quit", "خروج")));
    }

    #[test]
    fn an_arabic_tray_leaves_the_servers_english_out() {
        let d = Some("Installing the transcription tools (40%)");
        assert_eq!(
            status_line(Lang::Ar, &ServerStatus::Starting, d),
            "مكتبة قيد التشغيل…"
        );
        let api = ServerStatus::Problem {
            roles: vec!["api".into()],
        };
        assert_eq!(
            status_line(Lang::Ar, &api, Some("the backup before the update failed")),
            "مكتبة: تعذّر تشغيل api"
        );
        assert_eq!(status_line(Lang::Ar, &api, None), "مكتبة: يتكرر توقف api");
        // the log's own line stays as the log has it; the reason it names
        // when the log says nothing is the tray's
        let lock = ServerStatus::NotAnswering {
            reason: "another launcher runs this install".into(),
        };
        assert_eq!(
            status_line(Lang::Ar, &lock, None),
            "في مكتبة مشكلة: another launcher runs this install"
        );
        let quiet = ServerStatus::NotAnswering {
            reason: NO_ANSWER.into(),
        };
        assert_eq!(
            status_line(Lang::Ar, &quiet, None),
            "في مكتبة مشكلة: الخادم لا يستجيب"
        );
        assert_eq!(
            status_line(Lang::En, &quiet, None),
            "Maktaba has a problem: the server does not answer"
        );
        // the longest reason fits the tooltip in Arabic too
        let log = format!("Error: {}", ["x"; 300].join(" "));
        let reason = crate::status::failure_in_log(&log).expect("a reason");
        let line = status_line(Lang::Ar, &ServerStatus::NotAnswering { reason }, None);
        assert_eq!(tooltip(&line), line);
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

    #[test]
    fn the_window_menu_offers_what_the_tray_offers() {
        let window_ids: Vec<&str> = WINDOW_ITEMS.iter().flatten().copied().collect();
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
        let running = entries(Lang::En, true, &ServerStatus::Running, false);
        assert!(
            window_enabled(&running, "server-pause") && window_enabled(&running, "stop-and-quit")
        );
        assert!(!window_enabled(&running, "server-resume") && !window_enabled(&running, "update"));
        let paused = entries(Lang::En, true, &ServerStatus::Paused, true);
        assert!(window_enabled(&paused, "server-resume") && window_enabled(&paused, "update"));
        assert!(!window_enabled(&paused, "server-pause"));
        // client mode: no server items at all
        let client = entries(Lang::En, false, &ServerStatus::SetupNeeded, false);
        assert!(window_ids.iter().all(|id| !window_enabled(&client, id)));
        // the disabled status line is never "offered"
        assert!(!window_enabled(&running, "status"));
        // the window's ids never collide with the tray's (both handlers see every event)
        for id in &window_ids {
            assert!(!ids(&running).contains(&format!("{WINDOW_PREFIX}{id}").as_str()));
        }
    }
}
