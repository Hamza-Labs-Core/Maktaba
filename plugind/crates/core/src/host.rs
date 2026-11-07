//! The `maktaba:plugin` imports, implemented as thin forwarders (story 54.02 §3).
//!
//! Each import checks its argument sizes, checks locally that a capability covering it was
//! granted (if not, `call-error.capability` without leaving the host), adds the job's
//! token and sends the call to the plugin API, which checks everything again and decides.

use std::collections::{BTreeSet, HashMap};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Instant;

use maktaba_plugin_manifest as manifest;
use serde_json::{json, Value};
use wasmtime::component::ResourceTable;
use wasmtime::ResourceLimiter;
use wasmtime_wasi::{WasiCtx, WasiCtxView, WasiView};

use crate::api::{ApiError, PluginApi};
use crate::logging::{Logs, Origin};
use crate::protocol::{QueueRequest, Subject as PSubject};

wasmtime::component::bindgen!({
    path: "../../../shared/plugins/wit",
    world: "plugin",
});

use maktaba::plugin::compute::{Embeddings, GenerateOptions};
pub use maktaba::plugin::types::{CallError, Level, Subject, SubjectKind};
use maktaba::plugin::write::Chapter;

/// Argument size limits the host checks before a call leaves it (README §4.7).
pub const MAX_VALUE: usize = 64 << 10;
pub const MAX_ARTIFACT: usize = 4 << 20;
pub const MAX_READ: u32 = 8 << 20;
pub const MAX_REQUESTS: usize = 1000;
/// compute (story 54.21 §1): texts per embed call, characters per text, prompt bytes and
/// the most tokens one generate call may ask for
pub const MAX_EMBED_TEXTS: usize = 64;
pub const MAX_EMBED_CHARS: usize = 2000;
pub const MAX_PROMPT: usize = 32 << 10;
pub const MAX_TOKENS: u32 = 1024;

/// Memory, table and instance caps of one job's store (54.02 §5). A component holds a few
/// core instances, memories and tables (its module, shims, an adapter), so the counts are
/// small numbers rather than one.
pub struct Limiter {
    pub memory_cap: u64,
    pub peak_memory: u64,
    pub hit: bool,
}

impl ResourceLimiter for Limiter {
    fn memory_growing(&mut self, _current: usize, desired: usize, _max: Option<usize>) -> wasmtime::Result<bool> {
        if desired as u64 > self.memory_cap {
            self.hit = true;
            return Err(wasmtime::Error::msg(format!("plugin_memory: {desired} bytes asked, cap {}", self.memory_cap)));
        }
        self.peak_memory = self.peak_memory.max(desired as u64);
        Ok(true)
    }

    fn table_growing(&mut self, _current: usize, desired: usize, _max: Option<usize>) -> wasmtime::Result<bool> {
        if desired > 100_000 {
            self.hit = true;
            return Err(wasmtime::Error::msg(format!("plugin_memory: table of {desired} elements")));
        }
        Ok(true)
    }

    fn instances(&self) -> usize {
        16
    }
    fn tables(&self) -> usize {
        16
    }
    fn memories(&self) -> usize {
        4
    }
}

/// Where a job's progress (a percentage) and checkpoints go as they come.
pub type Progress = Box<dyn FnMut(Option<u8>, Option<&[u8]>) + Send>;

/// Why a job ended early, set by the epoch callback.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Ended {
    Timeout,
    Cancelled,
}

/// The state of one job's store. The token lives here and nowhere the guest can read.
pub struct JobState {
    pub wasi: WasiCtx,
    pub table: ResourceTable,
    pub limiter: Limiter,
    pub token: String,
    pub api: Arc<dyn PluginApi>,
    pub subject: PSubject,
    pub payload: String,
    pub settings: String,
    pub grants: Vec<String>,
    pub granted: BTreeSet<String>,
    pub last_checkpoint: Option<Vec<u8>>,
    pub checkpoint: Option<Vec<u8>>,
    pub scratch: HashMap<String, Vec<u8>>,
    pub scratch_used: u64,
    pub scratch_cap: u64,
    /// progress (a percentage) and checkpoints, sent to the dispatcher as they come
    pub progress: Progress,
    pub requests: Vec<QueueRequest>,
    /// the first refusal that fails a job (capability, scope, quota)
    pub refusal: Option<&'static str>,
    /// refusals made in the host without an API call
    pub local_refusals: u32,
    pub logs: Arc<Logs>,
    pub origin: Origin,
    pub deadline: Instant,
    pub cancel: Arc<AtomicBool>,
    pub ended: Option<Ended>,
}

impl WasiView for JobState {
    fn ctx(&mut self) -> WasiCtxView<'_> {
        WasiCtxView { ctx: &mut self.wasi, table: &mut self.table }
    }
}

/// The capability names a granted set unlocks, base names only ("plugin-data.read").
pub fn granted_bases(grants: &[String]) -> BTreeSet<String> {
    grants.iter().map(|g| g.split(':').next().unwrap_or("").to_string()).collect()
}

/// Is (interface, function) covered by a granted capability? Ungated interfaces always.
pub fn covers(granted: &BTreeSet<String>, interface: &str, function: &str) -> bool {
    if crate::audit::UNGATED.contains(&interface) {
        return true;
    }
    if interface == "media" && function == "info" {
        return granted.contains("library.read");
    }
    manifest::capabilities().any(|c| {
        c.interface == interface && granted.contains(&c.name) && c.functions.iter().any(|f| f == "*" || f == function)
    })
}

fn to_call_error(e: ApiError) -> CallError {
    match e {
        ApiError::Capability => CallError::Capability,
        ApiError::Scope => CallError::Scope,
        ApiError::Quota => CallError::Quota,
        ApiError::Rate => CallError::Rate,
        ApiError::TooLarge => CallError::TooLarge,
        ApiError::Field => CallError::Field,
        ApiError::Changed => CallError::Changed,
        ApiError::Unavailable => CallError::Unavailable,
        ApiError::NotFound => CallError::NotFound,
        ApiError::Ended => CallError::Ended,
        ApiError::Invalid(s) => CallError::Invalid(s),
    }
}

fn subject_json(s: &Subject) -> Value {
    let kind = match s.kind {
        SubjectKind::Video => "video",
        SubjectKind::Series => "series",
        SubjectKind::Season => "season",
        SubjectKind::Library => "library",
        SubjectKind::Catalog => "catalog",
    };
    json!({"kind": kind, "id": s.id})
}

fn kind_of(k: &str) -> SubjectKind {
    match k {
        "series" => SubjectKind::Series,
        "season" => SubjectKind::Season,
        "library" => SubjectKind::Library,
        "catalog" => SubjectKind::Catalog,
        _ => SubjectKind::Video,
    }
}

impl JobState {
    /// Forwards one call after the local capability check; records refusals.
    fn forward(&mut self, interface: &str, function: &str, body: Value) -> Result<crate::api::Answer, CallError> {
        if !covers(&self.granted, interface, function) {
            self.local_refusals += 1;
            self.refusal.get_or_insert("plugin_capability");
            return Err(CallError::Capability);
        }
        match self.api.call(&self.token, interface, function, &body) {
            Ok(a) => Ok(a),
            Err(e) => {
                if let Some(code) = e.refusal_code() {
                    self.refusal.get_or_insert(code);
                }
                Err(to_call_error(e))
            }
        }
    }

    fn json(&mut self, interface: &str, function: &str, body: Value) -> Result<Value, CallError> {
        self.forward(interface, function, body).map(|a| a.json())
    }

    fn text(&mut self, interface: &str, function: &str, body: Value) -> Result<String, CallError> {
        let v = self.json(interface, function, body)?;
        Ok(match v {
            Value::String(s) => s,
            other => other.to_string(),
        })
    }

    fn unit(&mut self, interface: &str, function: &str, body: Value) -> Result<(), CallError> {
        self.forward(interface, function, body).map(|_| ())
    }

    fn too_large(&self, n: usize, max: usize) -> Result<(), CallError> {
        if n > max {
            Err(CallError::TooLarge)
        } else {
            Ok(())
        }
    }

    pub fn ended(&self) -> bool {
        self.cancel.load(Ordering::Relaxed)
    }
}

impl maktaba::plugin::types::Host for JobState {}

impl maktaba::plugin::job::Host for JobState {
    fn job_subject(&mut self) -> Subject {
        Subject { kind: kind_of(&self.subject.kind), id: self.subject.id.clone() }
    }
    fn payload(&mut self) -> String {
        if self.payload.is_empty() {
            "{}".into()
        } else {
            self.payload.clone()
        }
    }
    fn settings(&mut self) -> String {
        if self.settings.is_empty() {
            "{}".into()
        } else {
            self.settings.clone()
        }
    }
    fn grants(&mut self) -> Vec<String> {
        self.grants.clone()
    }
    fn progress(&mut self, pct: u8) {
        (self.progress)(Some(pct.min(100)), None);
    }
    fn checkpoint(&mut self, state: Vec<u8>) -> Result<(), CallError> {
        self.too_large(state.len(), MAX_VALUE)?;
        (self.progress)(None, Some(&state));
        self.checkpoint = Some(state);
        Ok(())
    }
    fn last_checkpoint(&mut self) -> Option<Vec<u8>> {
        self.last_checkpoint.clone()
    }
    fn scratch_put(&mut self, key: String, value: Vec<u8>) -> Result<(), CallError> {
        if key.len() > 256 {
            return Err(CallError::TooLarge);
        }
        let old = self.scratch.get(&key).map(|v| v.len() as u64).unwrap_or(0);
        let used = self.scratch_used - old + value.len() as u64;
        if used > self.scratch_cap {
            return Err(CallError::Quota);
        }
        self.scratch_used = used;
        self.scratch.insert(key, value);
        Ok(())
    }
    fn scratch_get(&mut self, key: String) -> Option<Vec<u8>> {
        self.scratch.get(&key).cloned()
    }
}

impl maktaba::plugin::log::Host for JobState {
    fn write(&mut self, level: Level, code: String, fields: String) {
        let level = match level {
            Level::Debug => "debug",
            Level::Info => "info",
            Level::Warn => "warn",
            Level::Error => "error",
        };
        self.logs.plugin(&self.origin, level, &code, &fields);
    }
}

impl maktaba::plugin::data::Host for JobState {
    fn get(&mut self, subject: Subject, key: String) -> Result<Option<String>, CallError> {
        let v = self.json("data", "get", json!({"subject": subject_json(&subject), "key": key}))?;
        Ok(v.get("value").and_then(Value::as_str).map(str::to_string))
    }
    fn put(&mut self, subject: Subject, key: String, value: String, exported: bool) -> Result<(), CallError> {
        self.too_large(value.len(), MAX_VALUE)?;
        self.unit(
            "data",
            "put",
            json!({"subject": subject_json(&subject), "key": key, "value": value, "exported": exported}),
        )
    }
    fn delete(&mut self, subject: Subject, key: String) -> Result<(), CallError> {
        self.unit("data", "delete", json!({"subject": subject_json(&subject), "key": key}))
    }
    fn list(&mut self, subject: Subject, prefix: String) -> Result<Vec<String>, CallError> {
        let v = self.json("data", "list", json!({"subject": subject_json(&subject), "prefix": prefix}))?;
        Ok(v.get("keys")
            .and_then(Value::as_array)
            .map(|a| a.iter().filter_map(|x| x.as_str().map(str::to_string)).collect())
            .unwrap_or_default())
    }
}

impl maktaba::plugin::library::Host for JobState {
    fn video(&mut self, id: String) -> Result<String, CallError> {
        self.text("library", "video", json!({"id": id}))
    }
    fn series(&mut self, id: String) -> Result<String, CallError> {
        self.text("library", "series", json!({"id": id}))
    }
    fn list(&mut self, library: String, filter: String, page: u32) -> Result<String, CallError> {
        self.too_large(filter.len(), MAX_VALUE)?;
        self.text("library", "list", json!({"library": library, "filter": filter, "page": page}))
    }
}

impl maktaba::plugin::transcript::Host for JobState {
    fn segments(&mut self, video: String) -> Result<String, CallError> {
        self.text("transcript", "segments", json!({"video": video}))
    }
    fn words(&mut self, video: String) -> Result<String, CallError> {
        self.text("transcript", "words", json!({"video": video}))
    }
}

impl maktaba::plugin::media::Host for JobState {
    fn open(&mut self, video: String) -> Result<u64, CallError> {
        let v = self.json("media", "open", json!({"video": video}))?;
        v.get("handle").and_then(Value::as_u64).ok_or(CallError::Invalid("no handle".into()))
    }
    fn read(&mut self, handle: u64, offset: u64, len: u32) -> Result<Vec<u8>, CallError> {
        if len > MAX_READ {
            return Err(CallError::TooLarge);
        }
        self.forward("media", "read", json!({"handle": handle, "offset": offset, "len": len})).map(|a| a.bytes())
    }
    fn size(&mut self, handle: u64) -> Result<u64, CallError> {
        let v = self.json("media", "size", json!({"handle": handle}))?;
        v.get("size").and_then(Value::as_u64).ok_or(CallError::Invalid("no size".into()))
    }
    fn close(&mut self, handle: u64) {
        let _ = self.forward("media", "close", json!({"handle": handle}));
    }
    fn frame(&mut self, video: String, t_ms: u64, max_edge: u32) -> Result<Vec<u8>, CallError> {
        if max_edge > 1280 {
            return Err(CallError::TooLarge);
        }
        self.forward("media", "frame", json!({"video": video, "t_ms": t_ms, "max_edge": max_edge})).map(|a| a.bytes())
    }
    fn audio(&mut self, video: String, t0_ms: u64, t1_ms: u64, rate: u32) -> Result<Vec<u8>, CallError> {
        if t1_ms.saturating_sub(t0_ms) > 600_000 {
            return Err(CallError::TooLarge);
        }
        self.forward("media", "audio", json!({"video": video, "t0_ms": t0_ms, "t1_ms": t1_ms, "rate": rate}))
            .map(|a| a.bytes())
    }
    fn info(&mut self, video: String) -> Result<String, CallError> {
        self.text("media", "info", json!({"video": video}))
    }
}

impl maktaba::plugin::write::Host for JobState {
    fn set_field(&mut self, video: String, field: String, value: String) -> Result<(), CallError> {
        self.too_large(value.len(), MAX_VALUE)?;
        self.unit("write", "set-field", json!({"video": video, "field": field, "value": value}))
    }
    fn add_tags(&mut self, video: String, tags: Vec<String>) -> Result<(), CallError> {
        self.too_large(tags.iter().map(String::len).sum(), MAX_VALUE)?;
        self.unit("write", "add-tags", json!({"video": video, "tags": tags}))
    }
    fn set_chapters(&mut self, video: String, chapters: Vec<Chapter>) -> Result<(), CallError> {
        self.too_large(chapters.iter().map(|c| c.title.len() + 16).sum(), MAX_VALUE)?;
        let list: Vec<Value> = chapters.iter().map(|c| json!({"start_ms": c.start_ms, "title": c.title})).collect();
        self.unit("write", "set-chapters", json!({"video": video, "chapters": list}))
    }
    fn add_subtitle(&mut self, video: String, lang: String, text: String) -> Result<(), CallError> {
        self.too_large(text.len(), MAX_ARTIFACT)?;
        self.unit("write", "add-subtitle", json!({"video": video, "lang": lang, "text": text}))
    }
    fn set_artwork(&mut self, subject: Subject, kind: String, image: Vec<u8>) -> Result<(), CallError> {
        self.too_large(image.len(), MAX_ARTIFACT)?;
        self.unit(
            "write",
            "set-artwork",
            json!({"subject": subject_json(&subject), "kind": kind, "image": crate::protocol::b64(&image)}),
        )
    }
}

impl maktaba::plugin::suggest::Host for JobState {
    fn add(&mut self, subject: Subject, field: String, value: String, reason: String) -> Result<(), CallError> {
        self.too_large(value.len() + reason.len(), MAX_VALUE)?;
        self.unit(
            "suggest",
            "add",
            json!({"subject": subject_json(&subject), "field": field, "value": value, "reason": reason}),
        )
    }
}

impl maktaba::plugin::queue::Host for JobState {
    fn request(&mut self, stage: String, subject: Subject, payload: String) -> Result<(), CallError> {
        self.too_large(payload.len(), MAX_VALUE)?;
        if self.requests.len() >= MAX_REQUESTS {
            self.refusal.get_or_insert("plugin_quota");
            return Err(CallError::Quota);
        }
        let s = subject_json(&subject);
        self.unit("queue", "request", json!({"stage": stage, "subject": s, "payload": payload}))?;
        self.requests.push(QueueRequest {
            stage,
            subject: PSubject { kind: s["kind"].as_str().unwrap_or("video").into(), id: subject.id },
            payload,
        });
        Ok(())
    }
}

impl maktaba::plugin::notify::Host for JobState {
    fn admin(&mut self, code: String, params: String) -> Result<(), CallError> {
        self.too_large(params.len(), MAX_VALUE)?;
        self.unit("notify", "admin", json!({"code": code, "params": params}))
    }
}

impl maktaba::plugin::exports::Host for JobState {
    fn read(&mut self, plugin: String, subject: Subject, key: String) -> Result<Option<String>, CallError> {
        // the local check looks for plugin-data.read:<that plugin> exactly
        if !self.grants.iter().any(|g| g == &format!("plugin-data.read:{plugin}")) {
            self.local_refusals += 1;
            self.refusal.get_or_insert("plugin_capability");
            return Err(CallError::Capability);
        }
        let v =
            self.json("exports", "read", json!({"plugin": plugin, "subject": subject_json(&subject), "key": key}))?;
        Ok(v.get("value").and_then(Value::as_str).map(str::to_string))
    }
}

impl maktaba::plugin::compute::Host for JobState {
    fn embed(&mut self, texts: Vec<String>) -> Result<Embeddings, CallError> {
        if texts.len() > MAX_EMBED_TEXTS || texts.iter().any(|t| t.chars().count() > MAX_EMBED_CHARS) {
            return Err(CallError::TooLarge);
        }
        let v = self.json("compute", "embed", json!({"texts": texts}))?;
        let model = v.get("model").and_then(Value::as_str).unwrap_or("").to_string();
        let vectors = v
            .get("vectors")
            .and_then(Value::as_array)
            .map(|rows| {
                rows.iter()
                    .map(|r| {
                        r.as_array()
                            .map(|xs| xs.iter().map(|x| x.as_f64().unwrap_or(0.0) as f32).collect())
                            .unwrap_or_default()
                    })
                    .collect()
            })
            .unwrap_or_default();
        Ok(Embeddings { model, vectors })
    }

    fn generate(&mut self, prompt: String, options: GenerateOptions) -> Result<String, CallError> {
        self.too_large(prompt.len() + options.json_schema.as_ref().map_or(0, String::len), MAX_PROMPT)?;
        if options.max_tokens == 0 || options.max_tokens > MAX_TOKENS {
            return Err(CallError::TooLarge);
        }
        let v = self.json(
            "compute",
            "generate",
            json!({"prompt": prompt, "max_tokens": options.max_tokens,
                   "temperature": options.temperature, "json_schema": options.json_schema}),
        )?;
        Ok(v.get("text").and_then(Value::as_str).unwrap_or("").to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn coverage_follows_capabilities_toml() {
        let g = granted_bases(&["tags.write".into(), "media.audio".into(), "plugin-data.read:dev.example.x".into()]);
        assert!(covers(&g, "write", "add-tags"));
        assert!(!covers(&g, "write", "set-field"));
        assert!(covers(&g, "media", "audio"));
        assert!(!covers(&g, "media", "open"));
        assert!(!covers(&g, "media", "info"));
        assert!(covers(&g, "job", "anything"));
        assert!(covers(&g, "data", "put"));
        assert!(covers(&g, "exports", "read"));
        assert!(!covers(&g, "library", "video"));
        assert!(!covers(&g, "compute", "embed"));
        let g = granted_bases(&["compute.embed".into()]);
        assert!(covers(&g, "compute", "embed"));
        assert!(!covers(&g, "compute", "generate"));
    }
}
