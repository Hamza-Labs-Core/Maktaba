package liveguard

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
)

var (
	testChannel = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	otherChan   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	child       = uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	t0          = time.Date(2026, 11, 11, 19, 0, 0, 0, time.UTC)
)

// fakeVerdicts answers from fn and counts the questions.
type fakeVerdicts struct {
	mu    sync.Mutex
	calls int
	fn    func(user, channel uuid.UUID) (Verdict, error)
}

func (f *fakeVerdicts) Verdict(_ context.Context, user, channel uuid.UUID) (Verdict, error) {
	f.mu.Lock()
	f.calls++
	fn := f.fn
	f.mu.Unlock()
	return fn(user, channel)
}

func (f *fakeVerdicts) set(fn func(user, channel uuid.UUID) (Verdict, error)) {
	f.mu.Lock()
	f.fn = fn
	f.mu.Unlock()
}

func (f *fakeVerdicts) asked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func allow(uuid.UUID, uuid.UUID) (Verdict, error) {
	return Verdict{ChannelAllowed: true, Allowed: true}, nil
}

// clock is a settable time.
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

type fixture struct {
	g   *Guard
	key *rsa.PrivateKey
	v   *fakeVerdicts
	clk *clock
	h   http.Handler
	// served counts the requests that reached the stream
	served int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys := &auth.JWKSCache{}
	keys.Set(map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	f := &fixture{key: key, v: &fakeVerdicts{fn: allow}, clk: &clock{t: t0}}
	plex := netip.MustParsePrefix("192.168.1.20/32")
	f.g = &Guard{Keys: keys, Verdicts: f.v, Now: f.clk.now,
		Tuner: &Tuner{Now: f.clk.now, Load: func(context.Context) ([]netip.Prefix, error) { return []netip.Prefix{plex}, nil }}}
	f.h = f.g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.served++
		_, _ = io.WriteString(w, "stream")
	}))
	return f
}

func (f *fixture) token(t *testing.T, mut func(*auth.Claims)) string {
	t.Helper()
	c := &auth.Claims{Iss: "maktaba", Aud: AudChannel, Sub: testChannel.String(), Usr: child.String(),
		Iat: t0.Unix(), Exp: t0.Add(time.Hour).Unix()}
	if mut != nil {
		mut(c)
	}
	tok, err := auth.Sign(c, f.key, "k1")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

const chanPath = "/stream/channel/11111111-1111-4111-8111-111111111111/index.m3u8"

// get asks path from remote (a RemoteAddr), with headers hdr.
func (f *fixture) get(path, remote string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

const (
	stranger = "203.0.113.9:4000"
	loopback = "127.0.0.1:4000"
	plexAddr = "192.168.1.20:4000"
)

// T03: a channel's stream needs a signed URL for that channel; without one it is 401.
func TestChannelStreamNeedsASignedURL(t *testing.T) {
	f := newFixture(t)
	other := mustKey(t)
	cases := []struct {
		name   string
		path   string
		hdr    map[string]string
		status int
		typ    string
	}{
		{"no signature", chanPath, nil, 401, "signed-url-missing"},
		{"a segment with no signature", strings.Replace(chanPath, "index.m3u8", "seg-12.ts", 1), nil, 401, "signed-url-missing"},
		{"live.ts with no signature", strings.Replace(chanPath, "index.m3u8", "live.ts", 1), nil, 401, "signed-url-missing"},
		{"signed", chanPath + "?sig=" + f.token(t, nil), nil, 200, ""},
		{"signed, as a bearer token", chanPath, map[string]string{"Authorization": "Bearer " + f.token(t, nil)}, 200, ""},
		{"a video session's token", chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Aud = "streaming" }), nil, 401, "signed-url-wrong-aud"},
		{"a direct play token", chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Aud = "streaming-direct" }), nil, 401, "signed-url-wrong-aud"},
		{"another channel's token", chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Sub = otherChan.String() }), nil, 401, "signed-url-wrong-sub"},
		{"a token with no subject", chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Sub = "" }), nil, 401, "signed-url-wrong-sub"},
		{"expired", chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Exp = t0.Add(-time.Minute).Unix() }), nil, 403, "signed-url-expired"},
		{"signed with another key", chanPath + "?sig=" + signWith(t, other, nil), nil, 401, "signed-url-bad-signature"},
		{"not a token", chanPath + "?sig=abc", nil, 401, "signed-url-bad-signature"},
		{"no viewer in the token", chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Usr = "" }), nil, 403, "signed-url-no-viewer"},
		{"the admin placeholder with no admin claim", chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Usr = "admin" }), nil, 403, "signed-url-no-viewer"},
		{"an admin claim on a made-up viewer", chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Usr = "someone"; c.IsAdmin = true }), nil, 403, "signed-url-no-viewer"},
		{"the nil account", chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Usr = uuid.Nil.String() }), nil, 403, "signed-url-no-viewer"},
		{"a viewer header from elsewhere", chanPath, map[string]string{ViewerHeader: child.String()}, 401, "signed-url-missing"},
		{"an admin header from elsewhere", chanPath, map[string]string{AdminTokenHeader: "1"}, 401, "signed-url-missing"},
		{"not a channel id", "/stream/channel/abc/index.m3u8", nil, 404, "not-found"},
		{"the channel path written escaped", "/stream/channel/11111111-1111-4111-8111-111111111111/index%2Em3u8", nil, 404, "not-found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := f.get(c.path, stranger, c.hdr)
			if w.Code != c.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, c.status, w.Body)
			}
			if c.typ != "" {
				var p struct{ Type string }
				_ = json.Unmarshal(w.Body.Bytes(), &p)
				if !strings.HasSuffix(p.Type, "/"+c.typ) {
					t.Fatalf("type = %q, want %q", p.Type, c.typ)
				}
			} else if !strings.HasPrefix(w.Body.String(), "stream") {
				t.Fatalf("body = %q", w.Body)
			}
		})
	}
}

// The admin token's URL (usr admin with the signed is_admin claim) names no account:
// it plays with no verdict asked. The same holds for this machine's gate saying so.
func TestAdminTokenPlaysWithNoVerdict(t *testing.T) {
	f := newFixture(t)
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) { return Verdict{}, errors.New("down") })
	tok := f.token(t, func(c *auth.Claims) { c.Usr = "admin"; c.IsAdmin = true })
	if w := f.get(chanPath+"?sig="+tok, stranger, nil); w.Code != 200 {
		t.Fatalf("signed admin token URL = %d", w.Code)
	}
	if w := f.get(chanPath, loopback, map[string]string{AdminTokenHeader: "1"}); w.Code != 200 {
		t.Fatalf("the gate's admin token session = %d", w.Code)
	}
	if n := f.v.asked(); n != 0 {
		t.Fatalf("verdict asked %d times", n)
	}
	// an admin with an account is asked about like anyone
	tok = f.token(t, func(c *auth.Claims) { c.IsAdmin = true })
	if w := f.get(chanPath+"?sig="+tok, stranger, nil); w.Code != 503 {
		t.Fatalf("an admin account with no verdict = %d, want 503", w.Code)
	}
}

// The web port gate names the viewer on loopback; that viewer is asked about.
func TestGateViewerIsAskedAbout(t *testing.T) {
	f := newFixture(t)
	var got uuid.UUID
	f.v.set(func(u, _ uuid.UUID) (Verdict, error) {
		got = u
		return Verdict{Restricted: true, ChannelAllowed: false}, nil
	})
	if w := f.get(chanPath, loopback, map[string]string{ViewerHeader: child.String()}); w.Code != 404 {
		t.Fatalf("a hidden channel = %d, want 404", w.Code)
	}
	if got != child {
		t.Fatalf("asked about %s", got)
	}
	if w := f.get(chanPath, loopback, map[string]string{ViewerHeader: "admin"}); w.Code != 403 {
		t.Fatalf("a viewer that is no account = %d, want 403", w.Code)
	}
	// the viewer header wins over the admin header: a child's session cannot be opened
	if w := f.get(chanPath, loopback, map[string]string{ViewerHeader: child.String(), AdminTokenHeader: "1"}); w.Code != 404 {
		t.Fatalf("viewer and admin headers together = %d, want 404", w.Code)
	}
	if f.served != 0 {
		t.Fatalf("served %d", f.served)
	}
}

// Fail closed: no verdict (an error, a missing client) is a refusal, and is not kept.
func TestNoVerdictRefuses(t *testing.T) {
	f := newFixture(t)
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) { return Verdict{}, context.DeadlineExceeded })
	url := chanPath + "?sig=" + f.token(t, nil)
	for i := 0; i < 2; i++ {
		w := f.get(url, stranger, nil)
		if w.Code != 503 || w.Header().Get("Retry-After") == "" {
			t.Fatalf("no verdict = %d, want 503 with Retry-After", w.Code)
		}
	}
	if f.v.asked() != 2 {
		t.Fatalf("asked %d times: an error must not be reused", f.v.asked())
	}
	f.v.set(allow)
	if w := f.get(url, stranger, nil); w.Code != 200 {
		t.Fatalf("after the API is back = %d", w.Code)
	}
	f.g.Verdicts = nil
	f.clk.add(time.Minute)
	if w := f.get(url, stranger, nil); w.Code != 503 {
		t.Fatalf("no API to ask = %d, want 503", w.Code)
	}
	if w := (&Guard{}).Middleware(http.NotFoundHandler()); w == nil {
		t.Fatal("nil")
	}
	var zero Guard
	rec := httptest.NewRecorder()
	zero.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("served") })).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != 401 {
		t.Fatalf("the zero guard = %d, want 401", rec.Code)
	}
}

// T05: a programme above the limit starts: the next request is 403 with retry_at and the
// slate's text in English and Arabic; the stream is back when the programme ends.
func TestProgrammeBoundaryStopsTheStream(t *testing.T) {
	f := newFixture(t)
	start, end := t0.Add(10*time.Minute), t0.Add(70*time.Minute)
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
		now := f.clk.now()
		switch {
		case now.Before(start):
			return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true, Until: &start}, nil
		case now.Before(end):
			return Verdict{Restricted: true, ChannelAllowed: true, Allowed: false, Until: &end}, nil
		}
		return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true}, nil
	})
	seg := strings.Replace(chanPath, "index.m3u8", "seg-7.ts", 1) + "?sig=" + f.token(t, func(c *auth.Claims) { c.Exp = t0.Add(3 * time.Hour).Unix() })
	if w := f.get(seg, stranger, nil); w.Code != 200 {
		t.Fatalf("before the programme = %d", w.Code)
	}
	// a verdict is reused for VerdictTTL at most
	f.clk.add(10 * time.Second)
	f.get(seg, stranger, nil)
	if f.v.asked() != 1 {
		t.Fatalf("asked %d times within %s", f.v.asked(), VerdictTTL)
	}
	f.clk.add(6 * time.Second)
	f.get(seg, stranger, nil)
	if f.v.asked() != 2 {
		t.Fatalf("asked %d times after %s", f.v.asked(), VerdictTTL)
	}
	// 4 s before the boundary the verdict is fresh, yet it is not reused past the boundary
	f.clk.t = start.Add(-4 * time.Second)
	if w := f.get(seg, stranger, nil); w.Code != 200 || f.v.asked() != 3 {
		t.Fatalf("just before the boundary = %d (asked %d)", w.Code, f.v.asked())
	}
	f.clk.t = start
	w := f.get(seg, stranger, nil)
	if w.Code != 403 {
		t.Fatalf("at the boundary = %d, want 403", w.Code)
	}
	var body struct {
		Code, Reason string
		RetryAt      string `json:"retry_at"`
		Message      map[string]string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "restricted" || body.Reason != "programme" || body.RetryAt != end.Format(time.RFC3339) ||
		body.Message["en"] != SlateEN || body.Message["ar"] != SlateAR {
		t.Fatalf("refusal = %+v", body)
	}
	f.clk.t = end
	if w := f.get(seg, stranger, nil); w.Code != 200 {
		t.Fatalf("after the programme = %d", w.Code)
	}
}

// T05: a continuous stream (live.ts) is cut at the boundary, and when no verdict is had.
func TestLiveTSIsCut(t *testing.T) {
	for _, tc := range []struct {
		name string
		next func(uuid.UUID, uuid.UUID) (Verdict, error)
	}{
		{"a programme above the limit starts", func(uuid.UUID, uuid.UUID) (Verdict, error) {
			return Verdict{Restricted: true, ChannelAllowed: true, Allowed: false}, nil
		}},
		{"the channel is hidden", func(uuid.UUID, uuid.UUID) (Verdict, error) { return Verdict{Restricted: true}, nil }},
		{"the API stops answering", func(uuid.UUID, uuid.UUID) (Verdict, error) { return Verdict{}, errors.New("down") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tick := make(chan time.Time)
			waits := make(chan time.Duration, 8)
			f.g.After = func(d time.Duration) <-chan time.Time { waits <- d; return tick }
			boundary := t0.Add(5 * time.Second)
			f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
				return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true, Until: &boundary}, nil
			})
			ended := make(chan error, 1)
			h := f.g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for {
					if _, err := w.Write([]byte("ts")); err != nil {
						ended <- err
						return
					}
					select {
					case <-r.Context().Done():
						_, err := w.Write([]byte("more"))
						ended <- err
						return
					case <-time.After(time.Millisecond):
					}
				}
			}))
			r := httptest.NewRequest(http.MethodGet, strings.Replace(chanPath, "index.m3u8", "live.ts", 1)+"?sig="+f.token(t, nil), nil)
			r.RemoteAddr = stranger
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { h.ServeHTTP(w, r); close(done) }()
			if d := <-waits; d != 5*time.Second {
				t.Fatalf("the first check waits %s, want the 5s to the boundary", d)
			}
			// one check passes: the stream goes on
			f.clk.add(5 * time.Second)
			later := t0.Add(time.Hour)
			f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
				return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true, Until: &later}, nil
			})
			tick <- f.clk.now()
			if d := <-waits; d != VerdictTTL {
				t.Fatalf("the next check waits %s, want %s", d, VerdictTTL)
			}
			select {
			case <-done:
				t.Fatal("the stream ended while allowed")
			default:
			}
			f.clk.add(VerdictTTL)
			f.v.set(tc.next)
			tick <- f.clk.now()
			select {
			case err := <-ended:
				if err == nil {
					t.Fatal("a write after the cut succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the stream was not cut")
			}
			<-done
		})
	}
}

// A playlist asked for with a signed URL carries the signature on every address in it.
func TestSignedPlaylistSignsItsAddresses(t *testing.T) {
	f := newFixture(t)
	const playlist = "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.0,\ns/blue/seg-1.ts\r\n#EXTINF:6.0,\nseg-2.ts?x=1\n\nindex.m3u8\n"
	f.h = f.g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Content-Length", "5")
		_, _ = io.WriteString(w, playlist)
	}))
	tok := f.token(t, nil)
	w := f.get(chanPath+"?sig="+tok, stranger, nil)
	want := "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4?sig=" + tok + "\"\n#EXTINF:6.0,\ns/blue/seg-1.ts?sig=" + tok +
		"\r\n#EXTINF:6.0,\nseg-2.ts?x=1&sig=" + tok + "\n\nindex.m3u8?sig=" + tok + "\n"
	if w.Body.String() != want {
		t.Fatalf("playlist =\n%q\nwant\n%q", w.Body.String(), want)
	}
	if cl := w.Header().Get("Content-Length"); cl != itoa(len(want)) {
		t.Fatalf("Content-Length = %s, want %d", cl, len(want))
	}
	// the gate's viewer (no signature in the address) gets the playlist as it is
	w = f.get(chanPath, loopback, map[string]string{ViewerHeader: child.String()})
	if w.Body.String() != playlist {
		t.Fatalf("unsigned playlist changed: %q", w.Body.String())
	}
	// an error is passed on with its status
	f.h = f.g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "gone", http.StatusGone) }))
	if w := f.get(chanPath+"?sig="+tok, stranger, nil); w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "gone") || strings.Contains(w.Body.String(), "sig=") {
		t.Fatalf("an error = %d %q", w.Code, w.Body)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// T04: the tuner and the guide files answer the Plex server and this machine, and no
// one else; a tuner's address may also play a channel with no viewer named.
func TestTunerAndGuideFilesByAddress(t *testing.T) {
	f := newFixture(t)
	paths := append([]string{"/auto/v12", chanPath, strings.Replace(chanPath, "index.m3u8", "live.ts", 1)}, TunerPaths...)
	for _, p := range paths {
		for _, c := range []struct {
			from   string
			status int
		}{{plexAddr, 200}, {loopback, 200}, {"[::1]:9", 200}, {stranger, 403}, {"192.168.1.21:1", 403}, {"", 403}} {
			w := f.get(p, c.from, nil)
			want := c.status
			if want == 403 && strings.HasPrefix(p, ChannelPrefix) {
				want = 401 // a channel's stream asks for the signed URL
			}
			if w.Code != want {
				t.Errorf("%s from %q = %d, want %d", p, c.from, w.Code, want)
			}
		}
	}
	if f.v.asked() != 0 {
		t.Fatalf("a tuner is no viewer: verdict asked %d times", f.v.asked())
	}
	// POST /lineup.post is guarded like the rest
	r := httptest.NewRequest(http.MethodPost, "/lineup.post?scan=start", nil)
	r.RemoteAddr = stranger
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("POST /lineup.post from a stranger = %d", w.Code)
	}
	// what is not live TV's passes
	for _, p := range []string{"/channel/11111111-1111-4111-8111-111111111111/logo", "/healthz", "/stream/staging/channel/x/index.m3u8"} {
		if w := f.get(p, stranger, nil); w.Code != 200 {
			t.Errorf("%s = %d, want it passed on", p, w.Code)
		}
	}
}

// An address an admin removes stops within TunerTTL; a list that cannot be read allows
// this machine only, never the list as it was.
func TestTunerListChangesAndFailsClosed(t *testing.T) {
	clk := &clock{t: t0}
	list := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}
	var loadErr error
	tn := &Tuner{Now: clk.now, Load: func(context.Context) ([]netip.Prefix, error) { return list, loadErr }}
	ctx := context.Background()
	in, lo := netip.MustParseAddr("10.0.0.7"), netip.MustParseAddr("127.0.0.1")
	if !tn.Allowed(ctx, in) || tn.Allowed(ctx, netip.MustParseAddr("10.0.1.7")) {
		t.Fatal("the list is not applied")
	}
	list = nil
	clk.add(TunerTTL + time.Second)
	if tn.Allowed(ctx, in) {
		t.Fatal("a removed address is still allowed after TunerTTL")
	}
	list = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}
	clk.add(TunerTTL + time.Second)
	if !tn.Allowed(ctx, in) {
		t.Fatal("an added address is not allowed")
	}
	loadErr = errors.New("database down")
	clk.add(TunerTTL + time.Second)
	if tn.Allowed(ctx, in) {
		t.Fatal("allowed from a list that could not be read")
	}
	if !tn.Allowed(ctx, lo) || !(*Tuner)(nil).Allowed(ctx, lo) || (*Tuner)(nil).Allowed(ctx, in) {
		t.Fatal("this machine only, with no list")
	}
	if tn.Allowed(ctx, netip.Addr{}) {
		t.Fatal("no address is allowed")
	}
}

func TestParseAllowedAndPlexHost(t *testing.T) {
	got := ParseAllowed([]byte(`{"addresses":["192.168.1.20"," 10.0.0.0/8 ","0.0.0.0/0","::/0","nonsense",""]}`))
	if len(got) != 2 || got[0].String() != "192.168.1.20/32" || got[1].String() != "10.0.0.0/8" {
		t.Fatalf("ParseAllowed = %v", got)
	}
	if ParseAllowed([]byte(`not json`)) != nil {
		t.Fatal("unreadable list")
	}
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "plex.lan" {
			return []netip.Addr{netip.MustParseAddr("192.168.1.30"), netip.MustParseAddr("::ffff:192.168.1.31")}, nil
		}
		return nil, errors.New("no such host")
	}
	ctx := context.Background()
	if p := hostPrefixes(ctx, "http://192.168.1.20:32400", lookup); len(p) != 1 || p[0].String() != "192.168.1.20/32" {
		t.Fatalf("address URL = %v", p)
	}
	if p := hostPrefixes(ctx, "https://plex.lan:32400/", lookup); len(p) != 2 || p[1].String() != "192.168.1.31/32" {
		t.Fatalf("named URL = %v", p)
	}
	for _, u := range []string{"", "::", "http://nowhere.lan"} {
		if p := hostPrefixes(ctx, u, lookup); len(p) != 0 {
			t.Fatalf("%q = %v", u, p)
		}
	}
}

// Wrap guards the routes a mount registers, and only those.
func TestWrapGuardsAMount(t *testing.T) {
	f := newFixture(t)
	r := chi.NewRouter()
	f.g.Wrap(func(m chi.Router) {
		m.Get("/stream/channel/{id}/index.m3u8", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "stream") })
		m.Get("/xmltv.xml", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "guide") })
	})(r)
	r.Get("/other", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "other") })
	f.h = r
	if w := f.get(chanPath, stranger, nil); w.Code != 401 {
		t.Fatalf("channel = %d", w.Code)
	}
	if w := f.get("/xmltv.xml", stranger, nil); w.Code != 403 {
		t.Fatalf("guide = %d", w.Code)
	}
	if w := f.get("/xmltv.xml", plexAddr, nil); w.Code != 200 {
		t.Fatalf("guide from Plex = %d", w.Code)
	}
	if w := f.get("/other", stranger, nil); w.Code != 200 {
		t.Fatalf("other = %d", w.Code)
	}
}

// The API client: the local token, the question, and nothing but a whole 200 verdict.
func TestAPIVerdicts(t *testing.T) {
	until := t0.Add(time.Hour)
	var answerMu sync.Mutex
	var answer func(w http.ResponseWriter)
	setAnswer := func(fn func(w http.ResponseWriter)) { answerMu.Lock(); answer = fn; answerMu.Unlock() }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tv/parental/verdict" || r.Header.Get(LocalTokenHeader) != "local-token" ||
			r.URL.Query().Get("channel") != testChannel.String() || r.URL.Query().Get("user") != child.String() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		answerMu.Lock()
		fn := answer
		answerMu.Unlock()
		fn(w)
	}))
	defer srv.Close()
	tokFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokFile, []byte("local-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &APIVerdicts{BaseURL: srv.URL, TokenFile: tokFile, Client: &http.Client{Timeout: 200 * time.Millisecond}}
	ctx := context.Background()

	setAnswer(func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{"restricted": true, "channel_allowed": true, "allowed": false, "until": until})
	})
	v, err := a.Verdict(ctx, child, testChannel)
	if err != nil || !v.Restricted || !v.ChannelAllowed || v.Allowed || v.Until == nil || !v.Until.Equal(until) {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
	for name, ans := range map[string]func(w http.ResponseWriter){
		"a 403":               func(w http.ResponseWriter) { w.WriteHeader(403) },
		"a 500":               func(w http.ResponseWriter) { w.WriteHeader(500) },
		"not JSON":            func(w http.ResponseWriter) { _, _ = io.WriteString(w, "<html>") },
		"an empty object":     func(w http.ResponseWriter) { _, _ = io.WriteString(w, "{}") },
		"a field missing":     func(w http.ResponseWriter) { _, _ = io.WriteString(w, `{"restricted":false,"allowed":true}`) },
		"slower than allowed": func(w http.ResponseWriter) { time.Sleep(400 * time.Millisecond) },
	} {
		setAnswer(ans)
		if v, err := a.Verdict(ctx, child, testChannel); err == nil {
			t.Errorf("%s: verdict %+v with no error", name, v)
		}
	}
	// the wrong token, no token file, no API
	if err := os.WriteFile(tokFile, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	setAnswer(func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, `{"restricted":false,"channel_allowed":true,"allowed":true}`)
	})
	if _, err := a.Verdict(ctx, child, testChannel); err == nil {
		t.Error("the wrong token got a verdict")
	}
	for _, bad := range []*APIVerdicts{nil, {}, {BaseURL: srv.URL}, {BaseURL: srv.URL, TokenFile: filepath.Join(t.TempDir(), "none")}} {
		if _, err := bad.Verdict(ctx, child, testChannel); !errors.Is(err, ErrNoAPI) {
			t.Errorf("%+v: err = %v, want ErrNoAPI", bad, err)
		}
	}
}

func mustKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func signWith(t *testing.T, key *rsa.PrivateKey, mut func(*auth.Claims)) string {
	t.Helper()
	c := &auth.Claims{Aud: AudChannel, Sub: testChannel.String(), Usr: child.String(), Exp: t0.Add(time.Hour).Unix()}
	if mut != nil {
		mut(c)
	}
	tok, err := auth.Sign(c, key, "k1")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
