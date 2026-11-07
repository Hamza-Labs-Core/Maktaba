// The shell's own crashes (Epic 53 story 53.14 §2, the desktop row): a panic is kept on
// disk, scrubbed (redact.rs, the shared rules), under the logs folder's `crashes/`, the
// newest five. It leaves this machine only through the web app the window shows: at its next
// start the web app asks for the crashes not yet sent (`shell_crashes`), and sends each to
// the home as an envelope only when the person turned crash reports on there (the web's
// switch, web/src/lib/crashReports.ts), then says which went (`shell_crashes_sent`). Off,
// nothing leaves and the crash stays here, as app.log's panic line does.
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

/// How many crashes are kept (the oldest go first).
const KEEP: usize = 5;
/// The most of app.log a crash keeps (the home takes 512 KB of an app's log).
const LOG_MAX: usize = 256 * 1024;

/// One kept crash, as the web app sends it.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Crash {
    pub id: String,
    /// unix milliseconds
    pub at: u64,
    pub thread: String,
    pub message: String,
    /// "file:line" in the shell's source
    pub location: String,
    pub app_version: String,
    pub platform: String,
    /// app.log's last lines before it, scrubbed
    pub log: Vec<String>,
    #[serde(default)]
    pub sent: bool,
}

fn dir() -> Option<PathBuf> {
    crate::agent::logs_dir().map(|d| d.join("crashes"))
}

/// Keeps a panic (from applog's hook; never panics itself).
pub fn keep(thread: &str, message: &str, location: &str) {
    if cfg!(test) {
        return;
    }
    let Some(d) = dir() else { return };
    let log = d
        .parent()
        .map(|l| tail_of_log(&l.join("app.log")))
        .unwrap_or_default();
    let _ = keep_in(&d, thread, message, location, log, SystemTime::now());
}

pub(crate) fn keep_in(
    d: &Path,
    thread: &str,
    message: &str,
    location: &str,
    log: Vec<String>,
    now: SystemTime,
) -> std::io::Result<Crash> {
    std::fs::create_dir_all(d)?;
    let at = now
        .duration_since(UNIX_EPOCH)
        .map(|t| t.as_millis() as u64)
        .unwrap_or(0);
    let c = Crash {
        id: format!("{at:015}-{:08x}", std::process::id() ^ (at as u32)),
        at,
        thread: crate::redact::scrub(thread),
        message: crate::redact::scrub(message),
        location: location.to_string(),
        app_version: env!("CARGO_PKG_VERSION").to_string(),
        platform: std::env::consts::OS.to_string(),
        log: log.iter().map(|l| crate::redact::scrub(l)).collect(),
        sent: false,
    };
    let body = serde_json::to_vec(&c).map_err(std::io::Error::other)?;
    std::fs::write(d.join(format!("crash-{}.json", c.id)), body)?;
    let mut files = list(d);
    files.sort();
    let extra = files.len().saturating_sub(KEEP);
    for f in files.into_iter().take(extra) {
        let _ = std::fs::remove_file(f);
    }
    Ok(c)
}

fn list(d: &Path) -> Vec<PathBuf> {
    std::fs::read_dir(d)
        .map(|r| {
            r.filter_map(|e| e.ok().map(|e| e.path()))
                .filter(|p| {
                    p.file_name()
                        .and_then(|n| n.to_str())
                        .is_some_and(|n| n.starts_with("crash-") && n.ends_with(".json"))
                })
                .collect()
        })
        .unwrap_or_default()
}

/// app.log's last lines, at most LOG_MAX bytes, oldest first.
fn tail_of_log(path: &Path) -> Vec<String> {
    let Ok(text) = std::fs::read_to_string(path) else {
        return vec![];
    };
    let mut size = 0;
    let mut out: Vec<String> = text
        .lines()
        .rev()
        .take_while(|l| {
            size += l.len() + 1;
            size <= LOG_MAX
        })
        .map(str::to_string)
        .collect();
    out.reverse();
    out
}

/// The kept crashes not yet sent, oldest first.
pub(crate) fn pending_in(d: &Path) -> Vec<Crash> {
    let mut files = list(d);
    files.sort();
    files
        .iter()
        .filter_map(|f| std::fs::read(f).ok())
        .filter_map(|b| serde_json::from_slice::<Crash>(&b).ok())
        .filter(|c| !c.sent)
        .collect()
}

/// Marks crashes as sent (kept on disk, never offered again).
pub(crate) fn mark_sent_in(d: &Path, ids: &[String]) -> usize {
    let mut n = 0;
    for f in list(d) {
        let Ok(b) = std::fs::read(&f) else { continue };
        let Ok(mut c) = serde_json::from_slice::<Crash>(&b) else {
            continue;
        };
        if !c.sent && ids.contains(&c.id) {
            c.sent = true;
            if serde_json::to_vec(&c)
                .ok()
                .and_then(|b| std::fs::write(&f, b).ok())
                .is_some()
            {
                n += 1;
            }
        }
    }
    n
}

/// The shell's crashes not yet sent: the web app sends them when crash reports are on.
#[tauri::command]
pub fn shell_crashes() -> Vec<Crash> {
    dir().map(|d| pending_in(&d)).unwrap_or_default()
}

/// The web app sent these to the home.
#[tauri::command]
pub fn shell_crashes_sent(ids: Vec<String>) -> usize {
    dir().map(|d| mark_sent_in(&d, &ids)).unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    fn tmp() -> PathBuf {
        let d = std::env::temp_dir().join(format!(
            "maktaba-crashes-{}-{}",
            std::process::id(),
            rand_suffix()
        ));
        let _ = std::fs::remove_dir_all(&d);
        d
    }

    fn rand_suffix() -> u128 {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    }

    #[test]
    fn a_panic_is_kept_scrubbed_and_offered_until_sent() {
        let d = tmp();
        let c = keep_in(
            &d,
            "main",
            "called unwrap on Authorization: Bearer abcdef.ghijkl for someone@example.com",
            "src/tray.rs:12",
            vec!["{\"msg\":\"token\",\"attrs\":{\"text\":\"Bearer abcdef.ghijkl\"}}".into()],
            UNIX_EPOCH + Duration::from_secs(1_800_000_000),
        )
        .unwrap();
        assert!(!c.message.contains("abcdef") && !c.message.contains("someone@example.com"));
        assert!(!c.log[0].contains("abcdef"));
        assert_eq!(c.at, 1_800_000_000_000);
        let p = pending_in(&d);
        assert_eq!(p.len(), 1);
        assert_eq!(p[0].location, "src/tray.rs:12");
        assert_eq!(mark_sent_in(&d, std::slice::from_ref(&c.id)), 1);
        assert!(pending_in(&d).is_empty(), "sent: not offered again");
        assert_eq!(list(&d).len(), 1, "still kept on disk");
        let _ = std::fs::remove_dir_all(&d);
    }

    #[test]
    fn the_newest_five_are_kept() {
        let d = tmp();
        for i in 0..7 {
            keep_in(
                &d,
                "t",
                "m",
                "l",
                vec![],
                UNIX_EPOCH + Duration::from_secs(1_800_000_000 + i),
            )
            .unwrap();
        }
        let p = pending_in(&d);
        assert_eq!(p.len(), 5);
        assert_eq!(p[0].at, 1_800_000_002_000);
        let _ = std::fs::remove_dir_all(&d);
    }

    #[test]
    fn the_log_tail_is_the_newest_lines_within_its_limit() {
        let d = tmp();
        std::fs::create_dir_all(&d).unwrap();
        let f = d.join("app.log");
        let line = "x".repeat(1000);
        std::fs::write(
            &f,
            (0..400)
                .map(|i| format!("{i} {line}"))
                .collect::<Vec<_>>()
                .join("\n"),
        )
        .unwrap();
        let t = tail_of_log(&f);
        assert!(t.iter().map(|l| l.len() + 1).sum::<usize>() <= LOG_MAX);
        assert!(t.last().unwrap().starts_with("399 "));
        let _ = std::fs::remove_dir_all(&d);
    }
}
