//! The one door out of the host: the plugin API on the home's unix socket (story 54.04).
//!
//! Every `maktaba:plugin` import the guest calls becomes `POST /plugin-api/v1/<interface>/
//! <function>` with `Authorization: Plugin <token>` (the token the guest never sees). The
//! API makes every decision; the host only forwards. HTTP/1.1 is written by hand here so
//! the host carries no HTTP stack and no TCP code at all: the only transport is a unix
//! socket path.

use std::io::{BufRead, BufReader, Read, Write};
use std::path::{Path, PathBuf};
use std::time::Duration;

use serde_json::Value;

/// What a refused or failed call answers; the WIT `call-error`, one to one.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ApiError {
    Capability,
    Scope,
    Quota,
    Rate,
    TooLarge,
    Field,
    Changed,
    Unavailable,
    NotFound,
    Ended,
    /// a request or event that would loop (story 54.40): the guest sees `scope`
    Loop,
    Invalid(String),
}

impl ApiError {
    /// From the API's refusal code (`{"code": …}`).
    pub fn from_code(code: &str) -> Self {
        match code {
            "plugin_capability" => Self::Capability,
            "plugin_scope" => Self::Scope,
            "plugin_quota" => Self::Quota,
            "plugin_loop" => Self::Loop,
            "plugin_rate" => Self::Rate,
            "plugin_too_large" => Self::TooLarge,
            "plugin_field" => Self::Field,
            "media_changed" => Self::Changed,
            "media_unavailable" => Self::Unavailable,
            "not_found" => Self::NotFound,
            "plugin_token" | "plugin_grants_changed" => Self::Ended,
            other => Self::Invalid(other.to_string()),
        }
    }

    /// The refusal code a job result carries (54.06 §3), for the refusals that fail a job.
    pub fn refusal_code(&self) -> Option<&'static str> {
        match self {
            Self::Capability => Some("plugin_capability"),
            Self::Scope => Some("plugin_scope"),
            Self::Quota => Some("plugin_quota"),
            Self::Loop => Some("plugin_loop"),
            _ => None,
        }
    }
}

/// An answer: JSON, or raw bytes (media reads, frames, audio).
#[derive(Debug, Clone, PartialEq)]
pub enum Answer {
    Json(Value),
    Bytes(Vec<u8>),
}

impl Answer {
    pub fn json(self) -> Value {
        match self {
            Answer::Json(v) => v,
            Answer::Bytes(_) => Value::Null,
        }
    }
    pub fn bytes(self) -> Vec<u8> {
        match self {
            Answer::Bytes(b) => b,
            Answer::Json(_) => Vec::new(),
        }
    }
}

/// The plugin API as the host sees it. The real one is [`UnixApi`]; tests and the SDK's
/// harness (54.12) give a mock.
pub trait PluginApi: Send + Sync {
    fn call(&self, token: &str, interface: &str, function: &str, body: &Value) -> Result<Answer, ApiError>;
}

/// The plugin API over `<data>/run/plugin-api.sock`.
pub struct UnixApi {
    path: PathBuf,
    timeout: Duration,
}

impl UnixApi {
    pub fn new(path: impl Into<PathBuf>) -> Self {
        Self { path: path.into(), timeout: Duration::from_secs(60) }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }
}

/// The largest answer the host reads (an 8 MB media read plus headroom).
const MAX_ANSWER: usize = 9 << 20;

impl PluginApi for UnixApi {
    fn call(&self, token: &str, interface: &str, function: &str, body: &Value) -> Result<Answer, ApiError> {
        let mut s = crate::uds::connect(&self.path).map_err(|_| ApiError::Unavailable)?;
        let _ = crate::uds::set_timeouts(&s, self.timeout);
        let payload = serde_json::to_vec(body).map_err(|e| ApiError::Invalid(e.to_string()))?;
        let head = format!(
            "POST /plugin-api/v1/{interface}/{function} HTTP/1.1\r\nHost: plugin-api\r\nAuthorization: Plugin {token}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
            payload.len()
        );
        s.write_all(head.as_bytes()).map_err(|_| ApiError::Unavailable)?;
        s.write_all(&payload).map_err(|_| ApiError::Unavailable)?;
        read_answer(&mut BufReader::new(s))
    }
}

/// Reads one HTTP/1.1 answer: status, headers, a Content-Length body (or to the end).
pub fn read_answer(r: &mut impl BufRead) -> Result<Answer, ApiError> {
    let mut line = String::new();
    r.read_line(&mut line).map_err(|_| ApiError::Unavailable)?;
    let status: u16 = line.split_whitespace().nth(1).and_then(|s| s.parse().ok()).ok_or(ApiError::Unavailable)?;
    let mut len: Option<usize> = None;
    let mut binary = false;
    loop {
        line.clear();
        if r.read_line(&mut line).map_err(|_| ApiError::Unavailable)? == 0 {
            return Err(ApiError::Unavailable);
        }
        let l = line.trim_end();
        if l.is_empty() {
            break;
        }
        if let Some((k, v)) = l.split_once(':') {
            let (k, v) = (k.trim().to_ascii_lowercase(), v.trim());
            if k == "content-length" {
                len = v.parse().ok();
            } else if k == "content-type" {
                binary = v.starts_with("application/octet-stream");
            }
        }
    }
    let mut body = Vec::new();
    match len {
        Some(n) if n > MAX_ANSWER => return Err(ApiError::TooLarge),
        Some(n) => {
            body.resize(n, 0);
            r.read_exact(&mut body).map_err(|_| ApiError::Unavailable)?;
        }
        None => {
            r.take(MAX_ANSWER as u64 + 1).read_to_end(&mut body).map_err(|_| ApiError::Unavailable)?;
            if body.len() > MAX_ANSWER {
                return Err(ApiError::TooLarge);
            }
        }
    }
    if (200..300).contains(&status) {
        if binary {
            return Ok(Answer::Bytes(body));
        }
        if body.is_empty() {
            return Ok(Answer::Json(Value::Null));
        }
        return serde_json::from_slice(&body).map(Answer::Json).map_err(|e| ApiError::Invalid(e.to_string()));
    }
    let code = serde_json::from_slice::<Value>(&body)
        .ok()
        .and_then(|v| v.get("code").and_then(Value::as_str).map(str::to_string))
        .unwrap_or_else(|| format!("http_{status}"));
    Err(match status {
        413 => ApiError::TooLarge,
        404 if code.starts_with("http_") => ApiError::NotFound,
        503 if code.starts_with("http_") => ApiError::Unavailable,
        _ => ApiError::from_code(&code),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn answer(raw: &str) -> Result<Answer, ApiError> {
        read_answer(&mut BufReader::new(raw.as_bytes()))
    }

    #[test]
    fn json_answer() {
        let a = answer("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 11\r\n\r\n{\"a\":true}\n")
            .unwrap();
        assert_eq!(a, Answer::Json(serde_json::json!({"a": true})));
    }

    #[test]
    fn bytes_answer() {
        let a = answer("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: 3\r\n\r\nabc")
            .unwrap();
        assert_eq!(a, Answer::Bytes(b"abc".to_vec()));
    }

    #[test]
    fn refusals_map_to_call_errors() {
        for (status, code, want) in [
            (403, "plugin_capability", ApiError::Capability),
            (403, "plugin_scope", ApiError::Scope),
            (403, "plugin_quota", ApiError::Quota),
            (403, "plugin_loop", ApiError::Loop),
            (403, "plugin_rate", ApiError::Rate),
            (403, "plugin_field", ApiError::Field),
            (401, "plugin_grants_changed", ApiError::Ended),
            (401, "plugin_token", ApiError::Ended),
            (409, "media_changed", ApiError::Changed),
            (503, "media_unavailable", ApiError::Unavailable),
        ] {
            let body = format!("{{\"code\":\"{code}\"}}");
            let raw = format!("HTTP/1.1 {status} X\r\nContent-Length: {}\r\n\r\n{body}", body.len());
            assert_eq!(answer(&raw).unwrap_err(), want, "{code}");
        }
        assert_eq!(answer("HTTP/1.1 413 X\r\nContent-Length: 0\r\n\r\n").unwrap_err(), ApiError::TooLarge);
    }

    #[test]
    fn oversize_answers_are_refused() {
        let raw = format!("HTTP/1.1 200 OK\r\nContent-Length: {}\r\n\r\n", MAX_ANSWER + 1);
        assert_eq!(answer(&raw).unwrap_err(), ApiError::TooLarge);
    }
}
