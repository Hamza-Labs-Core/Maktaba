package logkeeper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newKeeper(t *testing.T, free int64, mod ...func(*Config)) (*Keeper, *clock, *bytes.Buffer) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 6, 10, 15, 0, 0, time.UTC)}
	var echo bytes.Buffer
	cfg := Config{
		Root: t.TempDir(), Node: "home", Now: c.now, Echo: &echo,
		MinFree: 5 << 30, Free: func(string) int64 { return free },
		Component: func(role, instance string) string {
			if role == "pipeline" && strings.HasPrefix(instance, "stt-") {
				return "transcription"
			}
			return "core." + role
		},
	}
	for _, m := range mod {
		m(&cfg)
	}
	k := New(cfg)
	k.segs.compress = compressFile // a closed hour is compressed before the next step
	t.Cleanup(k.Close)
	return k, c, &echo
}

func lines(t *testing.T, k *Keeper, q Query) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range k.Read(q).Lines {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// T01: a child's JSON lines are kept, its plain lines wrapped as raw_line; service, node,
// ts and component are filled, and each line is scrubbed again.
func TestCaptureKeepsJSONAndWrapsRawLines(t *testing.T) {
	k, _, _ := newKeeper(t, -1)
	f, err := k.Spool("pipeline", "stt-0")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, `{"ts":"2026-11-06T10:14:59Z","level":"warning","event":"stage_retry","url":"/a?token=abc","user_id":"42"}`)
	fmt.Fprintln(f, "Traceback (most recent call last): open /Users/sara/x failed")
	fmt.Fprint(f, `2026-11-06 10:15:00.100 UTC [23576] LOG:  checkpoint`) // no newline yet
	_ = f.Close()
	k.Flush()
	got := lines(t, k, Query{Since: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)})
	if len(got) != 2 {
		t.Fatalf("want 2 whole lines (the partial one waits), got %v", got)
	}
	// newest first: the traceback was stamped "now", after the structured line's ts
	raw, rec := got[0], got[1]
	if rec["msg"] != "stage_retry" || rec["level"] != "warn" || rec["url"] != "/a?token=[REDACTED]" {
		t.Errorf("structured line: %v", rec)
	}
	if _, ok := rec["user_id"]; ok {
		t.Errorf("a raw user id reached the segment: %v", rec)
	}
	for _, l := range got {
		if l["service"] != "pipeline" || l["node"] != "home" || l["component"] != "transcription" || l["instance"] != "stt-0" {
			t.Errorf("origin fields: %v", l)
		}
	}
	if raw["msg"] != "raw_line" || raw["level"] != "error" {
		t.Errorf("raw line: %v", raw)
	}
	if text := raw["attrs"].(map[string]any)["text"]; text != "Traceback (most recent call last): open ~/x failed" {
		t.Errorf("raw text not scrubbed: %v", text)
	}
	if _, err := os.Stat(filepath.Join(k.Root(), "segments", "pipeline.stt-0", "20261106-10.ndjson")); err != nil {
		t.Errorf("the hour's segment: %v", err)
	}
}

// The lines of each service stay in its own segments, in order, across a restart of the
// process (the same spool, read from where it stopped) and of the keeper (its offset).
func TestEachServiceInItsOwnSegmentAcrossRestarts(t *testing.T) {
	k, _, _ := newKeeper(t, -1)
	api, _ := k.Spool("api", "")
	pipe, _ := k.Spool("pipeline", "")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(api, `{"level":"info","msg":"api_line","n":%d,"ts":"2026-11-06T10:00:%02d.%03dZ"}`+"\n", i, i/10, i)
		fmt.Fprintf(pipe, `{"level":"info","msg":"pipe_line","n":%d}`+"\n", i)
	}
	_ = api.Close() // the api crashed
	k.Flush()
	api2, _ := k.Spool("api", "") // restarted
	fmt.Fprintln(api2, `{"level":"info","msg":"api_line","n":100,"ts":"2026-11-06T10:00:59Z"}`)
	_ = api2.Close()
	k.Flush()
	k.Close()
	// a new keeper (the supervisor restarted) reads nothing twice
	k2 := New(Config{Root: k.Root(), Node: "home", Now: k.cfg.Now})
	if _, err := k2.Spool("api", ""); err != nil {
		t.Fatal(err)
	}
	k2.Flush()
	got := lines(t, k2, Query{Services: []string{"api"}, Since: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), Limit: 1000})
	if len(got) != 101 {
		t.Fatalf("want 101 api lines, got %d", len(got))
	}
	for i, l := range got {
		if l["msg"] != "api_line" || int(l["n"].(float64)) != 100-i {
			t.Fatalf("line %d: %v", i, l)
		}
	}
	k2.Close()
}

// T02: a child is never blocked (it writes a file); under the disk floors the low levels
// are dropped, warn and above are kept, and log_lines_dropped says how many.
func TestLowDiskDropsLowLevelsAndSaysSo(t *testing.T) {
	k, _, echo := newKeeper(t, 900<<20) // under the 1 GB hard floor
	f, _ := k.Spool("pipeline", "")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			lv := "debug"
			if i%250 == 0 {
				lv = "warn"
			} else if i%2 == 0 {
				lv = "info"
			}
			fmt.Fprintf(f, `{"level":%q,"msg":"tick","n":%d}`+"\n", lv, i)
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the child was blocked writing its log")
	}
	_ = f.Close()
	k.Flush()
	if !k.LowDisk() {
		t.Error("LowDisk should say so")
	}
	got := lines(t, k, Query{MinLevel: "debug", Since: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), Limit: 5000})
	warns, dropped := 0, 0.0
	for _, l := range got {
		switch l["msg"] {
		case "tick":
			if l["level"] != "warn" {
				t.Fatalf("a %s line was written under the hard floor", l["level"])
			}
			warns++
		case "log_lines_dropped":
			dropped = l["attrs"].(map[string]any)["total"].(float64)
		}
	}
	if warns != 20 || dropped != 4980 {
		t.Fatalf("warn lines %d (want 20), dropped %v (want 4980)", warns, dropped)
	}
	if !strings.Contains(echo.String(), "log_lines_dropped") {
		t.Error("server.log should carry the drop line")
	}
}

// Between MinFree and HardFree only debug lines go.
func TestUnderMinFreeOnlyDebugStops(t *testing.T) {
	k, _, _ := newKeeper(t, 3<<30)
	f, _ := k.Spool("api", "")
	fmt.Fprintln(f, `{"level":"debug","msg":"d"}`)
	fmt.Fprintln(f, `{"level":"info","msg":"i"}`)
	_ = f.Close()
	k.Flush()
	got := lines(t, k, Query{MinLevel: "debug", Since: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)})
	msgs := []string{}
	for _, l := range got {
		msgs = append(msgs, l["msg"].(string))
	}
	if strings.Join(msgs, ",") != "log_lines_dropped,i" && strings.Join(msgs, ",") != "i,log_lines_dropped" {
		t.Fatalf("got %v", msgs)
	}
}

// server.log keeps every line as it came (EchoAll, this release), or else warn and above.
func TestServerLogEcho(t *testing.T) {
	for _, all := range []bool{true, false} {
		k, _, echo := newKeeper(t, -1, func(c *Config) { c.EchoAll = all })
		f, _ := k.Spool("api", "")
		fmt.Fprintln(f, "plain info words")
		fmt.Fprintln(f, `{"level":"error","msg":"boom"}`)
		_ = f.Close()
		k.Flush()
		s := echo.String()
		if all && !strings.Contains(s, "plain info words\n") {
			t.Errorf("EchoAll: %q", s)
		}
		if !all && (strings.Contains(s, "plain info words") || !strings.Contains(s, `"msg":"boom"`)) {
			t.Errorf("warn+ only: %q", s)
		}
	}
}

// T04: an hour's file rolls at MaxSegment; a closed hour is compressed and still read.
func TestRollAndCompress(t *testing.T) {
	k, c, _ := newKeeper(t, -1)
	k.segs.maxSize = 1 << 20 // MaxSegment (64 MB), scaled down
	pad := strings.Repeat("x", 1000)
	f, _ := k.Spool("api", "")
	for i := 0; i < 1100; i++ { // ~1.1 MB
		fmt.Fprintf(f, `{"level":"info","msg":"big","n":%d,"pad":%q}`+"\n", i, pad)
		if i%200 == 0 {
			k.Flush()
		}
	}
	k.Flush()
	dir := filepath.Join(k.Root(), "segments", "api")
	if _, err := os.Stat(filepath.Join(dir, "20261106-10.ndjson"+Zst)); err != nil {
		t.Fatalf("the full part should be closed and compressed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "20261106-10-1.ndjson")); err != nil {
		t.Fatalf("the hour should go on in part 1: %v", err)
	}
	c.add(time.Hour)
	fmt.Fprintln(f, `{"level":"info","msg":"next_hour"}`)
	_ = f.Close()
	k.Flush()
	if _, err := os.Stat(filepath.Join(dir, "20261106-11.ndjson")); err != nil {
		t.Fatalf("a new hour, a new file: %v", err)
	}
	got := k.Read(Query{Services: []string{"api"}, Since: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), Limit: 2000})
	if len(got.Lines) != 1101 || got.Truncated {
		t.Fatalf("read %d lines (truncated %v), want 1101", len(got.Lines), got.Truncated)
	}
}

// T03 and T07: retention by kind (debug 24 h, the rest 7 days), then the cap: the oldest
// hours go first, each service's newest 24 h of warn+ lines last.
func TestJanitorRetentionAndCap(t *testing.T) {
	k, c, _ := newKeeper(t, -1, func(c *Config) { c.MaxSegments = 300 << 10 })
	write := func(svc, level string, n int) {
		f, _ := k.Spool(svc, "")
		for i := 0; i < n; i++ {
			fmt.Fprintf(f, `{"level":%q,"msg":"m","pad":%q}`+"\n", level, strings.Repeat("y", 1000))
		}
		_ = f.Close()
		k.Flush()
	}
	// day 0: debug and info from the noisy pipeline, one warn from the quiet api
	write("pipeline", "debug", 100)
	write("pipeline", "info", 150) // ~150 KB
	write("api", "warn", 1)
	c.add(25 * time.Hour)
	k.Flush()
	k.Janitor()
	if fs := k.files(); hasKind(fs, "debug") {
		t.Fatal("debug lines over 24 h old must go")
	}
	// day 1..: more info, over the cap
	for h := 0; h < 3; h++ {
		write("pipeline", "info", 150)
		c.add(time.Hour)
		k.Flush()
	}
	write("api", "warn", 1) // the api's newest warn
	k.Janitor()
	var total int64
	for _, f := range k.files() {
		total += f.size
	}
	if total > 300<<10 {
		t.Fatalf("segments %d bytes, over the cap", total)
	}
	got := lines(t, k, Query{Services: []string{"api"}, MinLevel: "warn", Since: c.now().Add(-48 * time.Hour)})
	if len(got) == 0 {
		t.Fatal("the quiet service's newest warn lines must stay")
	}
	c.add(8 * 24 * time.Hour)
	k.Flush()
	k.Janitor()
	if fs := k.files(); len(fs) != 0 {
		t.Fatalf("after 7 days everything goes: %v", fs)
	}
}

func hasKind(fs []segmentFile, kind string) bool {
	for _, f := range fs {
		if f.kind == kind {
			return true
		}
	}
	return false
}

// T05: the reader answers a window, a level, a component, a text; errors only reads the
// warn/ copies.
func TestReaderFilters(t *testing.T) {
	k, c, _ := newKeeper(t, -1)
	api, _ := k.Spool("api", "")
	stt, _ := k.Spool("pipeline", "stt-0")
	fmt.Fprintln(api, `{"level":"info","msg":"early"}`)
	k.Flush()
	c.add(2 * time.Hour)
	fmt.Fprintln(api, `{"level":"error","msg":"api_failed","detail":"Plex unreachable"}`)
	fmt.Fprintln(stt, `{"level":"error","msg":"stt_failed"}`)
	fmt.Fprintln(stt, `{"level":"info","msg":"stt_ok"}`)
	_ = api.Close()
	_ = stt.Close()
	k.Flush()
	since := c.now().Add(-15 * time.Minute)
	if got := lines(t, k, Query{Services: []string{"api"}, MinLevel: "error", Since: since}); len(got) != 1 || got[0]["msg"] != "api_failed" {
		t.Errorf("api errors, last 15 min: %v", got)
	}
	if got := lines(t, k, Query{Component: "transcription", Since: since}); len(got) != 2 {
		t.Errorf("by component: %v", got)
	}
	if got := lines(t, k, Query{Text: "plex", Since: since}); len(got) != 1 {
		t.Errorf("by text: %v", got)
	}
	if got := lines(t, k, Query{Since: c.now().Add(-3 * time.Hour), Limit: 1}); len(got) != 1 || got[0]["msg"] == "early" {
		t.Errorf("newest first, limited: %v", got)
	}
	start := time.Now()
	_ = k.Read(Query{MinLevel: "error", Since: since})
	if d := time.Since(start); d > time.Second {
		t.Errorf("the reader took %v", d)
	}
}

// A file another program writes (the desktop app's app.log) is followed from its end.
func TestFollowFileFromItsEnd(t *testing.T) {
	k, _, _ := newKeeper(t, -1)
	p := filepath.Join(t.TempDir(), "app.log")
	_ = os.WriteFile(p, []byte(`{"level":"info","msg":"before"}`+"\n"), 0o600)
	k.FollowFile(p, Origin{Service: "desktop"})
	f, _ := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	fmt.Fprintln(f, `{"level":"warn","msg":"after"}`)
	_ = f.Close()
	k.Flush()
	got := lines(t, k, Query{Services: []string{"desktop"}, Since: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)})
	if len(got) != 1 || got[0]["msg"] != "after" {
		t.Fatalf("got %v", got)
	}
}

// A spool read to its end past SpoolMax is emptied; nothing is read twice or lost.
func TestSpoolIsEmptiedOnceRead(t *testing.T) {
	k, _, _ := newKeeper(t, -1, func(c *Config) { c.SpoolMax = 1 << 10 })
	f, _ := k.Spool("api", "")
	for round := 0; round < 3; round++ {
		for i := 0; i < 50; i++ {
			fmt.Fprintf(f, `{"level":"info","msg":"m","r":%d,"n":%d}`+"\n", round, i)
		}
		k.Flush()
	}
	_ = f.Close()
	fi, _ := os.Stat(filepath.Join(k.Root(), "spool", "api.out"))
	if fi.Size() != 0 {
		t.Errorf("spool %d bytes, want emptied", fi.Size())
	}
	if got := k.Read(Query{Since: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), Limit: 1000}); len(got.Lines) != 150 {
		t.Fatalf("%d lines, want 150", len(got.Lines))
	}
}

// The janitor leaves an hour that is being compressed (it would come back as .zst).
func TestJanitorLeavesAnHourBeingCompressed(t *testing.T) {
	k, c, _ := newKeeper(t, -1, func(c *Config) { c.Retention = 24 * time.Hour })
	release := make(chan struct{})
	k.segs.compress = func(p string) {
		k.segs.zipping[p] = true
		go func() {
			<-release
			compressFile(p)
			k.segs.mu.Lock()
			delete(k.segs.zipping, p)
			k.segs.mu.Unlock()
		}()
	}
	f, _ := k.Spool("api", "")
	fmt.Fprintln(f, `{"level":"info","msg":"m"}`)
	_ = f.Close()
	k.Flush()
	c.add(3 * 24 * time.Hour)
	k.Flush() // the hour closes; its compression waits
	k.Janitor()
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		k.segs.mu.Lock()
		n := len(k.segs.zipping)
		k.segs.mu.Unlock()
		if n == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fs := k.files(); len(fs) != 1 || !strings.HasSuffix(fs[0].path, Zst) {
		t.Fatalf("the compressed hour should be there for the next janitor: %v", fs)
	}
	k.Janitor()
	if fs := k.files(); len(fs) != 0 {
		t.Fatalf("then it goes: %v", fs)
	}
}

// A stop waits for the hours still being compressed: none is left half written.
func TestCloseWaitsForCompression(t *testing.T) {
	k, c, _ := newKeeper(t, -1)
	// in the background, as in production, on a slow disk
	k.segs.compress = func(p string) {
		k.segs.zipping[p] = true
		k.segs.zips.Add(1)
		go func() {
			defer k.segs.zips.Done()
			time.Sleep(50 * time.Millisecond)
			compressFile(p)
			k.segs.mu.Lock()
			delete(k.segs.zipping, p)
			k.segs.mu.Unlock()
		}()
	}
	f, _ := k.Spool("api", "")
	fmt.Fprintln(f, `{"level":"info","msg":"first_hour"}`)
	k.Flush()
	c.add(time.Hour)
	fmt.Fprintln(f, `{"level":"info","msg":"next_hour"}`)
	_ = f.Close()
	k.Flush()
	k.Close()
	dir := filepath.Join(k.Root(), "segments", "api")
	if _, err := os.Stat(filepath.Join(dir, "20261106-10.ndjson"+Zst)); err != nil {
		t.Fatalf("the closed hour is compressed before Close returns: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "20261106-10.ndjson")); err == nil {
		t.Fatal("its uncompressed file is gone before Close returns")
	}
}

// Story 53.11: a retention the admin shortens applies at the next janitor pass, with no
// restart.
func TestSetLimitsAppliesAtTheNextPass(t *testing.T) {
	k, c, _ := newKeeper(t, -1)
	f, _ := k.Spool("api", "")
	fmt.Fprintln(f, `{"level":"info","msg":"old"}`)
	_ = f.Close()
	k.Flush()
	c.add(3 * 24 * time.Hour)
	k.Flush()
	k.Janitor()
	if len(k.files()) == 0 {
		t.Fatal("within 7 days the hour stays")
	}
	k.SetLimits(48*time.Hour, 0, 0)
	k.Janitor()
	if fs := k.files(); len(fs) != 0 {
		t.Fatalf("over the new 2 days it goes: %v", fs)
	}
}
