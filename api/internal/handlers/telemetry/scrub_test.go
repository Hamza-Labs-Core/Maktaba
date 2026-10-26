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

// Error-level records are counted by their message and "event" attribute only
// (never their other attributes); lower levels are not; every record still
// reaches the wrapped handler.
func TestErrorCounts(t *testing.T) {
	var buf strings.Builder
	c := &ErrorCounts{}
	log := slog.New(c.Handler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	log.Error("scan failed", "path", "/srv/secret/x.mp4", "event", "scan_failed")
	log.Error("scan failed", "path", "/srv/other.mp4", "event", "scan_failed")
	log.With("event", "db_down").Error("query failed")
	log.Warn("slow")
	log.Info("hello")
	got := c.Take()
	want := []ErrorCount{{"scan_failed: scan failed", 2}, {"db_down: query failed", 1}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
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
		c.add(strings.Repeat("x", i+1))
	}
	if n := len(c.Take()); n != maxErrorKinds+1 {
		t.Fatalf("%d kinds, want %d", n, maxErrorKinds+1)
	}
}

func TestScrubErrors(t *testing.T) {
	got := scrubErrors([]ErrorCount{
		{"open /srv/a/1.mp4: denied", 3},
		{"dial tcp 10.0.0.1:5432: refused", 2},
		{"open /srv/b/2.mp4: denied", 2},
	})
	if len(got) != 2 || got[0] != (ErrorCount{"open <path>/<file>.mp4: denied", 5}) ||
		got[1] != (ErrorCount{"dial tcp <address>: refused", 2}) {
		t.Fatalf("scrubErrors = %+v", got)
	}
}
