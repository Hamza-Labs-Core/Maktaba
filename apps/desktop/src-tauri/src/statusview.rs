// What the status window shows (Epic 13 story 13.13; the design:
// docs/design/desktop/mockups/server-status.html), as data the page words in its own
// language: the server's status, the machine and the release, and one tile per part of the
// server, folded from the supervisor's processes (status::Snapshot). Pure, so it is tested
// without a running app; ownwindows.rs serves it.

use serde::Serialize;

use crate::autoinstall::Shown;
use crate::status::{Child, Detail, Llm, Snapshot, StatusView};

/// The parts the window shows a tile for, in its order, and the supervisor's roles each one
/// gathers. The copier, the log reader and other inner processes have none.
const PARTS: &[(&str, &[&str])] = &[
    ("database", &["postgres"]),
    ("server", &["api"]),
    ("playback", &["streaming", "tv-slot"]),
    ("indexing", &["pipeline"]),
    ("model", &["ollama"]),
    ("plugins", &["plugins", "plugin-runner"]),
    ("remote", &["relay"]),
];

/// A part's state, worst first: what its dot and its value say.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Tone {
    /// a process keeps failing
    Error,
    /// starting, restarting, a model downloading
    Busy,
    /// running
    Ok,
    /// stopped
    Idle,
    /// its component is off (held), or an on-demand process nobody asked for
    Off,
}

/// One tile.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Part {
    /// "database", "server", "playback", "indexing", "model", "plugins", "remote"
    pub id: &'static str,
    pub tone: Tone,
    /// how many of its processes run (the Live TV channels on air, with playback)
    pub running: usize,
    /// what it waits on or why it fails, as the server says it (its code and values for the
    /// page's words)
    #[serde(flatten)]
    pub detail: Option<Detail>,
    /// a download or an install under way, 0 to 1
    #[serde(skip_serializing_if = "Option::is_none")]
    pub progress: Option<f64>,
    /// the language model's name, while it downloads or once it is ready
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    /// the bytes downloaded and to download, while the model downloads
    #[serde(skip_serializing_if = "Option::is_none")]
    pub bytes: Option<(u64, u64)>,
}

/// A release on its way: the home's order (it installs by itself, by `by` or shortly, or
/// now), or one this app installs on a click (`install`: the Linux app catching up with its
/// server).
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateView {
    pub version: String,
    /// the time it installs by (the machine's clock), "" for shortly
    pub by: String,
    pub installing: bool,
    /// a button installs it now
    pub install: bool,
}

impl UpdateView {
    pub fn of_order(order: &Shown) -> UpdateView {
        match order {
            Shown::Ready { version, by } => UpdateView {
                version: version.clone(),
                by: by.clone(),
                installing: false,
                install: false,
            },
            Shown::Installing { version } => UpdateView {
                version: version.clone(),
                by: String::new(),
                installing: true,
                install: false,
            },
        }
    }
}

/// The window's whole view.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct StatusWindowView {
    /// the server's status, as the starting screen reads it ({"kind": "running"}, …)
    pub status: StatusView,
    /// this machine's name ("" when the supervisor does not answer)
    pub host: String,
    /// the app's release
    pub version: String,
    /// when the server started (RFC 3339), while it answers
    #[serde(skip_serializing_if = "Option::is_none")]
    pub since: Option<String>,
    pub parts: Vec<Part>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub update: Option<UpdateView>,
}

/// One process's tone.
fn tone_of(c: &Child) -> Tone {
    if c.failing() {
        return Tone::Error;
    }
    match c.state.as_str() {
        "running" => Tone::Ok,
        "starting" | "backoff" => Tone::Busy,
        "held" => Tone::Off,
        // an on-demand process nobody asked for (a Live TV channel off the air) is meant to
        // be down; one that was asked for is on its way
        "exited" | "stopped" if c.wanted => Tone::Busy,
        "exited" | "stopped" if c.role == "tv-slot" || c.role == "plugin-runner" => Tone::Off,
        _ => Tone::Idle,
    }
}

/// A part's tone: its worst process's, leaving out the on-demand ones that are off while
/// another of its processes says more.
fn fold(tones: &[Tone]) -> Tone {
    let said: Vec<Tone> = tones.iter().copied().filter(|t| *t != Tone::Off).collect();
    said.into_iter().min().unwrap_or(if tones.is_empty() {
        Tone::Idle
    } else {
        Tone::Off
    })
}

/// A detail's progress: its `pct` value (the venv being installed), 0 to 1.
fn progress_of(d: &Detail) -> Option<f64> {
    let pct = d.params.as_ref()?.get("pct")?.as_f64()?;
    Some((pct / 100.0).clamp(0.0, 1.0))
}

/// The language model's part from the model server's state: its tone, the model it is
/// about, and the download's bytes.
fn model_part(llm: &Llm) -> (Tone, Option<String>, Option<(u64, u64)>) {
    if let Some(m) = llm.models.iter().find(|m| m.state == "downloading") {
        let bytes = (m.total > 0).then(|| (m.done.min(m.total), m.total));
        return (Tone::Busy, Some(m.name.clone()), bytes);
    }
    if let Some(m) = llm.models.iter().find(|m| m.state == "failed") {
        return (Tone::Error, Some(m.name.clone()), None);
    }
    let name = llm.models.first().map(|m| m.name.clone());
    if llm.ready {
        (Tone::Ok, name, None)
    } else if !llm.up {
        (Tone::Busy, name, None)
    } else {
        // up, with a model missing: it downloads when the server asks for it
        (Tone::Idle, name, None)
    }
}

/// The tiles: one per part the supervisor runs, in PARTS' order. None while it does not
/// answer.
pub fn parts(snap: Option<&Snapshot>) -> Vec<Part> {
    let Some(snap) = snap else {
        return Vec::new();
    };
    let mut out = Vec::new();
    for (id, roles) in PARTS {
        let procs: Vec<&Child> = snap
            .processes
            .iter()
            .filter(|c| roles.contains(&c.role.as_str()))
            .collect();
        let llm = (*id == "model").then_some(snap.llm.as_ref()).flatten();
        if procs.is_empty() && llm.is_none() {
            continue;
        }
        let mut tones: Vec<Tone> = procs.iter().map(|c| tone_of(c)).collect();
        let (mut model, mut bytes) = (None, None);
        if let Some(llm) = llm {
            let (tone, name, b) = model_part(llm);
            tones.push(tone);
            model = name;
            bytes = b;
        }
        let tone = fold(&tones);
        // a failing process's reason first, else what one that is not running waits on
        let detail = procs
            .iter()
            .filter(|c| c.failing())
            .find_map(|c| c.detail())
            .or_else(|| {
                procs
                    .iter()
                    .filter(|c| c.state != "running")
                    .find_map(|c| c.detail())
            });
        let progress = match bytes {
            Some((done, total)) => Some(done as f64 / total as f64),
            None => detail.as_ref().and_then(progress_of),
        };
        out.push(Part {
            id,
            tone,
            running: procs.iter().filter(|c| c.state == "running").count(),
            detail,
            progress,
            model,
            bytes,
        });
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::status::{LlmModel, ServerStatus};
    use serde_json::json;

    fn child(role: &str, state: &str) -> Child {
        serde_json::from_value(json!({ "role": role, "state": state })).unwrap()
    }

    fn snap(processes: Vec<Child>) -> Snapshot {
        Snapshot {
            processes,
            ..Snapshot::default()
        }
    }

    fn ids(parts: &[Part]) -> Vec<&'static str> {
        parts.iter().map(|p| p.id).collect()
    }

    #[test]
    fn one_tile_per_part_in_the_window_s_order() {
        let s = snap(vec![
            child("pipeline", "running"),
            child("api", "running"),
            child("logs", "running"),
            child("postgres", "running"),
            child("streaming", "running"),
            child("copy", "running"),
        ]);
        let p = parts(Some(&s));
        // the inner processes (the copier, the log reader) have no tile
        assert_eq!(ids(&p), ["database", "server", "playback", "indexing"]);
        assert!(p.iter().all(|t| t.tone == Tone::Ok));
        // a server that does not answer has no tiles
        assert!(parts(None).is_empty());
    }

    #[test]
    fn a_part_says_its_worst_process() {
        let mut slot = child("tv-slot", "stopped");
        slot.instance = "blue".into();
        let mut on_air = child("tv-slot", "running");
        on_air.instance = "green".into();
        let s = snap(vec![child("streaming", "running"), slot.clone(), on_air]);
        let p = parts(Some(&s));
        // a channel off the air does not make playback stopped; one on the air is counted
        assert_eq!(p[0].tone, Tone::Ok);
        assert_eq!(p[0].running, 2);

        let mut crash = child("streaming", "backoff");
        crash.crash_loop = true;
        let p = parts(Some(&snap(vec![crash, slot.clone()])));
        assert_eq!(p[0].tone, Tone::Error);

        // a channel asked for and not up yet is on its way
        let mut asked = slot.clone();
        asked.wanted = true;
        let p = parts(Some(&snap(vec![child("streaming", "running"), asked])));
        assert_eq!(p[0].tone, Tone::Busy);

        // a part whose every process is off (a turned-off component) says so
        let p = parts(Some(&snap(vec![child("plugins", "held")])));
        assert_eq!((p[0].id, p[0].tone), ("plugins", Tone::Off));
        let p = parts(Some(&snap(vec![child("api", "stopped")])));
        assert_eq!(p[0].tone, Tone::Idle);
    }

    #[test]
    fn a_part_carries_what_it_waits_on_and_its_progress() {
        let install: Child = serde_json::from_value(json!({
            "role": "pipeline", "state": "starting",
            "detail": "Installing (40%)", "detail_code": "venv_install",
            "detail_params": { "pct": 40 }
        }))
        .unwrap();
        let p = parts(Some(&snap(vec![install])));
        assert_eq!(p[0].tone, Tone::Busy);
        let d = p[0].detail.as_ref().expect("the detail");
        assert_eq!(d.code.as_deref(), Some("venv_install"));
        assert_eq!(p[0].progress, Some(0.4));
        // flat, as the starting screen reads a status's detail
        let j = serde_json::to_value(&p[0]).unwrap();
        assert_eq!(j["detail"], "Installing (40%)");
        assert_eq!(j["detail_code"], "venv_install");
        assert_eq!(j["detail_params"]["pct"], 40);
        // a running process's detail is no news
        let done: Child = serde_json::from_value(json!({
            "role": "pipeline", "state": "running", "detail": "Installed"
        }))
        .unwrap();
        assert_eq!(parts(Some(&snap(vec![done])))[0].detail, None);
    }

    #[test]
    fn the_language_model_tile_follows_its_download() {
        let mut s = snap(vec![child("ollama", "running")]);
        s.llm = Some(Llm {
            up: true,
            ready: false,
            models: vec![LlmModel {
                name: "qwen2.5:7b".into(),
                state: "downloading".into(),
                done: 1 << 30,
                total: 4 << 30,
            }],
        });
        let p = parts(Some(&s));
        assert_eq!(ids(&p), ["model"]);
        assert_eq!(p[0].tone, Tone::Busy);
        assert_eq!(p[0].model.as_deref(), Some("qwen2.5:7b"));
        assert_eq!(p[0].bytes, Some((1 << 30, 4 << 30)));
        assert_eq!(p[0].progress, Some(0.25));
        // ready
        let llm = s.llm.as_mut().unwrap();
        llm.ready = true;
        llm.models[0].state = "ready".into();
        let p = parts(Some(&s));
        assert_eq!((p[0].tone, p[0].progress), (Tone::Ok, None));
        // a model server outside the app ([llm] url) has a tile with no process
        s.processes.clear();
        assert_eq!(ids(&parts(Some(&s))), ["model"]);
    }

    #[test]
    fn the_view_is_camel_case_with_the_status_as_the_starting_screen_reads_it() {
        let v = StatusWindowView {
            status: StatusView {
                status: ServerStatus::Running,
                detail: None,
            },
            host: "Haddad-PC".into(),
            version: "0.3.112".into(),
            since: Some("2026-11-23T08:14:00Z".into()),
            parts: parts(Some(&snap(vec![child("api", "running")]))),
            update: Some(UpdateView::of_order(&Shown::Ready {
                version: "0.3.113".into(),
                by: "03:00".into(),
            })),
        };
        let j = serde_json::to_value(&v).unwrap();
        assert_eq!(j["status"], json!({ "kind": "running" }));
        assert_eq!(
            j["parts"][0],
            json!({ "id": "server", "tone": "ok", "running": 1 })
        );
        assert_eq!(
            j["update"],
            json!({ "version": "0.3.113", "by": "03:00", "installing": false, "install": false })
        );
        let now = UpdateView::of_order(&Shown::Installing {
            version: "0.3.113".into(),
        });
        assert!(now.installing && now.by.is_empty());
    }
}
