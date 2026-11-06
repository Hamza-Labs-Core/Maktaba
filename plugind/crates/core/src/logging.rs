//! Plugin log lines (story 54.09 §3).
//!
//! Each plugin's lines are written as one JSON object per line with `service =
//! "plugin.<id>"`, the plugin's version, the job id and stage, `level`, `event` (the
//! plugin's code), `fields` and `origin = "plugin"`: the Epic 53 line (53.01). Limits per
//! plugin: 100 lines a second (burst 100), 10 MB a day, 8 KB a line; dropped lines are
//! counted and one `log_dropped` line a minute says how many.
//!
//! **The Epic 53 seam.** Lines leave through [`LogSink`]. Today the sink is the host's
//! stdout, which the supervisor captures into `server.log` with the same fields (the
//! fallback 54.09 names until 53.02 lands). Epic 53's log keeper (53.02) connects here:
//! either the supervisor routes `service = plugin.*` lines from this stream to
//! `<data>/logs/segments/plugin.<id>/`, or a segment sink replaces [`StdoutSink`].

use std::collections::HashMap;
use std::io::Write;
use std::sync::{Arc, Mutex};
use std::time::{Instant, SystemTime, UNIX_EPOCH};

use serde_json::{json, Map, Value};

pub const LINES_PER_SECOND: f64 = 100.0;
pub const BURST: f64 = 100.0;
pub const BYTES_PER_DAY: u64 = 10 << 20;
pub const MAX_LINE: usize = 8 << 10;

/// Where finished lines go. One method, so 53.02 can connect segments here.
pub trait LogSink: Send + Sync {
    fn write_line(&self, line: &str);
}

/// The host's stdout, captured by the supervisor.
pub struct StdoutSink;

impl LogSink for StdoutSink {
    fn write_line(&self, line: &str) {
        let mut out = std::io::stdout().lock();
        let _ = out.write_all(line.as_bytes());
        let _ = out.write_all(b"\n");
    }
}

/// A sink that keeps lines in memory (tests, the SDK's harness).
#[derive(Default)]
pub struct MemorySink(pub Mutex<Vec<String>>);

impl LogSink for MemorySink {
    fn write_line(&self, line: &str) {
        self.0.lock().unwrap().push(line.to_string());
    }
}

#[derive(Debug)]
struct Bucket {
    tokens: f64,
    last: Instant,
    day: u64,
    day_bytes: u64,
    dropped: u64,
    dropped_total: u64,
    last_report: Instant,
}

/// The rate limits of every plugin on this host, by plugin id.
pub struct Logs {
    sink: Arc<dyn LogSink>,
    buckets: Mutex<HashMap<String, Bucket>>,
}

/// Who a line is from.
#[derive(Debug, Clone, Default)]
pub struct Origin {
    pub plugin_id: String,
    pub version: String,
    pub job_id: String,
    pub stage: String,
}

fn now_rfc3339() -> String {
    let d = SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default();
    let secs = d.as_secs();
    let (days, rem) = (secs / 86_400, secs % 86_400);
    let (y, m, dd) = civil(days as i64);
    format!("{y:04}-{m:02}-{dd:02}T{:02}:{:02}:{:02}.{:03}Z", rem / 3600, rem % 3600 / 60, rem % 60, d.subsec_millis())
}

/// Days since 1970-01-01 to a civil date (Howard Hinnant's algorithm).
fn civil(z: i64) -> (i64, u32, u32) {
    let z = z + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    (if m <= 2 { y + 1 } else { y }, m, d)
}

impl Logs {
    pub fn new(sink: Arc<dyn LogSink>) -> Self {
        Self { sink, buckets: Mutex::new(HashMap::new()) }
    }

    /// The host's own line (`service = plugins.host`).
    pub fn host(&self, level: &str, event: &str, fields: Value) {
        let line =
            json!({"ts": now_rfc3339(), "level": level, "service": "plugins.host", "event": event, "fields": fields});
        self.sink.write_line(&line.to_string());
    }

    /// One plugin line, if its limits allow. `fields` is the plugin's JSON text, kept as
    /// data (an object, or the text as a string when it is not one). Answers false when
    /// the line was dropped.
    pub fn plugin(&self, o: &Origin, level: &str, code: &str, fields: &str) -> bool {
        let fields: Value = match serde_json::from_str::<Value>(fields) {
            Ok(Value::Object(m)) => Value::Object(m),
            _ => json!({"text": fields}),
        };
        let mut code =
            code.chars().filter(|c| c.is_ascii_alphanumeric() || *c == '_' || *c == '.').take(64).collect::<String>();
        if code.is_empty() {
            code = "line".into();
        }
        let mut line = json!({
            "ts": now_rfc3339(),
            "level": level,
            "service": format!("plugin.{}", o.plugin_id),
            "version": o.version,
            "job_id": o.job_id,
            "stage": o.stage,
            "event": code,
            "fields": fields,
            "origin": "plugin",
        })
        .to_string();
        if line.len() > MAX_LINE {
            let mut m = Map::new();
            m.insert("truncated".into(), Value::Bool(true));
            line = json!({
                "ts": now_rfc3339(), "level": level, "service": format!("plugin.{}", o.plugin_id),
                "version": o.version, "job_id": o.job_id, "stage": o.stage, "event": code,
                "fields": Value::Object(m), "origin": "plugin",
            })
            .to_string();
        }
        let (ok, report) = self.admit(&o.plugin_id, line.len() as u64);
        if let Some(n) = report {
            let r = json!({
                "ts": now_rfc3339(), "level": "warn", "service": format!("plugin.{}", o.plugin_id),
                "version": o.version, "job_id": o.job_id, "stage": o.stage, "event": "log_dropped",
                "fields": {"dropped": n}, "origin": "host",
            });
            self.sink.write_line(&r.to_string());
        }
        if ok {
            self.sink.write_line(&line);
        }
        ok
    }

    /// Total lines dropped for a plugin since the host started.
    pub fn dropped(&self, plugin_id: &str) -> u64 {
        self.buckets.lock().unwrap().get(plugin_id).map(|b| b.dropped_total).unwrap_or(0)
    }

    fn admit(&self, plugin_id: &str, bytes: u64) -> (bool, Option<u64>) {
        let now = Instant::now();
        let day = SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default().as_secs() / 86_400;
        let mut all = self.buckets.lock().unwrap();
        let b = all.entry(plugin_id.to_string()).or_insert(Bucket {
            tokens: BURST,
            last: now,
            day,
            day_bytes: 0,
            dropped: 0,
            dropped_total: 0,
            last_report: now,
        });
        b.tokens = (b.tokens + now.duration_since(b.last).as_secs_f64() * LINES_PER_SECOND).min(BURST);
        b.last = now;
        if b.day != day {
            b.day = day;
            b.day_bytes = 0;
        }
        let ok = b.tokens >= 1.0 && b.day_bytes + bytes <= BYTES_PER_DAY;
        if ok {
            b.tokens -= 1.0;
            b.day_bytes += bytes;
        } else {
            b.dropped += 1;
            b.dropped_total += 1;
        }
        let mut report = None;
        if b.dropped > 0 && now.duration_since(b.last_report).as_secs() >= 60 {
            report = Some(b.dropped);
            b.dropped = 0;
            b.last_report = now;
        }
        (ok, report)
    }

    /// Reports what is left unreported for a plugin (at the end of a job).
    pub fn flush_dropped(&self, o: &Origin) {
        let n = {
            let mut all = self.buckets.lock().unwrap();
            match all.get_mut(&o.plugin_id) {
                Some(b) if b.dropped > 0 => {
                    let n = b.dropped;
                    b.dropped = 0;
                    b.last_report = Instant::now();
                    n
                }
                _ => return,
            }
        };
        let r = json!({
            "ts": now_rfc3339(), "level": "warn", "service": format!("plugin.{}", o.plugin_id),
            "version": o.version, "job_id": o.job_id, "stage": o.stage, "event": "log_dropped",
            "fields": {"dropped": n}, "origin": "host",
        });
        self.sink.write_line(&r.to_string());
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn origin() -> Origin {
        Origin { plugin_id: "dev.example.x".into(), version: "1.0.0".into(), job_id: "j1".into(), stage: "s".into() }
    }

    /// 54.09 T04 (host side): a flood keeps at most the burst, counts the rest, and one
    /// log_dropped line reports them; another plugin is not affected.
    #[test]
    fn flood_is_rate_limited() {
        let sink = Arc::new(MemorySink::default());
        let logs = Logs::new(sink.clone());
        let mut kept = 0;
        let t = Instant::now();
        for i in 0..10_000 {
            if logs.plugin(&origin(), "info", "tick", &format!("{{\"i\":{i}}}")) {
                kept += 1;
            }
        }
        // the burst, plus what the rate refilled while the loop ran
        let most = BURST + t.elapsed().as_secs_f64() * LINES_PER_SECOND + 1.0;
        assert!(kept as f64 <= most, "kept {kept}, at most {most}");
        assert_eq!(logs.dropped("dev.example.x"), 10_000 - kept);
        let other = Origin { plugin_id: "dev.example.y".into(), ..origin() };
        assert!(logs.plugin(&other, "info", "tick", "{}"));
        logs.flush_dropped(&origin());
        let lines = sink.0.lock().unwrap();
        let last: Value =
            serde_json::from_str(lines.iter().rev().find(|l| l.contains("log_dropped")).unwrap()).unwrap();
        assert_eq!(last["service"], "plugin.dev.example.x");
        assert!(last["fields"]["dropped"].as_u64().unwrap() >= 9_000);
    }

    #[test]
    fn line_shape_and_size() {
        let sink = Arc::new(MemorySink::default());
        let logs = Logs::new(sink.clone());
        logs.plugin(&origin(), "info", "chapters found!", "{\"n\":3}");
        logs.plugin(&origin(), "warn", "big", &format!("{{\"x\":\"{}\"}}", "a".repeat(20_000)));
        logs.plugin(&origin(), "info", "text", "not json <script>");
        let lines = sink.0.lock().unwrap();
        let first: Value = serde_json::from_str(&lines[0]).unwrap();
        assert_eq!(first["service"], "plugin.dev.example.x");
        assert_eq!(first["event"], "chaptersfound");
        assert_eq!(first["origin"], "plugin");
        assert_eq!(first["job_id"], "j1");
        assert_eq!(first["fields"]["n"], 3);
        assert!(lines[1].len() <= MAX_LINE);
        let third: Value = serde_json::from_str(&lines[2]).unwrap();
        assert_eq!(third["fields"]["text"], "not json <script>");
    }

    #[test]
    fn civil_dates() {
        assert_eq!(civil(0), (1970, 1, 1));
        assert_eq!(civil(20_398), (2025, 11, 6));
    }
}
