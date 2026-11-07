package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// Epic 53 story 53.05 T03/T04: the running version's serve exits 3 times before it is
// up: the launcher stops starting it, writes start-failed.json (its exits, the log's last
// lines, scrubbed, and the version it can go back to), serves the can't-start page on the
// loopback web port, and a choice there starts serve again in safe mode.
func TestCantStartStopsAndOffersSafeMode(t *testing.T) {
	t.Parallel()
	processTest(t)
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "crash,crash,crash,run"}, "1.0.0")
	if err := l.Switch("2.0.0", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := l.Confirm(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "server.log")
	_ = os.WriteFile(logPath, []byte("[serve] level=error fatal: open /Users/sara/x?token=abc: denied\n"), 0o600)
	cfg := testConfig(l.Root)
	cfg.LogPath, cfg.PageAddr = logPath, freeAddr(t)
	startLauncher(t, cfg)
	recPath := filepath.Join(l.UpdatesDir(), StartFailedFile)
	// whole, not only there: on Windows a read can meet the file while its rename lands
	var rec StartFailed
	var b []byte
	waitFor(t, "start-failed.json", func() bool {
		b, _ = os.ReadFile(recPath)
		return json.Unmarshal(b, &rec) == nil
	})
	if len(rec.Exits) != CantStartMax || rec.Version != "2.0.0" ||
		rec.Rollback != "1.0.0" {
		t.Fatalf("record: %s", b)
	}
	if len(rec.LastLines) == 0 || !strings.Contains(rec.LastLines[0], "~/x?token=[REDACTED]") {
		t.Fatalf("last lines not scrubbed: %v", rec.LastLines)
	}
	time.Sleep(100 * time.Millisecond)
	if n := countAct(readTrace(l), "2.0.0", "crash"); n != CantStartMax {
		t.Fatalf("started %d times, want it stopped at %d", n, CantStartMax)
	}
	// the page, in Arabic, and its safe-mode button
	var page string
	waitFor(t, "the page", func() bool {
		req, _ := http.NewRequest(http.MethodGet, "http://"+cfg.PageAddr+"/", nil)
		req.Header.Set("Accept-Language", "ar")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		page = string(body)
		return resp.StatusCode == http.StatusServiceUnavailable
	})
	if !strings.Contains(page, "تعذّر تشغيل مكتبة") || !strings.Contains(page, `dir="rtl"`) || !strings.Contains(page, "1.0.0") {
		t.Fatalf("page: %s", page)
	}
	if resp, err := http.PostForm("http://"+cfg.PageAddr+"/launcher/choice", url.Values{"token": {"wrong"}, "choice": {"safe"}}); err != nil ||
		resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a post without the page's token: %v %v", resp, err)
	}
	tok := regexp.MustCompile(`name="token" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	if tok == nil {
		t.Fatal("no token in the page")
	}
	resp, err := http.PostForm("http://"+cfg.PageAddr+"/launcher/choice", url.Values{"token": {tok[1]}, "choice": {"safe"}})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("choice: %v %v", resp, err)
	}
	waitFor(t, "a start in safe mode", func() bool {
		b, _ := os.ReadFile(filepath.Join(l.Root, "safe"))
		return strings.TrimSpace(string(b)) == "2.0.0"
	})
	if _, err := os.Stat(recPath); err == nil {
		t.Error("the record goes once a choice is made")
	}
	if ev := events(l, "launcher_start_choice"); len(ev) != 1 || ev[0].Detail["choice"] != "safe" {
		t.Errorf("choice event: %+v", ev)
	}
}

// The app's choice (the file) works as the page's: rollback goes back to the version kept.
func TestCantStartRollbackByTheApp(t *testing.T) {
	t.Parallel()
	processTest(t)
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "crash"}, "1.0.0")
	if err := l.Switch("2.0.0", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := l.Confirm(); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(l.Root)
	cfg.PageAddr = freeAddr(t)
	startLauncher(t, cfg)
	waitFor(t, "start-failed.json", func() bool {
		_, err := os.Stat(filepath.Join(l.UpdatesDir(), StartFailedFile))
		return err == nil
	})
	if err := os.WriteFile(filepath.Join(l.UpdatesDir(), StartChoiceFile), []byte("rollback\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "1.0.0 started", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	if cur, _ := l.Current(); cur != "1.0.0" {
		t.Fatalf("current = %q", cur)
	}
}

// A run that was up StableAfter clears the count: slow failures are no cut-off.
func TestEarlyExitsNeedToBeClose(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	ln := &launcher{cfg: Config{StableAfter: time.Minute, Now: func() time.Time { return now }}}
	for i := 0; i < CantStartMax-1; i++ {
		if ln.earlyExit(runResult{code: 1, ran: time.Second}) {
			t.Fatal("too soon")
		}
	}
	ln.earlyExit(runResult{code: 0, ran: 2 * time.Minute})
	if ln.earlyExit(runResult{code: 1, ran: time.Second}) {
		t.Fatal("a good run in between resets the count")
	}
	now = now.Add(CantStartWindow + time.Second)
	ln.earlyExit(runResult{code: 1, ran: time.Second})
	if ln.earlyExit(runResult{code: 1, ran: time.Second}) {
		t.Fatal("exits more than two minutes apart do not count together")
	}
	if !ln.earlyExit(runResult{code: 1, ran: time.Second}) {
		t.Fatalf("three close exits are the cut-off: %v", ln.early)
	}
}

// A choice file caught while the app is still writing it (empty) is left for the next
// look, not removed: the choice is not lost.
func TestTakeChoiceWaitsForAWholeFile(t *testing.T) {
	l := installed(t, map[string]string{"1.0.0": "run"}, "1.0.0")
	ln := &launcher{l: l}
	p := filepath.Join(l.UpdatesDir(), StartChoiceFile)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if c := ln.takeChoice(); c != "" {
		t.Fatalf("an empty file: %q", c)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("an empty file is left for the writer")
	}
	_ = os.WriteFile(p, []byte("rollback\n"), 0o600)
	if c := ln.takeChoice(); c != "rollback" {
		t.Fatalf("the whole file: %q", c)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("a taken choice is removed")
	}
}
