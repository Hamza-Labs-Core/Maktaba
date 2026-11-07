//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The worker role run for real, in its own process: `serve --role worker`
// (config load, Validate, migrations, runWorker) against a scratch
// database. Every unit test drove the worker's jobs directly, so none saw
// that the role itself exited 1 at start (two listeners on its control
// port, from c15649f76 until this test's PR) and no job had ever run.

// childRoleEnv names the role the re-run test binary starts; childAddrEnv
// its control port.
const (
	childRoleEnv = "MAKTABA_TEST_CHILD_ROLE"
	childAddrEnv = "MAKTABA_TEST_CHILD_ADDR"
)

// TestRoleChildProcess is not a test on its own: the parent re-runs the
// test binary with only this test selected and childRoleEnv set, and this
// becomes the role's process. It returns when the role does (SIGTERM).
func TestRoleChildProcess(t *testing.T) {
	role := os.Getenv(childRoleEnv)
	if role == "" {
		t.Skip("only runs as the child of TestWorkerRoleStaysUp")
	}
	if a := os.Getenv(childAddrEnv); a != "" {
		workerControlAddr = a
	}
	runServe([]string{"--role", role})
}

func TestWorkerRoleStaysUp(t *testing.T) {
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// The worker migrates its own database at start, as on a node.
	scratch := scratchDB(ctx, t, url)
	var name string
	if err := scratch.DB.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name

	addr := freeLoopbackAddr(t)
	out := &syncBuffer{}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRoleChildProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), childRoleEnv+"=worker", childAddrEnv+"="+addr, "MAKTABA_CLOUD_DB_URL="+u.String())
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			<-exited
		}
	})

	// It answers on its control port...
	body := waitHealthy(t, "http://"+addr+"/healthz", exited, out)
	if !strings.Contains(body, `"role":"worker"`) {
		t.Fatalf("healthz body %q, want the worker's", body)
	}
	// ...and is still up a while later: the old bug failed the second listen
	// right after the first came up.
	select {
	case err := <-exited:
		t.Fatalf("the worker exited (%v) after answering; its output:\n%s", err, out.String())
	case <-time.After(3 * time.Second):
	}
	if _, err := httpGet("http://" + addr + "/healthz"); err != nil {
		t.Fatalf("healthz after 3 s: %v; output:\n%s", err, out.String())
	}

	// SIGTERM stops it cleanly, inside the shutdown grace.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the worker exited %v on SIGTERM, want 0; output:\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the worker did not stop within 30 s of SIGTERM; output:\n%s", out.String())
	}
}

// freeLoopbackAddr is a loopback address nothing listens on (released
// before the child takes it).
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// waitHealthy polls url until it answers 200 and returns the body. The
// child exiting first fails the test with its output.
func waitHealthy(t *testing.T, url string, exited <-chan error, out *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			t.Fatalf("the worker exited (%v) before answering; its output:\n%s", err, out.String())
		default:
		}
		if body, err := httpGet(url); err == nil {
			return body
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the worker never answered %s; output:\n%s", url, out.String())
	return ""
}

func httpGet(url string) (string, error) {
	c := http.Client{Timeout: 2 * time.Second}
	res, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", errors.New(res.Status)
	}
	return string(b), nil
}
