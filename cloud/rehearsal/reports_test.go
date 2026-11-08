//go:build cloudrehearsal

package rehearsal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/symbols"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/symbols/symbolstest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
)

// report-symbolicated (Epic 53 story 53.16 T07): the canary home, online on the relay, sends two
// problem reports through its relay agent's reports route (cloudlink.ReportsHandler, as
// maktaba-cloudlink serves it to the home) to this commit's api role: an Apple crash and an
// Android (R8) crash. Before any symbol table, the service admin reads raw frames and the missing
// tables. Then the tables are made in the job (the Apple fixture's dSYM, the test mapping.txt)
// and uploaded with a token made in the job (never a real secret), as the release workflows
// upload them; a wrong token is refused. After, both crashes read as functions, files and lines.
func reportSymbolicated(t *testing.T, c *cluster) Result {
	start := time.Now()
	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	tok := hex.EncodeToString(token)
	work := filepath.Join(c.dir, ".work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "symbols.token"), []byte(tok+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// the canary home: seeded, and online on the relay
	ids, err := seed.Homes(context.Background(), c.db, 0, 1, "pro")
	if err != nil {
		t.Fatal(err)
	}
	c.startHomes(ids, 0, 1, 1)
	_, secret, slug := simhome.Creds(0)

	// the api role, with its logs kept beside the scenario's
	t.Cleanup(func() {
		logs, _ := c.compose("--profile", "api", "logs", "--no-color", "--timestamps", "api")
		_ = os.WriteFile(filepath.Join(outDir(t), "report-symbolicated-api.log"), []byte(logs), 0o644)
		if os.Getenv("REHEARSAL_KEEP") == "" {
			_, _ = c.compose("--profile", "api", "rm", "-fsv", "api")
		}
	})
	c.must("--profile", "api", "up", "-d", "--wait", "api")
	ports := portsFor(c.project)
	api := fmt.Sprintf("http://127.0.0.1:%d", ports[1]+503)
	console := fmt.Sprintf("http://127.0.0.1:%d", ports[1]+504)
	users := &http.Client{Timeout: 30 * time.Second, Transport: hostTransport{"api.test"}}
	admin := &http.Client{Timeout: 30 * time.Second, Transport: hostTransport{"admin.test"}}

	// the home's agent: the home posts to it on loopback, it passes the report on as the home
	agent := cloudlink.ReportsHandler(&cloudlink.LinkReporter{CloudAPI: api,
		Creds: cloudlink.Credentials{ServerID: ids[slug], Secret: secret}, Client: users})
	tables, err := symbols.ExtractPath(symbolstest.Fixture(t, "crashy.dwarf"))
	if err != nil {
		t.Fatal(err)
	}
	var arm *symbols.Table
	for _, x := range tables {
		if x.Arch == "arm64" {
			arm = x
		}
	}
	if arm == nil {
		t.Fatal("the fixture has no arm64 table")
	}
	depth := funcOffset(t, arm, "depth")
	send := func(id int, app, version, platform string, crash map[string]any) {
		t.Helper()
		rep := map[string]any{"id": id, "when": time.Now().UTC().Format(time.RFC3339), "app": app, "version": version,
			"platform": platform, "category": "playback", "text": "it crashed", "crashes": []any{crash}}
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		raw, _ := json.Marshal(rep)
		_ = mw.WriteField("report", string(raw))
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/admin/cloud-link/reports", &b)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		agent(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("report %d through the agent: %d %s", id, rec.Code, rec.Body)
		}
	}
	send(1, "apple.player", "0.3.102", "ios", map[string]any{
		"platform": "cocoa",
		"exception": map[string]any{"values": []any{map[string]any{"type": "EXC_BAD_ACCESS", "value": "SIGSEGV",
			"stacktrace": map[string]any{"frames": []any{
				map[string]any{"function": fmt.Sprintf("crashy + 0x%x", depth+8), "package": "crashy", "in_app": true},
			}}}}},
		"debug_meta": map[string]any{"images": []any{map[string]any{"type": "macho", "code_file": "crashy",
			"debug_id": strings.ToUpper(arm.DebugID)}}},
	})
	send(2, "android.player", "0.3.102", "android", map[string]any{
		"platform": "java",
		"exception": map[string]any{"values": []any{map[string]any{"type": "a.b.e", "value": "a.b.e",
			"stacktrace": map[string]any{"frames": []any{
				map[string]any{"function": "android.os.Handler.dispatchMessage", "filename": "Handler.java", "lineno": 106},
				map[string]any{"function": "a.b.c.b", "filename": "SourceFile", "lineno": 5, "in_app": true},
			}}}}},
	})

	// the service admin, signed in on the console's listener
	email := "ops@rehearsal.test"
	hash, err := argon2id.Hash("rehearsal-ops-password", func() argon2id.Params {
		p := argon2id.DefaultParams()
		p.Memory, p.Time = 1024, 1
		return p
	}())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`INSERT INTO users (email, password_hash, display_name, email_verified, role)
        VALUES ($1, $2, 'Ops', true, 'service_admin')`, email, hash); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"email": email, "password": "rehearsal-ops-password"})
	req, _ := http.NewRequest(http.MethodPost, console+"/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://admin.test")
	req.Header.Set("X-Maktaba-Staff", "1")
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if code, b := call(t, admin, req, &login); code != http.StatusOK || login.AccessToken == "" {
		t.Fatalf("the service admin's sign-in: %d %s", code, b)
	}
	crashes := func() map[string]symbols.Crash {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, console+"/v1/admin/reports", nil)
		req.Header.Set("Authorization", "Bearer "+login.AccessToken)
		var list struct {
			Reports []struct {
				ID  int64  `json:"id"`
				App string `json:"app"`
			} `json:"reports"`
		}
		if code, b := call(t, admin, req, &list); code != http.StatusOK || len(list.Reports) != 2 {
			t.Fatalf("the reports: %d %s", code, b)
		}
		out := map[string]symbols.Crash{}
		for _, r := range list.Reports {
			req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/admin/reports/%d", console, r.ID), nil)
			req.Header.Set("Authorization", "Bearer "+login.AccessToken)
			var v struct {
				Crashes []symbols.Crash `json:"crashes"`
			}
			if code, b := call(t, admin, req, &v); code != http.StatusOK || len(v.Crashes) != 1 {
				t.Fatalf("report %d: %d %s", r.ID, code, b)
			}
			out[r.App] = v.Crashes[0]
		}
		return out
	}
	before := crashes()
	if a := before["apple.player"]; a.Frames[0].Symbol != nil || len(a.Missing) != 1 || a.Missing[0].DebugID != arm.DebugID {
		t.Fatalf("the Apple crash before any table: %+v", a)
	}
	if d := before["android.player"]; d.Frames[0].Symbol != nil || len(d.Missing) != 1 || d.Missing[0].Kind != symbols.KindR8 {
		t.Fatalf("the Android crash before any mapping: %+v", d)
	}

	// the tables, made here, uploaded as the release workflows do
	f, err := os.Open(filepath.Join(repoRoot(t), "cloud/internal/symbols/testdata/mapping.txt"))
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := symbols.ParseR8(f, "android.player", "0.3.102")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	upload := func(token string, tab *symbols.Table) int {
		t.Helper()
		b, err := tab.Encode()
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, api+"/v1/symbols?version=v0.3.102", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/gzip")
		code, _ := call(t, users, req, nil)
		return code
	}
	if code := upload("not-the-token", arm); code != http.StatusUnauthorized {
		t.Fatalf("a wrong token: %d", code)
	}
	for _, tab := range append(tables, mapping) {
		if code := upload(tok, tab); code != http.StatusCreated {
			t.Fatalf("upload %s %s: %d", tab.CodeFile, tab.Arch, code)
		}
	}

	after := crashes()
	a, d := after["apple.player"], after["android.player"]
	if s := a.Frames[0].Symbol; s == nil || s.Function != "depth" || s.File != "/src/crashy.c" || s.Line != 2 ||
		len(a.Missing) != 0 || a.Symbols != symbols.SymbolsDSYM {
		t.Errorf("the Apple crash after the upload: %+v", a)
	}
	if s := d.Frames[0].Symbol; s == nil || s.Function != "com.hamzalabs.maktaba.player.PlaybackController.stop" ||
		s.File != "PlaybackController.kt" || s.Line != 71 || len(d.Missing) != 0 || d.Symbols != symbols.SymbolsMapping ||
		d.Type != "com.hamzalabs.maktaba.player.PlaybackError" {
		t.Errorf("the Android crash after the upload: %+v", d)
	}
	took := time.Since(start)
	t.Logf("two reports through the canary home's agent, %d tables uploaded, both crashes symbolicated in %s", len(tables)+1, took)
	return Result{Requests: 2, Notes: []string{fmt.Sprintf("an Apple and an Android crash symbolicated (%d tables made in the job) in %s",
		len(tables)+1, took)}}
}

// hostTransport sends every request with one Host: the api answers its own names only.
type hostTransport struct{ host string }

func (h hostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = h.host
	return http.DefaultTransport.RoundTrip(r)
}

// call does req, decoding a 2xx JSON answer into v: its status and body.
func call(t *testing.T, cl *http.Client, req *http.Request, v any) (int, string) {
	t.Helper()
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if v != nil && resp.StatusCode/100 == 2 {
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("%s %s: %v: %s", req.Method, req.URL.Path, err, b)
		}
	}
	return resp.StatusCode, string(b)
}

// funcOffset is a function's first byte in a table, as an offset into __TEXT.
func funcOffset(t *testing.T, tab *symbols.Table, fn string) uint64 {
	t.Helper()
	for i, n := range tab.Names {
		if n != fn {
			continue
		}
		for _, f := range tab.Funcs {
			if int(f[2]) == i {
				return f[0] - tab.TextAddr
			}
		}
	}
	t.Fatalf("no %s in the table", fn)
	return 0
}
