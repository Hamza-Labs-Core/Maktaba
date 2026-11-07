// Package pgtest runs real Postgres servers on 127.0.0.1 for the supervisor's tests
// (nodecopy, switchover): a Postgres 16 install named by MAKTABA_TEST_PG_BIN, homes and
// copies prepared as pgbundle prepares them, migrated by maktaba-api built from the
// repository. Only tests use it.
package pgtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// Programs is the Postgres install the tests run (MAKTABA_TEST_PG_BIN); the test is
// skipped without one, or under -short.
func Programs(t *testing.T) string {
	t.Helper()
	home := os.Getenv("MAKTABA_TEST_PG_BIN")
	if home == "" {
		t.Skip("MAKTABA_TEST_PG_BIN unset (a Postgres 16 install)")
	}
	if testing.Short() {
		t.Skip("runs Postgres servers and builds maktaba-api")
	}
	return home
}

// FreePort is a free loopback port, held for this test (testport.Free).
func FreePort(t *testing.T) int {
	t.Helper()
	return testport.Free(t)
}

// WaitFor polls ok until it holds or d passes.
func WaitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// RepoRoot is the repository's root.
func RepoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "shared", "db", "migrations")); err == nil {
			return d
		}
	}
	t.Fatal("repository root not found")
	return ""
}

// Migrator returns what migrates a database with maktaba-api built from the repository.
func Migrator(t *testing.T) func(ctx context.Context, dsn string) error {
	t.Helper()
	root := RepoRoot(t)
	api := APIBinary(t)
	migrations := filepath.Join(root, "shared", "db", "migrations")
	return func(ctx context.Context, dsn string) error {
		cmd := exec.CommandContext(ctx, api, "migrate", "--dir", migrations, "--dsn", dsn, "up")
		cmd.Env = append(os.Environ(), "MAKTABA_NODE_MODE=")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	}
}

// Server is a postgres a test runs.
type Server struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
	log  bytes.Buffer
}

// Start runs exe args unless it runs already.
func (s *Server) Start(t *testing.T, exe string, args ...string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = &s.log, &s.log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", exe, err)
	}
	s.cmd, s.done = cmd, make(chan struct{})
	done := s.done
	go func() { _ = cmd.Wait(); close(done) }()
}

// Stop stops it (stop: the way to, nil: an interrupt).
func (s *Server) Stop(stop *exec.Cmd) {
	s.mu.Lock()
	cmd, done := s.cmd, s.done
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	if stop != nil {
		_ = stop.Run()
	} else {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	s.mu.Lock()
	s.cmd = nil
	s.mu.Unlock()
}

// Running reports whether it runs.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return false
	}
	select {
	case <-s.done:
		s.cmd = nil
		return false
	default:
		return true
	}
}

// Log is what it wrote.
func (s *Server) Log() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log.String()
}

// Bundle prepares a pgbundle cluster under dir on a free port (tls: its own CA's
// certificate) and starts it; it is stopped at the test's end.
func Bundle(ctx context.Context, t *testing.T, programs, dir string, tls bool) (pgbundle.Bundle, *Server) {
	t.Helper()
	b := pgbundle.Bundle{Home: programs, Data: filepath.Join(dir, "pg"), Keys: filepath.Join(dir, "keys"),
		Backups: filepath.Join(dir, "backups"), Port: FreePort(t), TLS: tls}
	if err := b.Prepare(ctx, &bytes.Buffer{}); err != nil {
		t.Fatalf("prepare %s: %v", dir, err)
	}
	s := &Server{}
	s.Start(t, b.Bin("postgres"), b.ServerArgs()...)
	t.Cleanup(func() { s.Stop(b.StopCommand()) })
	WaitUp(ctx, t, b)
	return b, s
}

// WaitUp waits for a bundle's server to take its app role.
func WaitUp(ctx context.Context, t *testing.T, b pgbundle.Bundle) {
	t.Helper()
	WaitFor(t, "Postgres at "+b.Addr(), 60*time.Second, func() bool {
		rctx, c := context.WithTimeout(ctx, 2*time.Second)
		defer c()
		return pgbundle.Ready(rctx, b.Addr(), pgbundle.AppRole, pgbundle.AppDatabase) == nil
	})
}

var (
	apiOnce sync.Once
	apiPath string
	apiErr  error
)

// APIBinary is maktaba-api built from the repository, once a test process, into one path per
// checkout under the temporary directory: go build into a target that is up to date
// does not link again (0.7 s, where a new path linked for 6.5 s and more on a busy
// runner, on every call: issue #855).
func APIBinary(t *testing.T) string {
	t.Helper()
	root := RepoRoot(t)
	apiOnce.Do(func() {
		sum := sha256.Sum256([]byte(root))
		dir := filepath.Join(os.TempDir(), "maktaba-pgtest-"+hex.EncodeToString(sum[:6]))
		if apiErr = os.MkdirAll(dir, 0o700); apiErr != nil {
			return
		}
		apiPath = filepath.Join(dir, "maktaba-api")
		if runtime.GOOS == "windows" {
			apiPath += ".exe"
		}
		build := exec.Command("go", "build", "-o", apiPath, ".")
		build.Dir = filepath.Join(root, "api")
		if out, err := build.CombinedOutput(); err != nil {
			apiErr = fmt.Errorf("%w\n%s", err, out)
		}
	})
	if apiErr != nil {
		t.Fatalf("build maktaba-api: %v", apiErr)
	}
	return apiPath
}
