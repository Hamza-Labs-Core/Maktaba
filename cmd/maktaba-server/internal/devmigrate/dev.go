// Package devmigrate moves a Mac that runs the developer stack (tools/dev/stack.sh
// with the compose Postgres in Docker) into Maktaba.app (Epic 36 story 36.9):
// `maktaba-server migrate-from-dev`. It finds the stack, stops it, dumps the
// Docker database into the bundled Postgres, copies the state the app keeps in
// its own folder (keys, the Chroma index, the models), carries the stack's
// settings into server.toml and checks the result. Nothing of the stack is
// deleted: the Docker volume is kept until `--finalize`, the files are copies.
package devmigrate

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sys is the outside world (tests replace it).
type Sys struct {
	// Command makes a command to run (exec.CommandContext).
	Command func(ctx context.Context, name string, args ...string) *exec.Cmd
	Getenv  func(string) string
	Home    string
}

// RealSys is this machine.
func RealSys() Sys {
	home, _ := os.UserHomeDir()
	return Sys{Command: exec.CommandContext, Getenv: os.Getenv, Home: home}
}

// output runs a command and returns its stdout (nil on any failure: the
// probes treat a missing tool like a missing process).
func (s Sys) output(ctx context.Context, name string, args ...string) []byte {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := s.Command(ctx, name, args...).Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	return out
}

// probeTimeout bounds each probe (a hung network mount can stall a tool).
const probeTimeout = 20 * time.Second

// Proc is one running process of the stack.
type Proc struct {
	PID  int
	Kind string // api, streaming, worker, stt (a transcription worker)
	Args string
	Env  map[string]string
	Cwd  string // its working directory (PWD: stack.sh starts each one in its folder)
}

// arg is the value of a --flag in the command line ("" when absent).
func (p Proc) arg(flag string) string {
	f := strings.Fields(p.Args)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == flag {
			return f[i+1]
		}
	}
	return ""
}

// Stack is what runs the developer stack on this Mac.
type Stack struct {
	Procs []Proc
	// App are processes on the stack's ports that Maktaba.app's supervisor runs.
	App []Proc
	// Repo is the checkout whose tools/dev/stack.sh runs the stack ("" unknown).
	Repo string
}

// Script is the checkout's stack.sh.
func (s Stack) Script() string {
	if s.Repo == "" {
		return ""
	}
	return filepath.Join(s.Repo, "tools", "dev", "stack.sh")
}

// Workers counts the transcription workers (by worker id: uv and its python
// both carry the command line).
func (s Stack) Workers() int {
	ids := map[string]bool{}
	for _, p := range s.Procs {
		if p.Kind == "stt" {
			ids[p.arg("--worker-id")] = true
		}
	}
	return len(ids)
}

// Summary names what runs, e.g. "api, streaming, main worker, 2 transcription workers".
func (s Stack) Summary() string {
	var api, streaming, worker bool
	for _, p := range s.Procs {
		switch p.Kind {
		case "api":
			api = true
		case "streaming":
			streaming = true
		case "worker":
			worker = true
		}
	}
	var parts []string
	if api {
		parts = append(parts, "api")
	}
	if streaming {
		parts = append(parts, "streaming")
	}
	if worker {
		parts = append(parts, "main worker")
	}
	if n := s.Workers(); n > 0 {
		parts = append(parts, plural(n, "transcription worker"))
	}
	if len(parts) == 0 {
		return "nothing (the stack is stopped)"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

// StackPorts are the stack's public ports (api, streaming): nothing may listen
// on them once it stopped (Maktaba.app's server takes the same ones).
var StackPorts = []string{"8080", "8081"}

// FindStack finds the running stack by command line. repo, when set, is the checkout to use.
func FindStack(ctx context.Context, sys Sys, repo string) Stack {
	seen := map[int]bool{}
	var st Stack
	add := func(kind string, pids []int) {
		for _, pid := range pids {
			if seen[pid] {
				continue
			}
			seen[pid] = true
			p := sys.proc(ctx, pid)
			p.Kind = kind
			if kind == "worker" && strings.Contains(" "+p.Args+" ", " --stages transcribe ") {
				p.Kind = "stt"
			}
			if p.Env["MAKTABA_SUPERVISED"] == "1" {
				st.App = append(st.App, p)
				continue
			}
			st.Procs = append(st.Procs, p)
		}
	}
	// by command line: `go run . serve` in api/ and streaming/ runs <build cache>/api
	// and .../streaming (lsof and netstat can hang or come back empty)
	add("api", pids(sys.output(ctx, "pgrep", "-f", "[/]api serve")))
	add("streaming", pids(sys.output(ctx, "pgrep", "-f", "[/]streaming serve")))
	add("worker", pids(sys.output(ctx, "pgrep", "-if", "python -m maktaba_pipeline run")))

	st.Repo = repo
	if st.Repo == "" {
		for _, p := range st.Procs {
			if p.Cwd == "" {
				continue
			}
			r := filepath.Dir(p.Cwd) // <repo>/api, <repo>/streaming, <repo>/pipeline
			if _, err := os.Stat(filepath.Join(r, "tools", "dev", "stack.sh")); err == nil {
				st.Repo = r
				break
			}
		}
	}
	return st
}

// PortsInUse are the ports something listens on (on loopback).
func PortsInUse(ports []string) []string {
	var busy []string
	for _, p := range ports {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+p, 500*time.Millisecond); err == nil {
			_ = c.Close()
			busy = append(busy, p)
		}
	}
	return busy
}

func pids(out []byte) []int {
	var ps []int
	for _, f := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(f); err == nil {
			ps = append(ps, n)
		}
	}
	sort.Ints(ps)
	return ps
}

var envKey = regexp.MustCompile(`(?:^| )([A-Za-z_][A-Za-z0-9_]*)=`)

// proc reads a process's command line and environment (ps -E: a user's own
// processes).
func (s Sys) proc(ctx context.Context, pid int) Proc {
	id := strconv.Itoa(pid)
	p := Proc{PID: pid, Env: map[string]string{}}
	p.Args = strings.TrimSpace(string(s.output(ctx, "ps", "-ww", "-o", "command=", "-p", id)))
	withEnv := strings.TrimSpace(string(s.output(ctx, "ps", "-wwE", "-o", "command=", "-p", id)))
	if env, ok := strings.CutPrefix(withEnv, p.Args); ok {
		p.Env = parseEnv(env)
	}
	p.Cwd = p.Env["PWD"]
	return p
}

// parseEnv splits ps -E's "A=1 B=two words C=3" (a value runs to the next NAME=).
func parseEnv(s string) map[string]string {
	env := map[string]string{}
	m := envKey.FindAllStringSubmatchIndex(s, -1)
	for i, loc := range m {
		end := len(s)
		if i+1 < len(m) {
			end = m[i+1][0]
		}
		env[s[loc[2]:loc[3]]] = strings.TrimSpace(s[loc[1]:end])
	}
	return env
}
