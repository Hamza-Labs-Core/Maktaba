package dbsafe

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// TestMain runs this test binary as a fake PostgreSQL program when asked (DBSAFE_FAKE).
func TestMain(m *testing.M) {
	if os.Getenv("DBSAFE_FAKE") == "1" {
		fakeProgram(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

// fakeProgram is pg_restore (it writes a script for ever, or ends well with a child holding
// its stdout: DBSAFE_FAKE_RESTORE=hold-stdout) or psql (it reads 4 KB of it, says an SQL
// error and exits 3, as ON_ERROR_STOP does; DBSAFE_FAKE_PSQL=read-all reads everything, and
// =commit-exit reads to the COMMIT and dies after it).
func fakeProgram(args []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "--dbname=") { // psql: the connection first (Target.command)
			switch os.Getenv("DBSAFE_FAKE_PSQL") {
			case "read-all":
				_, _ = io.Copy(io.Discard, os.Stdin)
			case "commit-exit":
				sc := bufio.NewScanner(os.Stdin)
				sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
				for sc.Scan() {
					l := strings.TrimSpace(sc.Text())
					if l == "COMMIT;" {
						_, _ = os.Stderr.WriteString("psql: killed at the commit\n")
						os.Exit(9)
					}
					if w, ok := strings.CutPrefix(l, `\warn `); ok && strings.HasSuffix(w, "-ran") {
						_, _ = os.Stdout.WriteString(w + "\n") // what it was told to say
					}
				}
			default:
				r := bufio.NewReader(os.Stdin)
				_, _ = io.CopyN(io.Discard, r, 4096)
				_, _ = os.Stderr.WriteString("psql:<stdin>:12: ERROR:  relation \"videos\" already exists\n")
				os.Exit(3)
			}
			os.Exit(0)
		}
	}
	for _, a := range args {
		if a == "-l" {
			return // a dump with nothing in it
		}
	}
	if os.Getenv("DBSAFE_FAKE_RESTORE") == "script-once" {
		// a script that ends well: one line, then the process is gone
		if _, err := os.Stdout.Write([]byte("INSERT INTO videos VALUES (1, 'x');\n")); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("DBSAFE_FAKE_RESTORE") == "hold-stdout" {
		// a child that holds this process's stdout open after it ends
		hold := exec.Command(os.Args[0], "-test.run=^TestHoldStdoutHelper$")
		hold.Env = append(os.Environ(), "DBSAFE_FAKE_HOLD=1")
		hold.Stdout, hold.Stderr = os.Stdout, os.Stderr
		if err := hold.Start(); err != nil {
			os.Exit(1)
		}
		line := []byte("INSERT INTO videos VALUES (1, 'x');\n")
		if _, err := os.Stdout.Write(line); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
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
	err = tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, "", func(step string) error {
		t.Errorf("told %q of a script that never ended", step)
		return nil
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "psql stopped") {
		t.Fatalf("the error: %v", err)
	}
	if took := time.Since(began); took > 10*time.Second || ctx.Err() != nil {
		t.Fatalf("it waited %s (until the context's end?)", took)
	}
}

// TestHoldStdoutHelper is the child that holds a fake pg_restore's stdout open (run only as
// a child).
func TestHoldStdoutHelper(t *testing.T) {
	if os.Getenv("DBSAFE_FAKE_HOLD") != "1" {
		t.Skip("a helper process")
	}
	time.Sleep(time.Minute)
}

// A psql that dies after COMMIT was written to it is not said to have changed nothing: the
// commit may have run, and the answer says so ("not known to be committed", as #1670 does).
func TestRestoreAtomicAKilledPsqlAfterCommitIsNotKnownCommitted(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBSAFE_FAKE", "1")
	t.Setenv("DBSAFE_FAKE_PSQL", "commit-exit")
	t.Setenv("DBSAFE_FAKE_RESTORE", "script-once")
	tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	told := make(chan string, 4)
	err = tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, "", func(step string) error {
		told <- step
		return nil
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not known to be committed") {
		t.Fatalf("the error: %v", err)
	}
	close(told)
	for step := range told {
		if step != pgbundle.StepReplaced {
			t.Errorf("told %q, want only %q (the commit was sent)", step, pgbundle.StepReplaced)
		}
	}
}

// A pg_restore that ends well but leaves a child holding its stdout does not hang the
// restore: the wait closes the pipe at its delay, the copy ends, and the restore fails with
// nothing changed (no real pg_restore does it; the bound is what matters).
func TestRestoreAtomicDoesNotWaitForAPgRestoreChildHoldingStdout(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBSAFE_FAKE", "1")
	t.Setenv("DBSAFE_FAKE_PSQL", "read-all")
	t.Setenv("DBSAFE_FAKE_RESTORE", "hold-stdout")
	old := childWaitDelay
	childWaitDelay = 150 * time.Millisecond
	t.Cleanup(func() { childWaitDelay = old })
	tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	began := time.Now()
	err = tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, "", func(step string) error {
		t.Errorf("told %q of a script that never ended", step)
		return nil
	}, io.Discard)
	if err == nil {
		t.Fatal("the restore of a dump whose script never ended says it worked")
	}
	if took := time.Since(began); took > 10*time.Second || ctx.Err() != nil {
		t.Fatalf("it waited %s (until the context's end?), not the child's delay", took)
	}
}
