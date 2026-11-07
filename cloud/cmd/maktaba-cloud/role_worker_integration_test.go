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
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The roles run for real, each in its own process: `serve --role <role>`
// (config load, Validate, migrations, the role) against a scratch
// database. Every unit test drove the roles' jobs directly, so none saw
// that the worker exited 1 at start (two listeners on its control port,
// from c15649f76 until #882) or that Validate refused the dns role, and
// neither role had ever run.

// The child's role, control port and config file.
const (
	childRoleEnv   = "MAKTABA_TEST_CHILD_ROLE"
	childAddrEnv   = "MAKTABA_TEST_CHILD_ADDR"
	childConfigEnv = "MAKTABA_TEST_CHILD_CONFIG"
)

// TestRoleChildProcess is not a test on its own: startRole re-runs the
// test binary with only this test selected and childRoleEnv set, and this
// becomes the role's process. It returns when the role does (SIGTERM).
func TestRoleChildProcess(t *testing.T) {
	role := os.Getenv(childRoleEnv)
	if role == "" {
		t.Skip("only runs as the child of startRole")
	}
	if a := os.Getenv(childAddrEnv); a != "" {
		workerControlAddr, dnsControlAddr = a, a
	}
	args := []string{"--role", role}
	if c := os.Getenv(childConfigEnv); c != "" {
		args = append(args, "--config", c)
	}
	runServe(args)
}

func TestWorkerRoleStaysUp(t *testing.T) {
	p := startRole(t, "worker", "")
	if !strings.Contains(p.health, `"role":"worker"`) {
		t.Fatalf("healthz body %q, want the worker's", p.health)
	}
	p.staysUpThenStops(t)
}

// roleProc is a role's process started by startRole.
type roleProc struct {
	cmd    *exec.Cmd
	addr   string // its control port
	health string // its first /healthz body
	exited chan error
	out    *syncBuffer
}

// startRole runs `serve --role <role>` in a child process, with config
// (TOML, may be empty) and a scratch database the role migrates itself,
// as on a node, and returns once its control port answers. The child
// exiting first fails the test with its output.
func startRole(t *testing.T, role, config string) *roleProc {
	t.Helper()
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
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

	p := &roleProc{addr: freeLoopbackAddr(t), exited: make(chan error, 1), out: &syncBuffer{}}
	env := append(os.Environ(), childRoleEnv+"="+role, childAddrEnv+"="+p.addr, "MAKTABA_CLOUD_DB_URL="+u.String())
	if config != "" {
		path := filepath.Join(t.TempDir(), "cloud.toml")
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		env = append(env, childConfigEnv+"="+path)
	}
	p.cmd = exec.Command(os.Args[0], "-test.run=^TestRoleChildProcess$", "-test.count=1")
	p.cmd.Env = env
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.exited <- p.cmd.Wait() }()
	t.Cleanup(func() {
		if p.cmd.ProcessState == nil {
			_ = p.cmd.Process.Kill()
			<-p.exited
		}
	})

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-p.exited:
			t.Fatalf("the %s role exited (%v) before answering; its output:\n%s", role, err, p.out.String())
		default:
		}
		if body, err := httpGet("http://" + p.addr + "/healthz"); err == nil {
			p.health = body
			return p
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the %s role never answered on %s; output:\n%s", role, p.addr, p.out.String())
	return nil
}

// staysUpThenStops checks the role is still up and answering 3 s after
// its first answer (the worker's bug failed its second listen right after
// the first came up), then that SIGTERM stops it with exit 0.
func (p *roleProc) staysUpThenStops(t *testing.T) {
	t.Helper()
	select {
	case err := <-p.exited:
		t.Fatalf("the role exited (%v) after answering; its output:\n%s", err, p.out.String())
	case <-time.After(3 * time.Second):
	}
	if _, err := httpGet("http://" + p.addr + "/healthz"); err != nil {
		t.Fatalf("healthz after 3 s: %v; output:\n%s", err, p.out.String())
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-p.exited:
		if err != nil {
			t.Fatalf("the role exited %v on SIGTERM, want 0; output:\n%s", err, p.out.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the role did not stop within 30 s of SIGTERM; output:\n%s", p.out.String())
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
