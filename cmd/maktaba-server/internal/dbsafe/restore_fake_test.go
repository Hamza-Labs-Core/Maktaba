package dbsafe

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/klauspost/compress/zstd"
)

// fastRaceSubprocess disables TSan's default 1-second atexit sleep on exit code 0 in
// fake child test binaries so unit tests under `go test -race` finish in milliseconds.
func fastRaceSubprocess(t *testing.T) {
	t.Helper()
	val := "atexit_sleep_ms=0"
	if cur := os.Getenv("GORACE"); cur != "" {
		val = cur + " " + val
	}
	t.Setenv("GORACE", val)
}

// TestMain runs this test binary as a fake PostgreSQL program when asked (DBSAFE_FAKE).
func TestMain(m *testing.M) {
	if os.Getenv("DBSAFE_FAKE") == "1" {
		fakeProgram(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

// fakeProgram is pg_restore (writes a script forever, writes a large payload and exits 0,
// or ends well with a grandchild holding stdout ONLY) or psql (stops at SQL error, drains
// slowly with backpressure, exits before COMMIT, or dies after COMMIT).
func fakeProgram(args []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "--dbname=") { // psql: the connection first (Target.command)
			switch os.Getenv("DBSAFE_FAKE_PSQL") {
			case "read-all":
				_, _ = io.Copy(io.Discard, os.Stdin)
			case "slow-backpressure":
				// Drain stdin slowly (longer than childWaitDelay) and record every byte
				// received so the test verifies zero truncation under backpressure.
				outFile := os.Getenv("DBSAFE_FAKE_PSQL_OUT")
				delay, _ := time.ParseDuration(os.Getenv("DBSAFE_FAKE_PSQL_DELAY"))
				var got bytes.Buffer
				buf := make([]byte, 32<<10)
				for {
					n, err := os.Stdin.Read(buf)
					if n > 0 {
						got.Write(buf[:n])
						if i := bytes.Index(got.Bytes(), []byte(`\warn `)); i >= 0 {
							rest := got.Bytes()[i:]
							for {
								j := bytes.Index(rest, []byte(`\warn `))
								if j < 0 {
									break
								}
								rest = rest[j:]
								nl := bytes.IndexByte(rest, '\n')
								if nl < 0 {
									break
								}
								line := strings.TrimSpace(string(rest[:nl]))
								if w, ok := strings.CutPrefix(line, `\warn `); ok && strings.HasSuffix(w, "-ran") {
									_, _ = os.Stdout.WriteString(w + "\n")
								}
								rest = rest[nl+1:]
							}
						}
						if delay > 0 {
							time.Sleep(delay)
						}
					}
					if err != nil {
						break
					}
				}
				if outFile != "" {
					_ = os.WriteFile(outFile, got.Bytes(), 0o600)
				}
			case "pre-commit-exit":
				sc := bufio.NewScanner(os.Stdin)
				sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
				for sc.Scan() {
					l := strings.TrimSpace(sc.Text())
					if w, ok := strings.CutPrefix(l, `\warn `); ok && strings.HasSuffix(w, "-ran") {
						_, _ = os.Stdout.WriteString(w + "\n")
						// Die immediately after signaling ran so COMMIT write fails with EPIPE.
						os.Exit(7)
					}
				}
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
		if a == "-l" || a == "--list" {
			if os.Getenv("DBSAFE_FAKE_VERIFY_DIR") == "1" {
				// Return a minimal TOC listing one table so VerifyDump/VerifyAside proceed to --file=-.
				_, _ = os.Stdout.WriteString("1; 1259 100 TABLE public videos maktaba\n2; 0 100 TABLE DATA public videos maktaba\n")
				os.Exit(0)
			}
			return // a dump with nothing in it
		}
	}
	if os.Getenv("DBSAFE_FAKE_VERIFY_DIR") == "1" {
		// Simulate pg_restore --file=- verifying every .dat.gz table-data member in a directory dump.
		dumpPath := args[len(args)-1]
		raw, err := os.ReadFile(filepath.Join(dumpPath, "100.dat.gz"))
		if err != nil || string(raw) != "VALID-GZIP-MEMBER" {
			_, _ = os.Stderr.WriteString("pg_restore: error: could not open input file 100.dat.gz: corrupt or missing\n")
			os.Exit(1)
		}
		_, _ = os.Stdout.WriteString("COPY public.videos FROM stdin;\n\\.\n")
		os.Exit(0)
	}
	switch os.Getenv("DBSAFE_FAKE_RESTORE") {
	case "script-once":
		// a script that ends well: one line, then the process is gone
		if _, err := os.Stdout.Write([]byte("INSERT INTO videos VALUES (1, 'x');\n")); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "revoke-public":
		// Emit a REVOKE ALL ON SCHEMA public FROM PUBLIC in post-data so tests verify
		// how.after re-grants USAGE ON SCHEMA public TO PUBLIC afterwards.
		isPost := true
		for _, a := range args {
			if a == "--section=data" {
				isPost = false
			}
		}
		if isPost {
			_, _ = os.Stdout.WriteString("REVOKE ALL ON SCHEMA public FROM PUBLIC;\n")
		} else {
			_, _ = os.Stdout.WriteString("INSERT INTO videos VALUES (1, 'x');\n")
		}
		os.Exit(0)
	case "slow-then-exit":
		// Block longer than childWaitDelay BEFORE emitting final bytes and exiting 0,
		// so pr.Read starts pre-exit and returns its final bytes after gen.Wait() flips doneAt.
		if d, err := time.ParseDuration(os.Getenv("DBSAFE_FAKE_RESTORE_DELAY")); err == nil && d > 0 {
			time.Sleep(d)
		}
		_, _ = os.Stdout.WriteString("INSERT INTO videos VALUES (1, 'x');\n")
		os.Exit(0)
	case "bulk":
		// Write a large deterministic payload (larger than kernel pipe buffer) and exit 0.
		nLines, _ := strconv.Atoi(os.Getenv("DBSAFE_FAKE_RESTORE_LINES"))
		if nLines <= 0 {
			nLines = 4096
		}
		part := "post"
		for _, a := range args {
			if a == "--section=data" {
				part = "data"
			}
		}
		bw := bufio.NewWriterSize(os.Stdout, 64<<10)
		for i := range nLines {
			_, _ = bw.WriteString("-- " + part + "-line-" + strconv.Itoa(i) + " " + strings.Repeat("x", 96) + "\n")
		}
		_, _ = bw.WriteString("-- " + part + "-end-marker\n")
		if err := bw.Flush(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "hold-stdout":
		// A grandchild that inherits stdout ONLY (not stderr!) and holds it open after
		// pg_restore itself exits 0.
		hold := exec.Command(os.Args[0], "-test.run=^TestHoldStdoutHelper$")
		hold.Env = append(os.Environ(), "DBSAFE_FAKE_HOLD=1")
		hold.Stdout = os.Stdout
		hold.Stderr = nil
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
	fastRaceSubprocess(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBSAFE_FAKE", "1")
	tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	began := time.Now()
	err = tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, "", "", nil, func(step string) error {
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
	fastRaceSubprocess(t)
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
	err = tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, "", "", nil, func(step string) error {
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

// When psql dies before COMMIT is written (so the COMMIT write itself fails with 0 bytes
// written), the error distinguishes that nothing changed rather than claiming an uncertain
// commit.
func TestRestoreAtomicCommitWriteFailureSaysNothingChanged(t *testing.T) {
	fastRaceSubprocess(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBSAFE_FAKE", "1")
	t.Setenv("DBSAFE_FAKE_PSQL", "pre-commit-exit")
	t.Setenv("DBSAFE_FAKE_RESTORE", "script-once")
	tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, "", "", nil, func(string) error {
		// Give pre-commit-exit a moment to exit before COMMIT is written.
		time.Sleep(20 * time.Millisecond)
		return nil
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "nothing changed: one transaction") {
		t.Fatalf("the error: %v", err)
	}
}

// Full successful pg_restore output arrives completely without truncation even under heavy
// psql backpressure that drains slower than childWaitDelay, on both deadline-capable pipes
// (Unix) and non-deadline pipes (Windows watchdog fallback).
func TestRestoreAtomicFullOutputUnderHeavyPsqlBackpressure(t *testing.T) {
	fastRaceSubprocess(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, noDeadline := range []bool{false, true} {
		name := "deadline-pipe"
		if noDeadline {
			name = "no-deadline-watchdog"
		}
		t.Run(name, func(t *testing.T) {
			if noDeadline {
				prev := pipeSetReadDeadline
				pipeSetReadDeadline = func(*os.File, time.Time) error { return os.ErrNoDeadline }
				t.Cleanup(func() { pipeSetReadDeadline = prev })
			}
			outFile := filepath.Join(t.TempDir(), "psql-received.sql")
			t.Setenv("DBSAFE_FAKE", "1")
			t.Setenv("DBSAFE_FAKE_PSQL", "slow-backpressure")
			t.Setenv("DBSAFE_FAKE_PSQL_OUT", outFile)
			t.Setenv("DBSAFE_FAKE_PSQL_DELAY", "3ms")
			t.Setenv("DBSAFE_FAKE_RESTORE", "bulk")
			t.Setenv("DBSAFE_FAKE_RESTORE_LINES", "3000") // ~340 KiB per part > OS pipe buffer
			old := childWaitDelay
			childWaitDelay = 20 * time.Millisecond // shorter than total slow drain time
			t.Cleanup(func() { childWaitDelay = old })
			tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, "", "", nil, func(string) error { return nil }, io.Discard); err != nil {
				t.Fatalf("restoreAtomic failed under backpressure: %v", err)
			}
			got, err := os.ReadFile(outFile)
			if err != nil {
				t.Fatal(err)
			}
			s := string(got)
			for _, want := range []string{"-- data-line-0 ", "-- data-line-2999 ", "-- data-end-marker\n", "-- post-line-0 ", "-- post-line-2999 ", "-- post-end-marker\n", "COMMIT;\n"} {
				if !strings.Contains(s, want) {
					t.Errorf("missing %q in %d bytes received by psql", want, len(s))
				}
			}
		})
	}
}

// A pr.Read that started before pg_restore exited (blocking > childWaitDelay while pg_restore
// works) and returns its final bytes after pg_restore exits must NOT charge pre-exit wait
// against the post-exit budget.
func TestRestoreAtomicReadStartedBeforeExitAndReturningAfterExitSucceeds(t *testing.T) {
	fastRaceSubprocess(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(t.TempDir(), "psql-received.sql")
	t.Setenv("DBSAFE_FAKE", "1")
	t.Setenv("DBSAFE_FAKE_PSQL", "slow-backpressure")
	t.Setenv("DBSAFE_FAKE_PSQL_OUT", outFile)
	t.Setenv("DBSAFE_FAKE_PSQL_DELAY", "0ms")
	t.Setenv("DBSAFE_FAKE_RESTORE", "slow-then-exit")
	t.Setenv("DBSAFE_FAKE_RESTORE_DELAY", "80ms") // > childWaitDelay (30ms)
	old := childWaitDelay
	childWaitDelay = 30 * time.Millisecond
	t.Cleanup(func() { childWaitDelay = old })
	tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, "", "", nil, func(string) error { return nil }, io.Discard); err != nil {
		t.Fatalf("restoreAtomic aborted on pre-exit read duration: %v", err)
	}
}

// publicAfresh's GRANT USAGE ON SCHEMA public TO PUBLIC (how.after) is emitted AFTER any
// archived REVOKE in post-data and BEFORE COMMIT so a dump's REVOKE cannot undo it.
func TestRestoreAtomicPublicAfreshGrantRunsAfterDumpRevoke(t *testing.T) {
	fastRaceSubprocess(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(t.TempDir(), "psql-received.sql")
	t.Setenv("DBSAFE_FAKE", "1")
	t.Setenv("DBSAFE_FAKE_PSQL", "slow-backpressure")
	t.Setenv("DBSAFE_FAKE_PSQL_OUT", outFile)
	t.Setenv("DBSAFE_FAKE_PSQL_DELAY", "0ms")
	t.Setenv("DBSAFE_FAKE_RESTORE", "revoke-public")
	tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, publicUsageGrant, "", nil, func(string) error { return nil }, io.Discard); err != nil {
		t.Fatalf("restoreAtomic failed: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	revIdx := strings.Index(s, "REVOKE ALL ON SCHEMA public FROM PUBLIC;")
	grantIdx := strings.LastIndex(s, publicUsageGrant)
	commitIdx := strings.LastIndex(s, "COMMIT;")
	if revIdx < 0 || grantIdx < revIdx || commitIdx < grantIdx {
		t.Fatalf("wrong order: revoke=%d grant=%d commit=%d\n%s", revIdx, grantIdx, commitIdx, s)
	}
}

// VerifyAside streams the full archive payload through pg_restore --file=- so a directory
// dump with intact toc.dat and counts.json but a missing or truncated table-data member is
// rejected instead of reused as the sole safety copy.
func TestVerifyAsideRejectsCorruptOrMissingTableDataMember(t *testing.T) {
	fastRaceSubprocess(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBSAFE_FAKE", "1")
	t.Setenv("DBSAFE_FAKE_VERIFY_DIR", "1")
	tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
	ctx := context.Background()

	makeDirDump := func(t *testing.T, dataContent *string) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "replaced-v0.4.0-20261126T000000.dump")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "toc.dat"), []byte("PGDMP\x01\x0e"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, pgbundle.CountsFile), []byte(`{"public.videos":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if dataContent != nil {
			if err := os.WriteFile(filepath.Join(dir, "100.dat.gz"), []byte(*dataContent), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	if err := tg.VerifyAside(ctx, makeDirDump(t, nil)); err == nil || !strings.Contains(err.Error(), "dump_corrupt") {
		t.Fatalf("missing table-data member accepted: %v", err)
	}
	trunc := "TRUNCATED"
	if err := tg.VerifyAside(ctx, makeDirDump(t, &trunc)); err == nil || !strings.Contains(err.Error(), "dump_corrupt") {
		t.Fatalf("truncated table-data member accepted: %v", err)
	}
	good := "VALID-GZIP-MEMBER"
	if err := tg.VerifyAside(ctx, makeDirDump(t, &good)); err != nil {
		t.Fatalf("valid directory dump rejected: %v", err)
	}

	// Sidecar binary vector chunk checks: missing final chunk (row total < counts.json)
	// and legacy chunked dump without counts.json must both be rejected by VerifyAside.
	const sig = "PGCOPY\n\xff\r\n\x00\x00\x00\x00\x00\x00\x00\x00\x00"
	const row = "\x00\x01\x00\x00\x00\x02hi"
	const end = "\xff\xff"
	makeChunked := func(t *testing.T, countsJSON *string, chunks map[string]string) string {
		t.Helper()
		dir := makeDirDump(t, &good)
		if countsJSON == nil {
			_ = os.Remove(filepath.Join(dir, pgbundle.CountsFile))
		} else {
			if err := os.WriteFile(filepath.Join(dir, pgbundle.CountsFile), []byte(*countsJSON), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for n, body := range chunks {
			if err := os.WriteFile(filepath.Join(dir, "chunk.public.segment_embeddings."+n+".bin"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	counts2 := `{"public.videos":1,"public.segment_embeddings":2}`
	missingFinal := makeChunked(t, &counts2, map[string]string{"000": sig + row + end})
	if err := tg.VerifyAside(ctx, missingFinal); err == nil || !strings.Contains(err.Error(), "1 of 2 rows") {
		t.Fatalf("chunked dump missing final sidecar chunk accepted: %v", err)
	}
	legacyNoCounts := makeChunked(t, nil, map[string]string{"000": sig + row + end})
	if err := tg.VerifyAside(ctx, legacyNoCounts); err == nil || !strings.Contains(err.Error(), "dump_incomplete") {
		t.Fatalf("legacy chunked dump without counts.json accepted: %v", err)
	}
	completeChunked := makeChunked(t, &counts2, map[string]string{"000": sig + row + end})
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := filepath.Join(completeChunked, "chunk.public.segment_embeddings.001.bin.zst")
	raw := sig + row + end
	encoded := encoder.EncodeAll([]byte(raw), nil)
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compressed, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	backups := t.TempDir()
	chunks, cleanup, err := prepareCopyChunks(ctx, completeChunked, backups)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	files := chunks["public.segment_embeddings"]
	if len(files) != 2 || filepath.Dir(filepath.Dir(files[1])) != backups {
		t.Fatalf("COPY files %v", files)
	}
	decoded, err := os.ReadFile(files[1])
	if err != nil || string(decoded) != raw || strings.Contains(pgbundle.CopyScript(chunks), ".bin.zst") {
		t.Fatalf("COPY file: %q, %v", decoded, err)
	}
	cleanup()
	if _, err := os.Stat(files[1]); !os.IsNotExist(err) {
		t.Fatalf("temporary COPY file: %v", err)
	}
	if err := tg.VerifyAside(ctx, completeChunked); err != nil {
		t.Fatalf("complete chunked dump rejected: %v", err)
	}
}

// A pg_restore that ends well but leaves a grandchild holding stdout ONLY (not stderr) does
// not hang the restore: the owned pipe's post-exit read deadline fires after childWaitDelay
// and the restore fails cleanly with nothing changed.
func TestRestoreAtomicDoesNotWaitForAPgRestoreChildHoldingStdout(t *testing.T) {
	fastRaceSubprocess(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBSAFE_FAKE", "1")
	t.Setenv("DBSAFE_FAKE_PSQL", "read-all")
	t.Setenv("DBSAFE_FAKE_RESTORE", "hold-stdout")
	old := childWaitDelay
	childWaitDelay = 80 * time.Millisecond
	t.Cleanup(func() { childWaitDelay = old })
	tg := Target{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", Bin: func(string) string { return self }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	began := time.Now()
	err = tg.restoreAtomic(ctx, "pre-0.3.1-to-0.3.2.dump", "", publicAfresh, "", "", nil, func(step string) error {
		t.Errorf("told %q of a script that never ended", step)
		return nil
	}, io.Discard)
	if err == nil {
		t.Fatal("the restore of a dump whose script never ended says it worked")
	}
	if took := time.Since(began); took > 5*time.Second || ctx.Err() != nil {
		t.Fatalf("it waited %s (until the context's end?), not the child's delay", took)
	}
}
