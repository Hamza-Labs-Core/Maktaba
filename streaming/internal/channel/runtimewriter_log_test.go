package channel

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A failed channel_runtime write is logged once per run of failures (a database that
// is down fails every refresh of every channel), and its recovery once.
func TestRuntimeWriterLogsFailuresOncePerRun(t *testing.T) {
	var buf bytes.Buffer
	w := &runtimeWriter{log: slog.New(slog.NewTextHandler(&buf, nil))}
	op := rtOp{id: uuid.New()}
	down := errors.New("dial tcp: connection refused")
	w.report(op, nil)
	w.report(op, down)
	w.report(op, down)
	w.report(op, down)
	w.report(op, nil)
	w.report(op, nil)
	out := buf.String()
	if n := strings.Count(out, "event=channel_runtime_write_failed"); n != 1 {
		t.Errorf("%d failure lines, want 1:\n%s", n, out)
	}
	if n := strings.Count(out, "event=channel_runtime_write_recovered"); n != 1 {
		t.Errorf("%d recovery lines, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "channel_id="+op.id.String()) || !strings.Contains(out, "connection refused") {
		t.Errorf("the failure lacks its channel or error:\n%s", out)
	}
}
