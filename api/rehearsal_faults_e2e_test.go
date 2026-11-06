//go:build unix

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The fault rehearsal (Epic 53 story 53.07, docs/fault-rehearsal.md): a home of this
// commit, on the update rehearsal's kit and seed, has one part killed at a time; the web
// port must keep serving, /status.json must say what is down within its budget, the
// recovery console must open with the API or Postgres down, and the part must come back.
// Each scenario is a job of update-rehearsal.yml (F-numbered), so the tag guard, which
// requires the update rehearsal on the exact commit, requires these too.

// faultHome is a home of this commit, serving, on the small seed.
func (r *rehearsal) faultHome() *appMachine {
	r.t.Helper()
	home, _ := r.homeOnPrev(profile("small"))
	home.installByHand(r.kit.curSpec())
	home.waitServing(r.kit.cur, 10*time.Minute)
	return home
}

// status is the home's /status.json (nil when it does not answer).
func (m *appMachine) status() map[string]any {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(m.webURL() + "/status.json")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var doc map[string]any
	if json.NewDecoder(resp.Body).Decode(&doc) != nil {
		return nil
	}
	return doc
}

// group is a status group's state ("" when unknown).
func (m *appMachine) group(name string) string {
	doc := m.status()
	if doc == nil {
		return ""
	}
	c, _ := doc["components"].(map[string]any)
	s, _ := c[name].(string)
	return s
}

// shellServes checks the web port answers its page in time, whatever is down.
func (m *appMachine) shellServes(budget time.Duration) {
	t := m.r.t
	t.Helper()
	c := http.Client{Timeout: 5 * time.Second}
	start := time.Now()
	resp, err := c.Get(m.webURL() + "/")
	took := time.Since(start)
	if err != nil {
		t.Errorf("%s: the web shell does not answer: %v", m.name, err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("%s: the web shell answered %d", m.name, resp.StatusCode)
	}
	if took > budget {
		t.Errorf("%s: the web shell's first answer took %s (budget %s)", m.name, took.Round(time.Millisecond), budget)
	}
}

// pidOf is the pid of this machine's running process whose command has all of words (0:
// none).
func (m *appMachine) pidOf(words ...string) int {
	out, _ := exec.Command("pgrep", "-f", words[0]).Output()
	for _, f := range strings.Fields(string(out)) {
		pid, _ := strconv.Atoi(f)
		cmd := processCommand(pid)
		if !strings.Contains(cmd, m.dir) {
			continue
		}
		all := true
		for _, w := range words[1:] {
			all = all && strings.Contains(cmd, w)
		}
		if all {
			return pid
		}
	}
	return 0
}

// kill9 kills one of this machine's processes at once.
func (m *appMachine) kill9(what string, words ...string) int {
	t := m.r.t
	t.Helper()
	var pid int
	waitFor(t, what+" running", time.Minute, func() bool { pid = m.pidOf(words...); return pid != 0 })
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 %s (%d): %v", what, pid, err)
	}
	m.r.logf("FAULT: %s (pid %d) killed with -9", what, pid)
	return pid
}

// untilGroup waits for a status group to be one of states; how long it took.
func (m *appMachine) untilGroup(name string, budget time.Duration, states ...string) time.Duration {
	t := m.r.t
	t.Helper()
	start := time.Now()
	deadline := start.Add(budget + 30*time.Second)
	for time.Now().Before(deadline) {
		got := m.group(name)
		for _, s := range states {
			if got == s {
				took := time.Since(start)
				if took > budget {
					t.Errorf("/status.json said %s %s after %s (budget %s)", name, got, took.Round(100*time.Millisecond), budget)
				}
				return took
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("/status.json never said %s %v within %s (last: %v)", name, states, budget+30*time.Second, m.status())
	return 0
}

// segmentLines counts the lines in a service's segments (the log keeper, story 53.02).
func (m *appMachine) segmentFiles(service string) int {
	files, _ := filepath.Glob(filepath.Join(m.root, "logs", "segments", service, "*.ndjson*"))
	return len(files)
}

// recoveryPass is an owner's browser with a recovery pass (story 53.04), asked while the
// API is up.
func (m *appMachine) recoveryPass() *ownerSession {
	t := m.r.t
	t.Helper()
	a := m.signIn()
	code, body := a.b.try("POST", m.webURL()+"/api/auth/recovery-pass", map[string]any{}, a.csrf)
	if code != 200 {
		t.Fatalf("the recovery pass: %d %s", code, tail(string(body), 300))
	}
	return a
}

// =====================================================================================
// F03 Streaming crash: the shell serves, the status says playback is down within 15 s,
// streaming is back within 60 s, and its lines are in its own segments.
// =====================================================================================

func TestFaultRehearsalF03StreamingCrash(t *testing.T) {
	r := newRehearsal(t)
	home := r.faultHome()
	home.untilGroup("streaming", time.Minute, "ok")
	home.kill9("streaming", "maktaba-streaming")
	took := home.untilGroup("streaming", 15*time.Second, "restarting", "down", "degraded")
	home.shellServes(time.Second)
	back := home.untilGroup("streaming", 60*time.Second, "ok")
	r.summary(fmt.Sprintf("| %s | streaming down in /status.json, then back | %s / %s | 15s / 60s |", t.Name(),
		took.Round(100*time.Millisecond), back.Round(time.Second)))
	if home.segmentFiles("streaming") == 0 {
		t.Errorf("streaming's lines are in no segment (the log keeper, story 53.02)")
	}
	home.assertServing(r.kit.cur)
}

// =====================================================================================
// F06 API crash: the shell serves, /status.json says the home is down at once, the
// restarting page answers for the API, and the API is back within 30 s.
// =====================================================================================

func TestFaultRehearsalF06APICrash(t *testing.T) {
	r := newRehearsal(t)
	home := r.faultHome()
	home.untilGroup("api", time.Minute, "ok")
	home.kill9("the API", "maktaba-api", "serve")
	took := home.untilGroup("api", 5*time.Second, "restarting", "down")
	if doc := home.status(); doc == nil || doc["overall"] != "down" {
		t.Errorf("with the API down, overall must be down: %v", doc)
	}
	home.shellServes(time.Second)
	back := home.untilGroup("api", 30*time.Second, "ok")
	r.summary(fmt.Sprintf("| %s | the API down in /status.json, then back | %s / %s | 5s / 30s |", t.Name(),
		took.Round(100*time.Millisecond), back.Round(time.Second)))
	home.assertServing(r.kit.cur)
}

// =====================================================================================
// F04 Postgres killed: the shell serves, the database reads not ok, and the recovery
// console opens with the admin's pass (no Postgres needed) within 10 s, showing Postgres.
// =====================================================================================

func TestFaultRehearsalF04PostgresKilled(t *testing.T) {
	r := newRehearsal(t)
	home := r.faultHome()
	a := home.recoveryPass()
	home.untilGroup("database", time.Minute, "ok")
	start := time.Now()
	home.kill9("postgres", "postgres", "-D")
	home.untilGroup("database", 25*time.Second, "restarting", "down", "degraded")
	home.shellServes(time.Second)
	var state map[string]any
	waitFor(t, "the recovery console with Postgres down", 30*time.Second, func() bool {
		code, body := a.b.try("GET", home.webURL()+"/recovery/api/state", nil, nil)
		if code != 200 {
			return false
		}
		return json.Unmarshal(body, &state) == nil
	})
	took := time.Since(start)
	if took > 10*time.Second {
		t.Errorf("the recovery console took %s with Postgres down (budget 10s)", took.Round(100*time.Millisecond))
	}
	if !strings.Contains(fmt.Sprint(state["components"]), "core.postgres") {
		t.Errorf("the console does not list Postgres: %v", state)
	}
	if code, _ := a.b.try("GET", home.webURL()+"/recovery", nil, nil); code != 200 {
		t.Errorf("the recovery page: %d", code)
	}
	if code, _ := newBrowser(t).try("GET", home.webURL()+"/recovery/api/state", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("the console without a pass: %d, want 401", code)
	}
	r.summary(fmt.Sprintf("| %s | the recovery console with Postgres down | %s | 10s |", t.Name(), took.Round(100*time.Millisecond)))
	home.untilGroup("database", 2*time.Minute, "ok")
	home.assertServing(r.kit.cur)
}

// =====================================================================================
// F10 Safe mode: the console's "Start in safe mode" restarts the home with only the safe
// set; /status.json says safe; "Leave safe mode" brings it back.
// =====================================================================================

func TestFaultRehearsalF10SafeMode(t *testing.T) {
	r := newRehearsal(t)
	home := r.faultHome()
	a := home.recoveryPass()
	act := func(action string) {
		code, body := a.b.try("POST", home.webURL()+"/recovery/api/action",
			map[string]any{"action": action, "confirm": 1}, map[string]string{"X-Maktaba-Recovery": "1"})
		if code != 200 {
			t.Fatalf("%s: %d %s", action, code, tail(string(body), 300))
		}
	}
	act("safe_mode")
	waitFor(t, "safe mode", 5*time.Minute, func() bool {
		doc := home.status()
		return doc != nil && doc["mode"] == "safe"
	})
	waitFor(t, "processing and streaming off in safe mode", 2*time.Minute, func() bool {
		return home.group("processing") == "off" && home.group("streaming") == "off" && home.group("api") == "ok"
	})
	if _, err := os.Stat(filepath.Join(home.root, "state", "safe-mode")); err == nil {
		t.Errorf("the safe-mode flag must be gone once read (one start)")
	}
	a.login() // the restart ended the session: a fresh pass with it
	a = home.recoveryPass()
	act("leave_safe")
	waitFor(t, "a normal start", 5*time.Minute, func() bool {
		doc := home.status()
		return doc != nil && doc["mode"] == "normal" && home.group("streaming") == "ok"
	})
	home.assertServing(r.kit.cur)
}
