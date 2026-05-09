package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// Note: tests in this file are deliberately NOT marked t.Parallel().
// Several touch package-level globals (Default, levelVar) and the race
// detector — correctly — flags concurrent writes. The suite runs in
// well under a second, so sequential execution is fine.

// TestJSONLineHasRequiredFields exercises AC2: every line carries ts,
// level, service, msg, version, env. (TC2 round-trip.)
func TestJSONLineHasRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(Options{
		Service: "api",
		Env:     "prod",
		Version: "v1.2.3",
		Output:  &buf,
	})
	logger.Info("hello", "k", "v")

	got := decodeOne(t, buf.Bytes())
	for _, k := range []string{"ts", "level", "service", "msg", "version", "env"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing required field %q in %q", k, buf.String())
		}
	}
	if got["service"] != "api" {
		t.Errorf("service = %v, want api", got["service"])
	}
	if got["version"] != "v1.2.3" {
		t.Errorf("version = %v, want v1.2.3", got["version"])
	}
	if got["env"] != "prod" {
		t.Errorf("env = %v, want prod", got["env"])
	}
	if got["level"] != "info" {
		t.Errorf("level = %v, want info (lowercase)", got["level"])
	}
	if got["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", got["msg"])
	}
}

// TestContextFieldsInjected exercises the `From(ctx)` pattern: ids
// stashed on the context appear in the emitted record.
func TestContextFieldsInjected(t *testing.T) {
	var buf bytes.Buffer
	saved := Default
	Default = NewLogger(Options{Service: "api", Env: "prod", Version: "v0", Output: &buf})
	t.Cleanup(func() { Default = saved })

	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-1")
	ctx = WithSessionID(ctx, "sess-2")
	ctx = WithJobID(ctx, "job-3")
	ctx = WithVideoID(ctx, "vid-4")
	ctx = WithUserID(ctx, "user-5")
	From(ctx).Info("ping")

	got := decodeOne(t, buf.Bytes())
	for k, want := range map[string]string{
		"request_id": "req-1",
		"session_id": "sess-2",
		"job_id":     "job-3",
		"video_id":   "vid-4",
		"user_id":    "user-5",
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
}

// TestRedactionMasksSensitiveFields verifies that the default redaction
// list masks values whose key matches (case-insensitive).
func TestRedactionMasksSensitiveFields(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(Options{Service: "api", Env: "prod", Version: "v0", Output: &buf})
	logger.Info("auth attempt",
		"username", "alice",
		"password", "hunter2",
		"Authorization", "Bearer xyz",
		"api_key", "k1",
		"request_id", "ok-to-log",
	)

	got := decodeOne(t, buf.Bytes())
	if got["password"] != redactedValue {
		t.Errorf("password not redacted: %v", got["password"])
	}
	if got["Authorization"] != redactedValue {
		t.Errorf("Authorization not redacted (case-insensitive): %v", got["Authorization"])
	}
	if got["api_key"] != redactedValue {
		t.Errorf("api_key not redacted: %v", got["api_key"])
	}
	// Non-sensitive fields pass through.
	if got["username"] != "alice" {
		t.Errorf("username should pass through: %v", got["username"])
	}
	if got["request_id"] != "ok-to-log" {
		t.Errorf("request_id should pass through: %v", got["request_id"])
	}
}

// TestSetLevelFiltersDebug verifies that debug lines are filtered out
// at info level and emitted after SetLevel(LevelDebug). (Backs TC3 in
// the abstract — the SIGUSR1 wiring just calls SetLevel.)
func TestSetLevelFiltersDebug(t *testing.T) {
	var buf bytes.Buffer
	saved := Default
	Default = NewLogger(Options{Service: "api", Env: "prod", Version: "v0", Output: &buf, Level: slog.LevelInfo})
	t.Cleanup(func() {
		Default = saved
		levelVar.Set(slog.LevelInfo)
	})

	Default.Debug("first-debug")
	if strings.Contains(buf.String(), "first-debug") {
		t.Fatalf("debug line emitted at info level: %s", buf.String())
	}

	SetLevel(slog.LevelDebug)
	buf.Reset()
	Default.Debug("second-debug")
	if !strings.Contains(buf.String(), "second-debug") {
		t.Fatalf("debug line NOT emitted at debug level: %s", buf.String())
	}
}

// TestRTLUnicodeRoundTrip exercises EC3: RTL Arabic content survives
// the JSON encode/decode cycle byte-for-byte.
func TestRTLUnicodeRoundTrip(t *testing.T) {
	const title = "كتاب الفهرست"
	var buf bytes.Buffer
	logger := NewLogger(Options{Service: "api", Env: "prod", Version: "v0", Output: &buf})
	logger.Info("rtl test", "title", title)

	got := decodeOne(t, buf.Bytes())
	if got["title"] != title {
		t.Errorf("title round-trip: got %q, want %q", got["title"], title)
	}
}

// TestLargeMsgTruncated exercises EC1: a 70 KiB msg is capped at 60 KiB
// with an inline truncation marker.
func TestLargeMsgTruncated(t *testing.T) {
	big := strings.Repeat("x", 70_000)
	var buf bytes.Buffer
	logger := NewLogger(Options{Service: "api", Env: "prod", Version: "v0", Output: &buf})
	logger.Info(big)

	got := decodeOne(t, buf.Bytes())
	gotMsg, ok := got["msg"].(string)
	if !ok {
		t.Fatalf("msg is not a string: %T %v", got["msg"], got["msg"])
	}
	if len(gotMsg) > maxMsgBytes {
		t.Errorf("truncated msg length = %d, want <= %d", len(gotMsg), maxMsgBytes)
	}
	if !strings.HasSuffix(gotMsg, truncationSuffix) {
		t.Errorf("truncation marker missing: ...%q", gotMsg[len(gotMsg)-50:])
	}
}

// TestFFmpegStderrWrap exercises EC2: stderr lines are emitted as
// event=ffmpeg_stderr records with the original line preserved.
func TestFFmpegStderrWrap(t *testing.T) {
	var buf bytes.Buffer
	saved := Default
	Default = NewLogger(Options{Service: "pipeline", Env: "prod", Version: "v0", Output: &buf, Level: slog.LevelDebug})
	t.Cleanup(func() {
		Default = saved
		levelVar.Set(slog.LevelInfo)
	})

	stderr := strings.NewReader(strings.Join([]string{
		"[info] frame=  100 fps=25",
		"[warning] missing PTS, setting to 0",
		"[error] could not open input",
	}, "\n"))
	WrapFFmpegStderr(context.Background(), stderr)

	lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte{'\n'})
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), buf.String())
	}
	for _, raw := range lines {
		var rec map[string]any
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatalf("not valid JSON: %s err=%v", raw, err)
		}
		if rec["event"] != "ffmpeg_stderr" {
			t.Errorf("event field = %v, want ffmpeg_stderr (line=%s)", rec["event"], raw)
		}
		if _, ok := rec["line"].(string); !ok {
			t.Errorf("missing or wrong-typed line field in %s", raw)
		}
	}
	// The error line should map to level=error.
	var errRec map[string]any
	_ = json.Unmarshal(lines[2], &errRec)
	if errRec["level"] != "error" {
		t.Errorf("error line level = %v, want error", errRec["level"])
	}
}

// decodeOne parses the first JSON object in the provided buffer.
func decodeOne(t *testing.T, b []byte) map[string]any {
	t.Helper()
	idx := bytes.IndexByte(b, '\n')
	if idx < 0 {
		idx = len(b)
	}
	var got map[string]any
	if err := json.Unmarshal(b[:idx], &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", b[:idx], err)
	}
	return got
}
