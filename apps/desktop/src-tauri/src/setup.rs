// First-run setup steps (Epic 36, story 36.6).
//
// Each setup screen runs one step of `maktaba-server setup --json --step
// <name>` (specs/epics/36-mac-app/contract-setup.md): the input is one
// JSON object on stdin, the output is NDJSON on stdout — `progress`
// lines, then one `result` or `error`. Secrets (the admin password, the
// Hugging Face token) only travel on stdin and are never logged here.
use std::collections::VecDeque;
use std::io::{BufRead, BufReader, Read, Write};
use std::path::Path;
use std::process::{Command, Stdio};

use serde::{Deserialize, Serialize};
use serde_json::Value;

/// The steps the app may run, in screen order.
pub const STEPS: &[&str] = &[
    "status",
    "check",
    "admin",
    "libraries",
    "models",
    "plex",
    "venv",
    "llm",
    "finish",
];

/// How many stderr lines are kept for the "details" box of an error.
const STDERR_TAIL: usize = 50;

/// One NDJSON line from a step.
#[derive(Debug, Clone, PartialEq, Deserialize)]
#[serde(tag = "type", rename_all = "lowercase")]
pub enum Event {
    Progress(Progress),
    Result {
        #[serde(default)]
        data: Value,
    },
    Error {
        code: String,
        #[serde(default)]
        message: String,
        #[serde(default)]
        field: Option<String>,
        /// The values the screen puts into the code's own text.
        #[serde(default)]
        params: Option<Value>,
    },
}

/// A progress line, relayed to the screen as-is.
#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
pub struct Progress {
    #[serde(default)]
    pub stage: String,
    #[serde(default)]
    pub message: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub done: Option<u64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub total: Option<u64>,
}

/// A failed step, as the screen gets it.
#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct StepError {
    pub code: String,
    pub message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub field: Option<String>,
    /// The values the screen puts into the code's own text (setup.error.<code>), boxed: a
    /// step's `Result` stays small (clippy's result_large_err).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub params: Option<Box<Value>>,
    /// The step's last stderr lines, for the "details" box.
    pub details: Vec<String>,
}

impl StepError {
    fn new(code: &str, message: impl Into<String>) -> Self {
        Self {
            code: code.into(),
            message: message.into(),
            field: None,
            params: None,
            details: Vec::new(),
        }
    }
}

/// Parse one stdout line; blank lines and lines that are not an event
/// (a stray print) are skipped rather than failing the step.
pub fn parse_line(line: &str) -> Option<Event> {
    let line = line.trim();
    if line.is_empty() {
        return None;
    }
    serde_json::from_str(line).ok()
}

/// Run one step with `bin` (the bundled maktaba-server) and return its
/// result data. `on_progress` gets every progress line as it arrives.
pub fn run_step(
    bin: &Path,
    step: &str,
    input: &Value,
    mut on_progress: impl FnMut(Progress),
) -> Result<Value, StepError> {
    if !STEPS.contains(&step) {
        return Err(StepError::new(
            "unknown_step",
            format!("unknown setup step {step:?}"),
        ));
    }
    if !input.is_object() {
        return Err(StepError::new(
            "bad_input",
            "a step's input is a JSON object",
        ));
    }
    let mut child = Command::new(bin)
        .args(["setup", "--json", "--step", step])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| {
            StepError::new(
                "server_missing",
                format!("cannot run {}: {e}", bin.display()),
            )
        })?;

    // stdin: the input, then EOF. A step that exits without reading it
    // closes the pipe; the exit code and events tell what happened.
    if let Some(mut stdin) = child.stdin.take() {
        let _ = stdin.write_all(input.to_string().as_bytes());
    }

    // stderr is drained on a thread so a chatty step cannot block on a
    // full pipe while we read stdout.
    let stderr = child.stderr.take();
    let tail = std::thread::spawn(move || {
        let mut tail: VecDeque<String> = VecDeque::new();
        if let Some(s) = stderr {
            for line in BufReader::new(s).lines().map_while(Result::ok) {
                if tail.len() == STDERR_TAIL {
                    tail.pop_front();
                }
                tail.push_back(line);
            }
        }
        Vec::from(tail)
    });

    let mut outcome: Option<Result<Value, StepError>> = None;
    if let Some(stdout) = child.stdout.take() {
        // one line is never more than 1 MB; a longer one is cut and skipped
        let mut reader = BufReader::new(stdout);
        let mut buf = Vec::new();
        loop {
            buf.clear();
            match (&mut reader).take(1 << 20).read_until(b'\n', &mut buf) {
                Ok(0) | Err(_) => break,
                Ok(_) => {}
            }
            match parse_line(&String::from_utf8_lossy(&buf)) {
                Some(Event::Progress(p)) => on_progress(p),
                Some(Event::Result { data }) if outcome.is_none() => outcome = Some(Ok(data)),
                Some(Event::Error {
                    code,
                    message,
                    field,
                    params,
                }) if outcome.is_none() => {
                    outcome = Some(Err(StepError {
                        code,
                        message,
                        field,
                        params: params.map(Box::new),
                        details: Vec::new(),
                    }))
                }
                _ => {}
            }
        }
    }
    let status = child.wait();
    let details = tail.join().unwrap_or_default();
    let ok = matches!(&status, Ok(s) if s.success());
    match outcome {
        Some(Ok(data)) if ok => Ok(data),
        Some(Err(mut e)) => {
            e.details = details;
            Err(e)
        }
        _ => {
            let how = match status {
                Ok(s) => s.code().map_or("was killed".to_string(), |c| {
                    format!("exited with code {c}")
                }),
                Err(e) => format!("could not be waited for: {e}"),
            };
            let mut e = StepError::new(
                "no_result",
                format!("setup step {step} {how} without a result"),
            );
            e.details = details;
            Err(e)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::os::unix::fs::PermissionsExt;

    /// A stand-in for maktaba-server that speaks the contract.
    fn stub(dir: &Path, body: &str) -> std::path::PathBuf {
        let p = dir.join("maktaba-server");
        std::fs::write(&p, format!("#!/bin/sh\n{body}\n")).unwrap();
        std::fs::set_permissions(&p, std::fs::Permissions::from_mode(0o755)).unwrap();
        p
    }

    #[test]
    fn parses_each_event_kind() {
        assert_eq!(
            parse_line(
                r#"{"type":"progress","stage":"download","message":"m","done":1,"total":2}"#
            ),
            Some(Event::Progress(Progress {
                stage: "download".into(),
                message: "m".into(),
                done: Some(1),
                total: Some(2)
            }))
        );
        assert_eq!(
            parse_line(r#"{"type":"result","data":{"ok":true}}"#),
            Some(Event::Result {
                data: json!({"ok": true})
            })
        );
        assert_eq!(
            parse_line(
                r#"{"type":"error","code":"password_short","message":"Too short","field":"password","params":{"min":8}}"#
            ),
            Some(Event::Error {
                code: "password_short".into(),
                message: "Too short".into(),
                field: Some("password".into()),
                params: Some(json!({"min": 8}))
            })
        );
        assert_eq!(
            parse_line(r#"{"type":"error","code":"network","message":"m"}"#),
            Some(Event::Error {
                code: "network".into(),
                message: "m".into(),
                field: None,
                params: None
            })
        );
        assert_eq!(parse_line(""), None);
        assert_eq!(parse_line("Downloading…"), None);
        assert_eq!(parse_line(r#"{"type":"other"}"#), None);
    }

    #[test]
    fn a_step_gets_its_input_on_stdin_and_its_name_in_argv() {
        let tmp = tempfile::tempdir().unwrap();
        // echo argv and stdin back in the result, the way a real step reads them
        let bin = stub(
            tmp.path(),
            r#"read -r input
printf '{"type":"progress","stage":"one","message":"first"}\n'
printf 'stray line\n'
printf '{"type":"result","data":{"argv":"%s","input":%s}}\n' "$*" "$input""#,
        );
        let mut seen = Vec::new();
        let out = run_step(
            &bin,
            "admin",
            &json!({"username": "admin", "password": "s3cret"}),
            |p| seen.push(p.stage),
        )
        .unwrap();
        assert_eq!(out["argv"], "setup --json --step admin");
        assert_eq!(
            out["input"],
            json!({"username": "admin", "password": "s3cret"})
        );
        assert_eq!(seen, ["one"]);
    }

    #[test]
    fn an_error_event_keeps_code_field_and_stderr_tail() {
        let tmp = tempfile::tempdir().unwrap();
        let bin = stub(
            tmp.path(),
            r#"echo "checking token" >&2
printf '{"type":"error","code":"hf_gated","message":"Accept the terms","field":"hf_token"}\n'
exit 1"#,
        );
        let e = run_step(&bin, "models", &json!({}), |_| {}).unwrap_err();
        assert_eq!(e.code, "hf_gated");
        assert_eq!(e.field.as_deref(), Some("hf_token"));
        assert_eq!(e.details, ["checking token"]);
    }

    #[test]
    fn exiting_without_a_result_is_an_error() {
        let tmp = tempfile::tempdir().unwrap();
        let bin = stub(tmp.path(), "echo boom >&2; exit 3");
        let e = run_step(&bin, "check", &json!({}), |_| {}).unwrap_err();
        assert_eq!(e.code, "no_result");
        assert!(e.message.contains("exited with code 3"), "{}", e.message);
        assert_eq!(e.details, ["boom"]);
        // a result with a failing exit code is not trusted either
        let bin = stub(
            tmp.path(),
            r#"printf '{"type":"result","data":{}}\n'; exit 1"#,
        );
        assert_eq!(
            run_step(&bin, "check", &json!({}), |_| {})
                .unwrap_err()
                .code,
            "no_result"
        );
    }

    #[test]
    fn unknown_steps_bad_input_and_a_missing_binary_are_refused() {
        let missing = Path::new("/nonexistent/maktaba-server");
        assert_eq!(
            run_step(missing, "rm -rf", &json!({}), |_| {})
                .unwrap_err()
                .code,
            "unknown_step"
        );
        assert_eq!(
            run_step(missing, "check", &json!([1]), |_| {})
                .unwrap_err()
                .code,
            "bad_input"
        );
        assert_eq!(
            run_step(missing, "check", &json!({}), |_| {})
                .unwrap_err()
                .code,
            "server_missing"
        );
    }

    #[test]
    fn a_step_that_ignores_stdin_still_works() {
        let tmp = tempfile::tempdir().unwrap();
        let bin = stub(
            tmp.path(),
            r#"exec 0<&-; printf '{"type":"result","data":{"configured":false}}\n'"#,
        );
        let out = run_step(&bin, "status", &json!({"big": "x".repeat(1 << 17)}), |_| {}).unwrap();
        assert_eq!(out, json!({"configured": false}));
    }
}
