package updates

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 1, nil }

func TestNoteStoreLogsAFailedWrite(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	noteStore(logger, "SetRunDetail", errors.New("connection reset"), "run_id", "r1")
	out := buf.String()
	for _, want := range []string{"level=WARN", "event=update_store_failed", "op=SetRunDetail", "run_id=r1",
		`err="connection reset"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestNoteStoreIgnoresNoFailure(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	noteStore(logger, "a", nil)
	noteStore(logger, "b", context.Canceled)
	noteStore(logger, "c", fmt.Errorf("read: %w", sql.ErrNoRows))
	storeExec(logger, "drain")(fakeResult{}, nil)
	if buf.Len() != 0 {
		t.Fatalf("logged without a failure: %q", buf.String())
	}
	storeExec(logger, "undrain")(nil, errors.New("deadlock"))
	if !strings.Contains(buf.String(), "op=undrain") {
		t.Fatalf("a failed undrain is not logged: %q", buf.String())
	}
}
