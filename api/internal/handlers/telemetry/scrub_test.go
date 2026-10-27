package telemetry

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	red := NewRedactor([]string{"/srv/Lectures", "/Volumes/مكتبة"})
	for _, c := range []struct{ in, want string }{
		// a library's file: neither its folders nor its name, only the extension
		{"open /srv/Lectures/Sheikh Name/أسماء الله الحسنى.mp4: permission denied",
			"open <library>/<file>.mp4: permission denied"},
		{`stat "/Volumes/مكتبة/a b/c.mkv"`, `stat "<library>/<file>.mkv"`},
		// another path, and a home folder
		{"read /var/lib/maktaba/cache/x.db: disk full", "read <path>/<file>.db: disk full"},
		{"config at /Users/sara/Library/Maktaba", "config at <home>"},
		// a path runs to ": " or the line's end (a name with spaces must not leak a
		// word), so what follows it on the line goes too
		{`C:\Users\sara\maktaba\server.toml missing`, `<home>`},
		{`C:\Users\sara\maktaba\server.toml: missing`, `<home>/<file>.toml: missing`},
		// addresses
		{"dial tcp 192.168.1.20:8088: connect: refused", "dial tcp <address>: connect: refused"},
		{"GET http://nas.local:8088/api/videos?q=secret failed", "GET <address>/api/videos failed"},
		{"lookup home.example.com: no such host", "lookup <address>: no such host"},
		{"mail to sara@example.org bounced", "mail to <address> bounced"},
		{"listen [fe80::1ff:fe23:4567:890a]:8080", "listen [<address>]:8080"},
		// what names nothing stays
		{"context deadline exceeded", "context deadline exceeded"},
		{"pq: relation \"videos\" does not exist", "pq: relation \"videos\" does not exist"},
	} {
		if got := red.Apply(c.in); got != c.want {
			t.Errorf("Apply(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestRange(t *testing.T) {
	for n, want := range map[int64]string{
		-1: "0", 0: "0", 1: "1", 2: "2-5", 5: "2-5", 6: "6-20", 20: "6-20", 21: "21-100", 100: "21-100",
		101: "101-1k", 1000: "101-1k", 1001: "1k-10k", 10000: "1k-10k", 10001: "10k-100k", 100001: "100k+",
	} {
		if got := Range(n); got != want {
			t.Errorf("Range(%d) = %q, want %q", n, got, want)
		}
	}
	seen := map[string]bool{}
	for _, r := range Ranges {
		seen[r] = true
	}
	for n := int64(0); n < 200000; n += 997 {
		if !seen[Range(n)] {
			t.Fatalf("Range(%d) = %q is not one of Ranges", n, Range(n))
		}
	}
}

// Error-level records are counted by their "event" attribute alone (never their
// message or other attributes, where a path or a title could be); one without
// a usable event is "other"; lower levels are not counted; every record still
// reaches the wrapped handler.
func TestErrorCounts(t *testing.T) {
	var buf strings.Builder
	c := &ErrorCounts{}
	log := slog.New(c.Handler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	log.Error("scan failed", "path", "/srv/secret/x.mp4", "event", "scan_failed")
	log.Error("scan failed", "path", "/srv/other.mp4", "event", "scan_failed")
	log.With("event", "db_down").Error("query failed")
	log.Error("could not open Sheikh Name's lecture")
	log.Error("x", "event", "Lecture by Sara")
	log.Warn("slow")
	log.Info("hello")
	got := c.Take()
	want := []ErrorCount{{"other", 2}, {"scan_failed", 2}, {"db_down", 1}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
	if len(c.Take()) != 0 {
		t.Fatal("Take did not start again")
	}
	if !strings.Contains(buf.String(), "slow") || strings.Contains(buf.String(), "hello") ||
		strings.Count(buf.String(), "scan failed") != 2 {
		t.Fatalf("the wrapped handler did not get the records as its level says:\n%s", buf.String())
	}
	if !c.Handler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError + 4})).Enabled(context.Background(), slog.LevelError) {
		t.Fatal("an Error record must be counted even when the wrapped handler drops it")
	}
	// bounded: past maxErrorKinds, a new kind counts as "other"
	for i := 0; i < maxErrorKinds+10; i++ {
		c.add("k" + strings.Repeat("x", i%60) + string(rune('a'+i/60)))
	}
	if n := len(c.Take()); n != maxErrorKinds+1 {
		t.Fatalf("%d kinds, want %d", n, maxErrorKinds+1)
	}
}

func TestScrubErrors(t *testing.T) {
	got := scrubErrors([]ErrorCount{
		{"scan_failed", 3},
		{"open /srv/a/1.mp4: denied", 2},
		{"Sara's NAS down", 4},
		{"db_down", 0},
	})
	if len(got) != 2 || got[0] != (ErrorCount{"other", 6}) || got[1] != (ErrorCount{"scan_failed", 3}) {
		t.Fatalf("scrubErrors = %+v", got)
	}
}

// Every field goes as Maktaba Cloud takes it (it refuses a whole batch for one
// value it does not): names and codes to their characters, numbers whole and in
// range, what cannot be one dropped, an event without its point dropped.
func TestNormalize(t *testing.T) {
	for _, c := range []struct {
		kind string
		in   map[string]any
		want map[string]any
		ok   bool
	}{
		{"feature.used", map[string]any{"feature": "Live-TV", "count": 2.6}, map[string]any{"feature": "live_tv", "count": 3.0}, true},
		{"feature.used", map[string]any{"feature": "--", "count": 1.0}, nil, false},
		{"feature.used", map[string]any{"count": -4.0, "feature": "x"}, map[string]any{"feature": "x", "count": 0.0}, true},
		{"player.start", map[string]any{"source": "remux", "codec": ""}, map[string]any{}, true},
		{"player.start", map[string]any{"source": "direct", "codec": "h264 (avc1)"}, map[string]any{"source": "direct", "codec": "h264avc1"}, true},
		{"player.error", map[string]any{"error_kind": "http_404", "duration_ms": 1e12}, map[string]any{"error_kind": "http_404", "duration_ms": float64(maxDuration)}, true},
		{"app.open", map[string]any{"cold_start": "yes"}, map[string]any{}, true},
		{"error.uncaught", map[string]any{"error_message": "  "}, map[string]any{"error_message": "Error"}, true},
		{"error.uncaught", map[string]any{"error_message": "TypeError: cannot read 'أسماء الله' of undefined"}, map[string]any{"error_message": "TypeError"}, true},
		{"error.uncaught", map[string]any{"error_message": "Uncaught ChunkLoadError: /Users/sara/x.js"}, map[string]any{"error_message": "ChunkLoadError"}, true},
	} {
		got, ok := Normalize(c.kind, c.in)
		if ok != c.ok || len(got) != len(c.want) {
			t.Errorf("Normalize(%s, %v) = %v, %v", c.kind, c.in, got, ok)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("Normalize(%s, %v)[%s] = %v, want %v", c.kind, c.in, k, got[k], v)
			}
		}
	}
	if version("0.3.27", "x") != "0.3.27" || version("", "unknown") != "unknown" || version("1 2", "") != "" {
		t.Error("version")
	}
	if locale("ar") != "ar" || locale("en-US") != "en-US" || locale("123") != "" {
		t.Error("locale")
	}
}

// An error's frames keep the app's own code only: a function and a source
// file's name, never its folder, origin, query, or a file that is not code.
func TestCodeFrames(t *testing.T) {
	in := strings.Join([]string{
		"TypeError: boom",
		"    at renderRow (http://192.168.1.5:8088/assets/index-3f2a.js:12:34)",
		"    at http://nas.local:8088/assets/vendor.js?v=2:1:2",
		"    at async loadLecture (file:///Users/sara/Videos/درس.mp4:1:1)",
		"go@https://home.example.com/assets/app.mjs:9:8",
		"    at Object.<anonymous> (/Users/sara/Library/Maktaba/server.js:5:6)",
		"not a frame",
		"    at a (x.js:1:1)", "    at b (x.js:1:1)", "    at c (x.js:1:1)",
	}, "\n")
	want := strings.Join([]string{
		"renderRow (index-3f2a.js:12:34)",
		"<anonymous> (vendor.js:1:2)",
		"go (app.mjs:9:8)",
		"Object.<anonymous> (server.js:5:6)",
		"a (x.js:1:1)",
	}, "\n")
	if got := CodeFrames(in); got != want {
		t.Fatalf("CodeFrames\n got %q\nwant %q", got, want)
	}
	for _, bad := range []string{"sara", "192.168", "nas.local", "درس", "home.example", "Users", "v=2"} {
		if strings.Contains(CodeFrames(in), bad) {
			t.Errorf("frames carry %q", bad)
		}
	}
}
