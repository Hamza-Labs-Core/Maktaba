package log

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Epic 53 story 53.01 T01: the shared redaction cases (shared/redact/cases.json),
// the same table every language's logger passes.
type sharedCases struct {
	Scrub []struct{ In, Out string } `json:"scrub"`
	Keys  []struct {
		Key    string `json:"key"`
		String bool   `json:"string"`
		Masked bool   `json:"masked"`
	} `json:"keys"`
	UserHash []struct {
		KeyHex string `json:"key_hex"`
		ID     string `json:"id"`
		UserH  string `json:"user_h"`
	} `json:"user_hash"`
}

func loadSharedCases(t *testing.T) sharedCases {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "redact", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c sharedCases
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSharedRedactCases(t *testing.T) {
	c := loadSharedCases(t)
	for _, tc := range c.Scrub {
		if got := Scrub(tc.In); got != tc.Out {
			t.Errorf("Scrub(%q) = %q, want %q", tc.In, got, tc.Out)
		}
	}
	for _, tc := range c.Keys {
		if got := keySensitive(nil, tc.Key, tc.String); got != tc.Masked {
			t.Errorf("key %q (string %v) masked = %v, want %v", tc.Key, tc.String, got, tc.Masked)
		}
	}
	for _, tc := range c.UserHash {
		k, _ := hex.DecodeString(tc.KeyHex)
		if got := UserHash(k, tc.ID); got != tc.UserH {
			t.Errorf("UserHash(%s, %s) = %s, want %s", tc.KeyHex[:8], tc.ID, got, tc.UserH)
		}
	}
}

// The embedded copy is the shared file, byte for byte (tools/log-schema/sync.py).
func TestEmbeddedRulesAreTheSharedFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "redact", "rules.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, redactRulesJSON) {
		t.Fatal("shared/log/go/redact_rules.json differs from shared/redact/rules.json: run python3 tools/log-schema/sync.py")
	}
}

// T03: a line logged with a user id carries user_h and never user_id; two homes'
// keys give two hashes; with no key the id is dropped.
func TestUserIDIsWrittenAsItsHash(t *testing.T) {
	keyA := bytes.Repeat([]byte{1}, LogKeySize)
	keyB := bytes.Repeat([]byte{2}, LogKeySize)
	t.Cleanup(func() { SetUserHashKey(nil) })

	line := func() map[string]any {
		var buf bytes.Buffer
		NewLogger(Options{Service: "api", Env: "prod", Output: &buf}).Info("request_done", "user_id", 42)
		return decodeOne(t, buf.Bytes())
	}
	SetUserHashKey(keyA)
	a := line()
	if _, ok := a["user_id"]; ok {
		t.Fatalf("user_id written: %v", a)
	}
	if a["user_h"] != UserHash(keyA, "42") || len(a["user_h"].(string)) != 16 {
		t.Fatalf("user_h = %v", a["user_h"])
	}
	if again := line(); again["user_h"] != a["user_h"] {
		t.Fatal("the same user must get the same user_h on one home")
	}
	SetUserHashKey(keyB)
	if b := line(); b["user_h"] == a["user_h"] {
		t.Fatal("two homes must hash the same id differently")
	}
	SetUserHashKey(nil)
	if n := line(); n["user_h"] != nil || n["user_id"] != nil {
		t.Fatalf("with no key the id must be dropped: %v", n)
	}
}

func TestEnsureLogKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "keys", LogKeyFile)
	k1, err := EnsureLogKey(p)
	if err != nil || len(k1) != LogKeySize {
		t.Fatalf("EnsureLogKey: %v %d", err, len(k1))
	}
	k2, err := EnsureLogKey(p)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatal("an existing key must be kept")
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
	if err := os.WriteFile(p, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureLogKey(p); err == nil {
		t.Fatal("a key of the wrong size must be an error, never replaced")
	}
}

func TestComponentFromContext(t *testing.T) {
	var buf bytes.Buffer
	saved := Default
	Default = NewLogger(Options{Service: "api", Env: "prod", Output: &buf})
	t.Cleanup(func() { Default = saved })
	ctx := WithComponent(context.Background(), "transcription")
	From(ctx).Info("job_done")
	if got := decodeOne(t, buf.Bytes()); got["component"] != "transcription" || got["node"] != "home" {
		t.Fatalf("component/node: %v", got)
	}
}

func TestErrAttr(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(Options{Service: "api", Env: "prod", Output: &buf}).Error("fetch_failed",
		Err(os.ErrNotExist, "unreachable", "plex-down"))
	got := decodeOne(t, buf.Bytes())
	e, ok := got["err"].(map[string]any)
	if !ok || e["kind"] != "unreachable" || e["code"] != "plex-down" || e["msg"] != "file does not exist" {
		t.Fatalf("err = %v", got["err"])
	}
}

// The budget is ≤ 20 µs per line p99 (story 53.13): scrubbing a typical value.
func BenchmarkScrub(b *testing.B) {
	s := "GET /api/videos/01J9/stream?sig=abc&exp=2 from 192.168.1.23 took 12ms"
	for i := 0; i < b.N; i++ {
		_ = Scrub(s)
	}
}

// Epic 53 story 53.02: MAKTABA_LOG_FORMAT=json (the supervisor's) makes a dev logger
// write JSON lines.
func TestFormatFromTheEnvironment(t *testing.T) {
	t.Setenv("MAKTABA_LOG_FORMAT", "json")
	var buf bytes.Buffer
	NewLogger(Options{Service: "api", Env: "dev", Output: &buf}).Info("hello")
	decodeOne(t, buf.Bytes())
}

// Epic 53 story 53.10 T01: messages that differ in numbers, ids, paths and hosts are one
// group; another frame or service is another.
func TestFingerprintFoldsWhatVaries(t *testing.T) {
	a := Fingerprint("server", "api", GroupScrub("open /Users/sara/Movies/a.mkv: no such file (video 17)"), "handlers/videos.go")
	b := Fingerprint("server", "api", GroupScrub("open /home/omar/Films/b.mkv: no such file (video 9182)"), "handlers/videos.go")
	if a != b || len(a) != 32 {
		t.Fatalf("one failure, two groups: %s %s", a, b)
	}
	if Fingerprint("server", "streaming", GroupScrub("open /x/a.mkv: no such file"), "handlers/videos.go") == a {
		t.Error("another service is another group")
	}
	if Fingerprint("server", "api", GroupScrub("dial tcp 10.0.0.5:5432: refused"), "db.go") !=
		Fingerprint("server", "api", GroupScrub("dial tcp 192.168.1.9:5432: refused"), "db.go") {
		t.Error("addresses fold")
	}
}
