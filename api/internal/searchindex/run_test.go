package searchindex

// #1483: the api:search_index loop returns at once on a SQLite-backed API: there is no BM25
// index to build, and a round would ask to_regprocedure, which SQLite lacks, every hour.

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRunReturnsOnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		Run(context.Background(), db, slog.New(slog.NewTextHandler(&out, nil)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop runs on SQLite")
	}
	if strings.Contains(out.String(), "search_index_failed") || !strings.Contains(out.String(), "search_index_skipped") {
		t.Fatalf("log: %s", out.String())
	}
}
