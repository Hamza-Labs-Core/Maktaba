package dbsafe

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain runs this test binary as a fake PostgreSQL program when asked (DBSAFE_FAKE).
func TestMain(m *testing.M) {
	if os.Getenv("DBSAFE_FAKE") == "1" {
		fakeProgram(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

// fakeProgram is pg_restore (it writes a script for ever) or psql (it reads 4 KB of it,
// says an SQL error and exits 3, as ON_ERROR_STOP does).
func fakeProgram(args []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "--dbname=") { // psql: the connection first (Target.command)
			r := bufio.NewReader(os.Stdin)
			_, _ = io.CopyN(io.Discard, r, 4096)
			_, _ = os.Stderr.WriteString("psql:<stdin>:12: ERROR:  relation \"videos\" already exists\n")
			os.Exit(3)
		}
	}
	line := []byte("INSERT INTO videos VALUES (1, 'x');\n")
	for {
		if _, err := os.Stdout.Write(line); err != nil {
			os.Exit(1)
		}
	}
}

// A psql that stops at an SQL error stops the restore at once (pg_restore, writing for
// ever, is stopped too), and its own words are the error: no hang until serve's end.
func TestRestoreAtomicPsqlStopsFirst(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBSAFE_FAKE", "1")
	tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	began := time.Now()
	err = tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "psql stopped") {
		t.Fatalf("the error: %v", err)
	}
	if took := time.Since(began); took > 10*time.Second || ctx.Err() != nil {
		t.Fatalf("it waited %s (until the context's end?)", took)
	}
}
