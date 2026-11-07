//! `mkp flow` (story 54.41 §3): the graph a plugin, or several that work together, declare
//! in their manifests, as a text chart for the terminal and as Mermaid (`flow.mmd`). The
//! full check of a flow (cycles, the leaf rule, flow.toml) is the SDK's and the home's
//! (54.39); this draws what the manifests say, edge by edge, in the order they run.

use serde_json::{Map, Value};

/// One edge, as people read it: from, to.
pub type Edge = (String, String);

fn strs(v: &Value) -> Vec<String> {
    v.as_array().map(|a| a.iter().filter_map(|x| x.as_str().map(String::from)).collect()).unwrap_or_default()
}

/// The edges the manifests declare: core events into stages, a stage after another, a
/// stage's requests and emits, and published events into their subscribers.
pub fn edges(manifests: &[Map<String, Value>]) -> Vec<Edge> {
    let mut out = Vec::new();
    for m in manifests {
        let id = m["id"].as_str().unwrap_or_default();
        for st in m["stage"].as_array().into_iter().flatten() {
            let me = format!("{id}/{}", st["name"].as_str().unwrap_or_default());
            for t in st["trigger"].as_array().into_iter().flatten() {
                let from = match (t["event"].as_str(), t["stage"].as_str()) {
                    (Some(e), _) => e.to_string(),
                    (None, Some(s)) => format!("{id}/{s}"),
                    _ => continue,
                };
                out.push((from, me.clone()));
            }
            for r in strs(&st["may_request"]) {
                out.push((me.clone(), format!("{id}/{r}")));
            }
            for e in strs(&st["emits"]) {
                out.push((me.clone(), format!("plugin:{id}/{e}")));
            }
        }
    }
    order(out)
}

/// Edges in the order they run: an edge whose source nothing points at first, then by
/// depth, ties by name; each once.
fn order(mut es: Vec<Edge>) -> Vec<Edge> {
    es.sort();
    es.dedup();
    let mut depth = std::collections::BTreeMap::<String, usize>::new();
    for _ in 0..es.len() + 1 {
        let mut moved = false;
        for (a, b) in &es {
            let d = depth.get(a).copied().unwrap_or(0) + 1;
            if depth.get(b).copied().unwrap_or(0) < d && d <= es.len() {
                depth.insert(b.clone(), d);
                moved = true;
            }
        }
        if !moved {
            break;
        }
    }
    es.sort_by(|x, y| {
        (depth.get(&x.0).copied().unwrap_or(0), &x.0, &x.1).cmp(&(depth.get(&y.0).copied().unwrap_or(0), &y.0, &y.1))
    });
    es
}

/// The text chart: one line per edge.
pub fn text(es: &[Edge]) -> String {
    es.iter().map(|(a, b)| format!("{a} → {b}")).collect::<Vec<_>>().join("\n")
}

/// Mermaid, left to right.
pub fn mermaid(es: &[Edge]) -> String {
    let mut ids: Vec<&String> = es.iter().flat_map(|(a, b)| [a, b]).collect();
    ids.sort();
    ids.dedup();
    let key = |s: &String| format!("n{}", ids.iter().position(|x| *x == s).unwrap_or(0));
    let mut lines = vec!["graph LR".to_string()];
    for s in &ids {
        let label = s.replace('"', "'");
        lines.push(if s.contains('/') && !s.starts_with("plugin:") {
            format!("  {}[\"{label}\"]", key(s))
        } else if s.starts_with("plugin:") {
            format!("  {}{{{{\"{label}\"}}}}", key(s))
        } else {
            format!("  {}([\"{label}\"])", key(s))
        });
    }
    for (a, b) in es {
        lines.push(format!("  {} --> {}", key(a), key(b)));
    }
    lines.join("\n")
}
