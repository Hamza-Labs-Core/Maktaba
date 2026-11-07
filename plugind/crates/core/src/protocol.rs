//! The dispatch protocol on `<data>/run/plugind.sock` (story 54.02 §2).
//!
//! Frames are a 4-byte big-endian length and one JSON object with a `type`. (The story
//! names protobuf; JSON frames keep the host, the pipeline and the SDK's harness free of a
//! code generator, and the messages are small. Bytes travel as base64.) A connection
//! sends one request; a `run` gets `progress` frames and then one `result`, every other
//! request one answer.

use std::io::{self, Read, Write};

use base64::Engine as _;
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// The largest frame either side accepts.
pub const MAX_FRAME: usize = 4 << 20;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Request {
    Run(Box<Run>),
    Cancel {
        invocation_id: String,
    },
    Preload {
        plugin_id: String,
        version: String,
        package_path: String,
    },
    Unload {
        plugin_id: String,
        version: String,
    },
    /// `/healthz` and `/readyz` (Epic 53) on the dispatch socket.
    Health,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
pub struct Subject {
    pub kind: String,
    pub id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
pub struct Job {
    pub id: String,
    pub stage: String,
    pub subject: Subject,
    /// JSON text
    #[serde(default)]
    pub payload: String,
    #[serde(default)]
    pub attempt: u32,
    /// base64
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub checkpoint: Option<String>,
}

/// What the dispatcher asks for; the host clamps each to the manifest (54.02 §2: a
/// dispatcher cannot raise them).
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Default)]
pub struct Limits {
    #[serde(default)]
    pub memory_mb: u64,
    #[serde(default)]
    pub timeout_ms: u64,
    /// 0: derived from the timeout
    #[serde(default)]
    pub fuel: u64,
    #[serde(default)]
    pub scratch_mb: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
pub struct Run {
    pub invocation_id: String,
    pub plugin_id: String,
    pub version: String,
    pub package_path: String,
    /// the manifest stage's `export`; the host calls `run(stage)` (the SDK dispatches)
    #[serde(default)]
    pub export: String,
    pub job: Job,
    /// JSON text
    #[serde(default)]
    pub settings: String,
    #[serde(default)]
    pub grants: Vec<String>,
    /// opaque, from the plugin API (54.04); never shown to the guest
    pub token: String,
    #[serde(default)]
    pub limits: Limits,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Outcome {
    Ok,
    /// the guest trapped (a panic, an unreachable, a stack overflow)
    Trap,
    /// the deadline or the fuel ran out
    Timeout,
    /// the memory or table cap was hit
    Memory,
    /// refused: a capability, scope or quota refusal ended the job, or it was cancelled
    Refused,
    /// the guest returned its own error
    Failed,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct QueueRequest {
    pub stage: String,
    pub subject: Subject,
    /// JSON text
    pub payload: String,
}

/// An event the job emitted (story 54.40): delivered to `stages`, the subscribing stages
/// the plugin API named, when the job ends well.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct EmittedEvent {
    pub name: String,
    pub subject: Subject,
    /// JSON text
    pub payload: String,
    #[serde(default)]
    pub stages: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Response {
    Progress {
        invocation_id: String,
        pct: u8,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        checkpoint: Option<String>,
    },
    Result(RunResult),
    Preloaded {
        plugin_id: String,
        version: String,
        ok: bool,
        #[serde(default, skip_serializing_if = "String::is_empty")]
        error_code: String,
        #[serde(default, skip_serializing_if = "String::is_empty")]
        detail: String,
    },
    Ack {
        ok: bool,
    },
    Health(Health),
    Error {
        error_code: String,
        detail: String,
    },
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct RunResult {
    pub invocation_id: String,
    pub outcome: Outcome,
    #[serde(default)]
    pub output_hash: String,
    /// JSON text, <= 64 KB
    #[serde(default)]
    pub summary: String,
    #[serde(default)]
    pub requests: Vec<QueueRequest>,
    #[serde(default)]
    pub events: Vec<EmittedEvent>,
    #[serde(default)]
    pub log_dropped: u64,
    #[serde(default)]
    pub fuel_used: u64,
    #[serde(default)]
    pub peak_memory: u64,
    #[serde(default)]
    pub wall_ms: u64,
    /// plugin_trap, plugin_timeout, plugin_memory, plugin_capability, plugin_scope,
    /// plugin_quota, plugin_failed, cancelled, plugin_load (and limits_clamped as a note)
    #[serde(default)]
    pub error_code: String,
    #[serde(default)]
    pub detail: String,
    /// base64 of the last checkpoint, returned on cancel and failure
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub checkpoint: Option<String>,
    #[serde(default)]
    pub limits_clamped: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
pub struct Health {
    pub ok: bool,
    pub ready: bool,
    pub wasmtime: String,
    pub plugin_api: String,
    /// "full", "partial" (Degraded, sandbox_partial) or "none" (tests only)
    pub sandbox: String,
    #[serde(default)]
    pub sandbox_missing: Vec<String>,
    pub loaded: usize,
    pub running: usize,
}

pub fn b64(b: &[u8]) -> String {
    base64::engine::general_purpose::STANDARD.encode(b)
}

pub fn unb64(s: &str) -> Option<Vec<u8>> {
    base64::engine::general_purpose::STANDARD.decode(s).ok()
}

pub fn write_frame<T: Serialize>(w: &mut impl Write, msg: &T) -> io::Result<()> {
    let body = serde_json::to_vec(msg).map_err(io::Error::other)?;
    if body.len() > MAX_FRAME {
        return Err(io::Error::new(io::ErrorKind::InvalidData, "frame too large"));
    }
    w.write_all(&(body.len() as u32).to_be_bytes())?;
    w.write_all(&body)?;
    w.flush()
}

pub fn read_frame<T: for<'de> Deserialize<'de>>(r: &mut impl Read) -> io::Result<T> {
    let mut len = [0u8; 4];
    r.read_exact(&mut len)?;
    let n = u32::from_be_bytes(len) as usize;
    if n > MAX_FRAME {
        return Err(io::Error::new(io::ErrorKind::InvalidData, "frame too large"));
    }
    let mut body = vec![0u8; n];
    r.read_exact(&mut body)?;
    serde_json::from_slice(&body).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
}

/// A frame as loose JSON (tests, logs).
pub fn read_value(r: &mut impl Read) -> io::Result<Value> {
    read_frame(r)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frames_round_trip() {
        let req = Request::Run(Box::new(Run {
            invocation_id: "i1".into(),
            plugin_id: "dev.example.x".into(),
            token: "t".into(),
            job: Job { id: "j".into(), stage: "s".into(), ..Default::default() },
            ..Default::default()
        }));
        let mut buf = Vec::new();
        write_frame(&mut buf, &req).unwrap();
        let back: Request = read_frame(&mut buf.as_slice()).unwrap();
        assert_eq!(back, req);
        let v: Value = serde_json::from_slice(&buf[4..]).unwrap();
        assert_eq!(v["type"], "run");
    }

    #[test]
    fn oversize_frames_are_refused() {
        let mut buf = ((MAX_FRAME + 1) as u32).to_be_bytes().to_vec();
        buf.extend_from_slice(b"{}");
        assert!(read_frame::<Value>(&mut buf.as_slice()).is_err());
    }
}
