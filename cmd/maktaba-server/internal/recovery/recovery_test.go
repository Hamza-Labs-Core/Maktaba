package recovery

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

var t0 = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func sign(t *testing.T, k *rsa.PrivateKey, c map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	cb, _ := json.Marshal(c)
	in := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)
	d := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, d[:])
	if err != nil {
		t.Fatal(err)
	}
	return in + "." + enc.EncodeToString(sig)
}

func pass(sub string, admin bool, aud string, iat, exp time.Time) map[string]any {
	return map[string]any{"sub": sub, "is_admin": admin, "aud": aud, "iat": iat.Unix(), "exp": exp.Unix()}
}

// T01: only a valid, unrevoked admin's pass or access token passes.
func TestVerify(t *testing.T) {
	home, other := newKey(t), newKey(t)
	revoked := filepath.Join(t.TempDir(), "recovery-revoked.json")
	_ = os.WriteFile(revoked, []byte(`[{"user_id":"demoted","at":"`+t0.Add(-time.Hour).Format(time.RFC3339)+`"}]`), 0o600)
	v := &Verifier{PublicKeys: func() []*rsa.PublicKey { return []*rsa.PublicKey{&home.PublicKey} }, RevokedPath: revoked,
		Now: func() time.Time { return t0 }}
	week := t0.Add(7 * 24 * time.Hour)
	cases := []struct {
		name string
		tok  string
		ok   bool
	}{
		{"admin pass", sign(t, home, pass("a1", true, AudienceRecovery, t0, week)), true},
		{"admin access token", sign(t, home, pass("a1", true, AudienceAccess, t0, t0.Add(15*time.Minute))), true},
		{"viewer", sign(t, home, pass("v1", false, AudienceRecovery, t0, week)), false},
		{"expired", sign(t, home, pass("a1", true, AudienceRecovery, t0.Add(-8*24*time.Hour), t0.Add(-time.Minute))), false},
		{"wrong audience", sign(t, home, pass("a1", true, "cloud", t0, week)), false},
		{"other home", sign(t, other, pass("a1", true, AudienceRecovery, t0, week)), false},
		{"revoked after issue", sign(t, home, pass("demoted", true, AudienceRecovery, t0.Add(-2*time.Hour), week)), false},
		{"issued after the revocation", sign(t, home, pass("demoted", true, AudienceRecovery, t0, week)), true},
		{"garbage", "a.b.c", false},
	}
	for _, c := range cases {
		_, err := v.Verify(c.tok)
		if (err == nil) != c.ok {
			t.Errorf("%s: err %v, want ok %v", c.name, err, c.ok)
		}
	}
	// an unreadable revocation file refuses (fail closed)
	_ = os.WriteFile(revoked, []byte("not json"), 0o600)
	if _, err := v.Verify(sign(t, home, pass("a1", true, AudienceRecovery, t0, week))); err == nil {
		t.Error("a broken revocation file must refuse")
	}
}

// T02: a tray code works once, within two minutes.
func TestCodes(t *testing.T) {
	now := t0
	c := &Codes{Now: func() time.Time { return now }}
	a := c.Issue()
	if !c.Redeem(a) || c.Redeem(a) {
		t.Fatal("single use")
	}
	b := c.Issue()
	now = now.Add(CodeTTL + time.Second)
	if c.Redeem(b) {
		t.Fatal("expired after two minutes")
	}
}

// T03: five failures in a minute lock an address for five minutes.
func TestLockout(t *testing.T) {
	now := t0
	l := &Lockout{Now: func() time.Time { return now }}
	for i := 0; i < LockFailures-1; i++ {
		l.Fail("10.0.0.5")
	}
	if l.Locked("10.0.0.5") {
		t.Fatal("not yet")
	}
	l.Fail("10.0.0.5")
	if !l.Locked("10.0.0.5") || l.Locked("10.0.0.6") {
		t.Fatal("the fifth failure locks that address only")
	}
	now = now.Add(LockFor + time.Second)
	if l.Locked("10.0.0.5") {
		t.Fatal("unlocked after five minutes")
	}
}

type fakeActions struct {
	restarted []string
	all       int
	safe      []bool
	restored  string
}

func newHandler(t *testing.T, k *rsa.PrivateKey) (*Handler, *fakeActions) {
	t.Helper()
	fa := &fakeActions{}
	h := &Handler{
		Verifier: &Verifier{PublicKeys: func() []*rsa.PublicKey { return []*rsa.PublicKey{&k.PublicKey} },
			Now: func() time.Time { return t0 }},
		Codes:    &Codes{Now: func() time.Time { return t0 }},
		Sessions: &Sessions{Now: func() time.Time { return t0 }},
		Lockout:  &Lockout{Now: func() time.Time { return t0 }},
		Status: func() supervisor.StatusDoc {
			return supervisor.StatusDoc{V: 1, Mode: "normal", Overall: "down", Components: map[string]string{"database": "down", "api": "restarting"}}
		},
		Processes: func() []supervisor.ChildStatus {
			code := 3
			return []supervisor.ChildStatus{
				{Role: "postgres", Component: "core.postgres", State: supervisor.StateFailed, Restarts: 6, CutOff: true,
					LastExit: &supervisor.ExitInfo{At: t0.Add(-time.Minute), Code: code}},
				{Role: "pipeline", Instance: "stt-0", Component: "transcription", State: supervisor.StateStopped},
			}
		},
		Phases: func() map[string]supervisor.ComponentPhase {
			return map[string]supervisor.ComponentPhase{"transcription": {Phase: "stopped", Reason: "held"}}
		},
		Actions: Actions{
			RestartComponent: func(id string) int { fa.restarted = append(fa.restarted, id); return 1 },
			RestartAll:       func() { fa.all++ },
			SafeMode:         func(on bool) error { fa.safe = append(fa.safe, on); return nil },
			Restore:          func(name, _ string) error { fa.restored = name; return nil },
		},
		Record: func(Who, string, string) {},
		Disk:   func() (int64, int64) { return 10 << 30, 5 << 20 },
		Now:    func() time.Time { return t0 },
	}
	return h, fa
}

func do(h http.Handler, method, path, body string, cookie *http.Cookie, hdr map[string]string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// T04: signed in with a pass, the page loads, the state names each component with its
// detail, and the actions do what they say (with their confirms); a held component is not
// restarted from here; without a pass every route is 401 and says nothing of the home.
func TestConsole(t *testing.T) {
	k := newKey(t)
	h, fa := newHandler(t, k)
	for _, p := range []string{"/recovery", "/recovery/api/state", "/recovery/api/logs"} {
		rec := do(h, http.MethodGet, p, "", nil, nil)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "postgres") {
			t.Fatalf("%s without a pass: %d %s", p, rec.Code, rec.Body.String())
		}
	}
	viewer := &http.Cookie{Name: CookiePass, Value: sign(t, k, pass("v", false, AudienceRecovery, t0, t0.Add(time.Hour)))}
	if rec := do(h, http.MethodGet, "/recovery/api/state", "", viewer, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a viewer's token: %d", rec.Code)
	}
	admin := &http.Cookie{Name: CookiePass, Value: sign(t, k, pass("a1", true, AudienceRecovery, t0, t0.Add(time.Hour)))}
	start := time.Now()
	rec := do(h, http.MethodGet, "/recovery", "", admin, map[string]string{"Accept-Language": "ar"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `dir="rtl"`) ||
		!strings.Contains(rec.Body.String(), "استعادة مكتبة") || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("the page in Arabic: %d", rec.Code)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("the page took %v", d)
	}
	rec = do(h, http.MethodGet, "/recovery/api/state", "", admin, nil)
	var st State
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	var pg, tr *ComponentView
	for i := range st.Components {
		switch st.Components[i].ID {
		case "core.postgres":
			pg = &st.Components[i]
		case "transcription":
			tr = &st.Components[i]
		}
	}
	if pg == nil || pg.State != "down" || !pg.CutOff || pg.Restarts != 6 || pg.LastCode == nil || *pg.LastCode != 3 ||
		pg.Group != "database" || !pg.CanRestart {
		t.Fatalf("postgres: %+v", pg)
	}
	if tr == nil || tr.State != "off" || tr.CanRestart || tr.Reason != "held" {
		t.Fatalf("held transcription: %+v", tr)
	}
	if st.Status.Components["database"] != "down" || st.DiskFree != 10<<30 {
		t.Fatalf("state: %+v", st)
	}

	post := func(body string, hdr map[string]string) *httptest.ResponseRecorder {
		return do(h, http.MethodPost, "/recovery/api/action", body, admin, hdr)
	}
	csrf := map[string]string{"X-Maktaba-Recovery": "1"}
	if rec := post(`{"action":"restart","component":"core.postgres","confirm":1}`, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("no header: %d", rec.Code)
	}
	if rec := post(`{"action":"restart","component":"core.postgres"}`, csrf); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("no confirm: %d", rec.Code)
	}
	if rec := post(`{"action":"restart","component":"core.postgres","confirm":1}`, csrf); rec.Code != http.StatusOK ||
		len(fa.restarted) != 1 {
		t.Fatalf("restart: %d %v", rec.Code, fa.restarted)
	}
	if rec := post(`{"action":"restart","component":"transcription","confirm":1}`, csrf); rec.Code != http.StatusConflict ||
		len(fa.restarted) != 1 {
		t.Fatalf("a held component is not restarted here: %d", rec.Code)
	}
	_ = post(`{"action":"restart_all","confirm":1}`, csrf)
	_ = post(`{"action":"safe_mode","confirm":1}`, csrf)
	_ = post(`{"action":"leave_safe","confirm":1}`, csrf)
	if fa.all != 1 || len(fa.safe) != 2 || !fa.safe[0] || fa.safe[1] {
		t.Fatalf("restart all / safe mode: %+v", fa)
	}
	if rec := post(`{"action":"restore","backup":"b1","confirm":1}`, csrf); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("restore asks twice: %d", rec.Code)
	}
	if rec := post(`{"action":"restore","backup":"b1","confirm":2}`, csrf); rec.Code != http.StatusOK || fa.restored != "b1" {
		t.Fatalf("restore: %d %q", rec.Code, fa.restored)
	}
	relay := map[string]string{"X-Maktaba-Recovery": "1", "X-Maktaba-Via": "relay"}
	if rec := post(`{"action":"restart_all","confirm":1}`, relay); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("through the relay every action asks twice: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/recovery/api/diagnostics", "", admin, nil); rec.Code != http.StatusOK ||
		rec.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("diagnostics: %d", rec.Code)
	}
}

// The tray's link: the admin port gives a code, the page takes it once and signs the
// browser in with its own session; the second use is refused.
func TestTrayLink(t *testing.T) {
	h, _ := newHandler(t, newKey(t))
	link := LinkHandler(h.Codes, "http://127.0.0.1:8088", nil)
	rec := do(link, http.MethodPost, "/recovery/link", "", nil, nil)
	var out struct{ URL string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !strings.HasPrefix(out.URL, "http://127.0.0.1:8088/recovery?code=") {
		t.Fatalf("link: %s", rec.Body.String())
	}
	path := strings.TrimPrefix(out.URL, "http://127.0.0.1:8088")
	rec = do(h, http.MethodGet, path, "", nil, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("first use: %d", rec.Code)
	}
	var sess *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieSession {
			sess = c
		}
	}
	if sess == nil || sess.Path != "/recovery" || !sess.HttpOnly {
		t.Fatalf("session cookie: %+v", sess)
	}
	if rec := do(h, http.MethodGet, "/recovery/api/state", "", sess, nil); rec.Code != http.StatusOK {
		t.Fatalf("with the session: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, path, "", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("second use: %d", rec.Code)
	}
	if rec := do(link, http.MethodGet, "/recovery/link", "", nil, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET link: %d", rec.Code)
	}
}

// T06: every string has its Arabic, and the page follows the browser's language.
func TestWordsHaveArabic(t *testing.T) {
	for k, w := range Words {
		if strings.TrimSpace(w[0]) == "" || strings.TrimSpace(w[1]) == "" {
			t.Errorf("%s lacks English or Arabic", k)
		}
	}
	for accept, want := range map[string]string{"ar-SA,ar;q=0.9,en;q=0.8": "ar", "en-US,en;q=0.9,ar;q=0.8": "en", "": "en", "fr": "en"} {
		if got := Lang(accept); got != want {
			t.Errorf("Lang(%q) = %s, want %s", accept, got, want)
		}
	}
	if got := T("en", "recovery.action.rollback", "version", "0.3.44"); got != "Go back to version 0.3.44" {
		t.Errorf("vars: %q", got)
	}
}
