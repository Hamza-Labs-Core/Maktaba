package tvremote

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestCleanupFailedLogsTheStep(t *testing.T) {
	var buf bytes.Buffer
	c := &Controller{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	c.cleanupFailed("disable_input", nil, "input", "cf-1")
	c.cleanupFailed("disable_input", context.Canceled, "input", "cf-1")
	if buf.Len() != 0 {
		t.Fatalf("logged without a failure: %q", buf.String())
	}
	c.cleanupFailed("delete_input", errors.New("cloudflare: 500"), "input", "cf-1")
	out := buf.String()
	for _, want := range []string{"level=WARN", "event=tv_cf_cleanup_failed", "step=delete_input", "input=cf-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}
