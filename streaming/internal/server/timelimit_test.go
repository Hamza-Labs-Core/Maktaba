package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/timelimit"
)

// timeFixture is a server with one transcode session of a restricted account, its
// segment on disk, and a time-limit registry on a clock the test moves.
type timeFixture struct {
	t                *testing.T
	priv             *rsa.PrivateKey
	deps             Deps
	reg              *timelimit.Registry
	mu               sync.Mutex
	clock            time.Time
	step             time.Duration // the clock moves on by this with every reading
	user, lib, video string
	sess             string
	// pb and row are the video's probe: a test may change the row and Set it again
	// before the first request
	pb  *probe.FakeBackend
	row *probe.Row
}

func newTimeFixture(t *testing.T) *timeFixture {
	t.Helper()
	f := &timeFixture{t: t, clock: time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC), user: uuid.NewString()}
	f.priv, _ = rsa.GenerateKey(rand.Reader, 2048)
	jwks, _ := auth.NewJWKSCache(context.Background(), "", time.Minute)
	jwks.Set(map[string]*rsa.PublicKey{"k1": &f.priv.PublicKey})
	libID := uuid.New()
	row := &probe.Row{VideoID: uuid.New(), LibraryID: libID, Path: "/v/x.mp4", Container: "mp4",
		VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 4000, Probed: true}
	pb := probe.NewFakeBackend()
	pb.Set(row)
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	store := session.NewMemoryStore(time.Second)
	sess := &session.Row{ID: uuid.New(), VideoID: row.VideoID, UserID: uuid.MustParse(f.user), Mode: session.ModeTranscode,
		Format: session.FormatDASH}
	if err := store.Insert(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	dir := layout.HLSDir(sess.ID.String())
	_ = os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "chunk-0-1.m4s"), []byte("CHUNK"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.lib, f.video, f.sess = libID.String(), row.VideoID.String(), sess.ID.String()
	f.pb, f.row = pb, row
	f.reg = timelimit.New()
	f.reg.Now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.clock = f.clock.Add(f.step)
		return f.clock
	}
	f.deps = Deps{Cfg: config.Load(), JWKS: jwks, Probe: probe.NewCache(pb, 16), Profiles: capability.NewRegistry(),
		Sessions: store, Layout: layout, Files: &fakeOpener{file: make([]byte, 4<<20)}, Now: time.Now, TimeLimits: f.reg}
	return f
}

func (f *timeFixture) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock
}

func (f *timeFixture) pass(d time.Duration) {
	f.mu.Lock()
	f.clock = f.clock.Add(d)
	f.mu.Unlock()
}

// limit is the API's push: the account's end, and a lease for a session.
func (f *timeFixture) limit(end time.Time, next *time.Time, session string, lease time.Duration) {
	lim := &timelimit.Limit{EndAt: &end, Reason: timelimit.ReasonLimitReached, NextAllowedAt: next}
	if session != "" {
		until := f.now().Add(lease)
		lim.SessionID, lim.LeaseUntil = session, &until
	}
	f.reg.Set(f.user, f.lib, lim)
}

// segment asks for the session's segment with a URL that names leaseOf as the
// session whose lease it needs ("": the URL of an account that was not restricted).
func (f *timeFixture) segment(deps Deps, leaseOf string) (int, map[string]any) {
	tok := mintToken(f.t, f.priv, auth.Claims{Aud: "streaming", Sub: f.sess, Usr: f.user, Lib: []string{f.lib}, TLS: leaseOf})
	rec := httptest.NewRecorder()
	New(deps).ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+f.sess+"/chunk-0-1.m4s?sig="+tok, nil))
	body := map[string]any{}
	if rec.Code != 200 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec.Code, body
}

// T04: a session with an end time is served until it; segments asked for after it
// are refused with the parental-time problem, at once (the story allows 60 s).
func TestSegmentsAfterTheEndTimeAreRefused(t *testing.T) {
	f := newTimeFixture(t)
	end := f.now().Add(30 * time.Second)
	next := f.now().Add(11 * time.Hour)
	f.limit(end, &next, f.sess, time.Minute)
	if code, _ := f.segment(f.deps, f.sess); code != 200 {
		t.Fatalf("before the end: %d", code)
	}
	f.pass(29 * time.Second)
	if code, _ := f.segment(f.deps, f.sess); code != 200 {
		t.Fatalf("one second before the end: %d", code)
	}
	f.pass(time.Second)
	code, body := f.segment(f.deps, f.sess)
	if code != 403 || body["reason"] != timelimit.ReasonLimitReached || body["next_allowed_at"] != next.Format(time.RFC3339) ||
		body["type"] != "https://maktaba.invalid/problems/parental-time" {
		t.Fatalf("at the end: %d %v", code, body)
	}
	if code, body := f.segment(f.deps, ""); code != 403 || body["reason"] != timelimit.ReasonLimitReached {
		t.Fatalf("a URL with no lease claim after the end: %d %v", code, body)
	}
	// the guardian gives more time: the API pushes a later end and the stream goes on
	f.limit(f.now().Add(10*time.Minute), nil, f.sess, time.Minute)
	if code, _ := f.segment(f.deps, f.sess); code != 200 {
		t.Fatalf("after more time was given: %d", code)
	}
}

// Security findings 2 and 3 on the streaming side: a restricted account's session
// is served only while its own lease runs. No lease (never given, lost with a
// restart, another session's, or run out) is a refusal, never free time.
func TestASessionIsServedOnlyUnderItsOwnLease(t *testing.T) {
	f := newTimeFixture(t)
	end := f.now().Add(2 * time.Hour)
	other := uuid.NewString()

	// nothing pushed yet (a restart): the URL names a lease, there is none
	if code, body := f.segment(f.deps, f.sess); code != 403 || body["reason"] != timelimit.ReasonNoHeartbeat {
		t.Fatalf("no lease held: %d %v", code, body)
	}
	// and a server given no registry holds none either
	bare := f.deps
	bare.TimeLimits = nil
	if code, _ := f.segment(bare, f.sess); code != 403 {
		t.Fatalf("no registry: %d", code)
	}

	// another session of the account has a lease: it does not carry this one
	f.limit(end, nil, other, time.Minute)
	if code, _ := f.segment(f.deps, f.sess); code != 403 {
		t.Fatalf("served under another session's lease: %d", code)
	}
	f.limit(end, nil, f.sess, time.Minute)
	if code, _ := f.segment(f.deps, f.sess); code != 200 {
		t.Fatalf("under its own lease: %d", code)
	}
	if at, known := f.reg.Served(f.user, f.sess); !known || !at.Equal(f.now()) {
		t.Fatalf("served time %v (known %v), want now", at, known)
	}
	// a push that names no session (the rules changed) renews nothing
	f.pass(59 * time.Second)
	f.limit(end, nil, "", 0)
	f.pass(time.Second)
	if code, body := f.segment(f.deps, f.sess); code != 403 || body["reason"] != timelimit.ReasonNoHeartbeat {
		t.Fatalf("60 s after the last counted call: %d %v", code, body)
	}
	if at, _ := f.reg.Served(f.user, f.sess); !at.Equal(f.now().Add(-time.Minute)) {
		t.Fatalf("a refused request moved the served time to %v", at)
	}
	// the API ends the lease (the session closed, or its mark was let go)
	f.limit(end, nil, f.sess, time.Minute)
	f.limit(end, nil, f.sess, 0)
	if code, _ := f.segment(f.deps, f.sess); code != 403 {
		t.Fatalf("after the lease was ended: %d", code)
	}

	// the leases are full: the one that goes is refused, not served free
	f.reg.LeasesKept = 1
	f.limit(end, nil, f.sess, time.Minute)
	if code, _ := f.segment(f.deps, f.sess); code != 200 {
		t.Fatalf("under a renewed lease: %d", code)
	}
	f.limit(end, nil, uuid.NewString(), time.Minute)
	if code, _ := f.segment(f.deps, f.sess); code != 403 {
		t.Fatalf("a lease dropped for room still served: %d", code)
	}
}

// Security finding 4: the guard does not fail open on a URL that names no session.
// For an account whose time is kept (anything is held for it in the library) such a
// URL is refused, before the end as well as after it; with its session named and a
// live lease it is served until the end; an account nothing is held for is served
// as before, and so is the kept account's URL once the API clears its limit.
func TestAKeptAccountsURLWithoutASessionIsRefused(t *testing.T) {
	f := newTimeFixture(t)
	if code, _ := f.segment(f.deps, ""); code != 200 {
		t.Fatalf("an account with no limit, URL with no session: %d", code)
	}
	end := f.now().Add(30 * time.Minute)
	f.limit(end, nil, f.sess, time.Minute)
	if code, body := f.segment(f.deps, ""); code != 403 || body["reason"] != timelimit.ReasonNoHeartbeat {
		t.Fatalf("a kept account's URL with no session, before the end: %d %v", code, body)
	}
	// a restricted account with no rule has an entry with no end: kept all the same
	other := uuid.NewString()
	f.reg.Set(other, f.lib, &timelimit.Limit{})
	tok := mintToken(t, f.priv, auth.Claims{Aud: "streaming-direct", Sub: f.video, Usr: other, Lib: []string{f.lib}})
	rec := httptest.NewRecorder()
	New(f.deps).ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+f.video+"?sig="+tok, nil))
	if rec.Code != 403 {
		t.Fatalf("direct play for a kept account with no session named: %d", rec.Code)
	}
	// with its session named it is served until the end time, and not after
	for i := 0; i < 29; i++ {
		f.pass(time.Minute)
		f.limit(end, nil, f.sess, time.Minute) // the counted call of that minute
		if code, _ := f.segment(f.deps, f.sess); code != 200 {
			t.Fatalf("minute %d under a live lease: %d", i+1, code)
		}
	}
	f.pass(time.Minute)
	f.limit(end, nil, f.sess, time.Minute)
	if code, body := f.segment(f.deps, f.sess); code != 403 || body["reason"] != timelimit.ReasonLimitReached {
		t.Fatalf("at the end time: %d %v", code, body)
	}
	// another account in the same library is not touched by any of it
	free := mintToken(t, f.priv, auth.Claims{Aud: "streaming-direct", Sub: f.video, Usr: uuid.NewString(), Lib: []string{f.lib}})
	rec = httptest.NewRecorder()
	New(f.deps).ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+f.video+"?sig="+free, nil))
	if rec.Code != 200 && rec.Code != 206 {
		t.Fatalf("an account with no limit: %d", rec.Code)
	}
	// the restriction is lifted: the API clears the entry
	f.reg.Set(f.user, f.lib, nil)
	if code, _ := f.segment(f.deps, ""); code != 200 {
		t.Fatalf("after the limit was cleared: %d", code)
	}
}

// Security finding 5: missing state is not "unlimited". A streaming service that
// just started holds nothing, so it cannot tell a kept account by its registry; the
// API's own word on the URL decides. With an empty registry a kept account's URL
// that names no session is refused, and so is a URL that says nothing either way
// (signed by an older or another signing path); only a URL the API signed as not
// kept is served without a lease.
func TestAnEmptyRegistryDoesNotMeanUnlimited(t *testing.T) {
	f := newTimeFixture(t)
	get := func(deps Deps, c auth.Claims) (int, string) {
		c.Usr, c.Lib = f.user, []string{f.lib}
		path := "/stream/" + f.sess + "/chunk-0-1.m4s"
		if c.Aud == "streaming-direct" {
			path = "/stream/direct/" + f.video
		}
		rec := httptest.NewRecorder()
		New(deps).ServeHTTP(rec, httptest.NewRequest("GET", path+"?sig="+mintToken(t, f.priv, c), nil))
		body := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		reason, _ := body["reason"].(string)
		return rec.Code, reason
	}
	bare := f.deps
	bare.TimeLimits = nil
	for name, deps := range map[string]Deps{"an empty registry": f.deps, "no registry": bare} {
		for _, aud := range []struct{ aud, sub string }{{"streaming", f.sess}, {"streaming-direct", f.video}} {
			if code, reason := get(deps, auth.Claims{Aud: aud.aud, Sub: aud.sub, Tk: "1"}); code != 403 || reason != timelimit.ReasonNoHeartbeat {
				t.Errorf("%s, %s: a kept account's URL with no session: %d %q", name, aud.aud, code, reason)
			}
			if code, reason := get(deps, auth.Claims{Aud: aud.aud, Sub: aud.sub, Tk: "none"}); code != 403 || reason != timelimit.ReasonNoHeartbeat {
				t.Errorf("%s, %s: a URL that does not say whether the time is kept: %d %q", name, aud.aud, code, reason)
			}
			if code, reason := get(deps, auth.Claims{Aud: aud.aud, Sub: aud.sub, Tk: "1", TLS: f.sess}); code != 403 || reason != timelimit.ReasonNoHeartbeat {
				t.Errorf("%s, %s: a kept account's URL before any lease: %d %q", name, aud.aud, code, reason)
			}
			if code, _ := get(deps, auth.Claims{Aud: aud.aud, Sub: aud.sub, Tk: "0"}); code != 200 && code != 206 {
				t.Errorf("%s, %s: an account whose time is not kept: %d", name, aud.aud, code)
			}
		}
	}
	// the API gives the lease: the kept account's URL is served
	f.limit(f.now().Add(time.Hour), nil, f.sess, time.Minute)
	if code, _ := get(f.deps, auth.Claims{Aud: "streaming", Sub: f.sess, Tk: "1", TLS: f.sess}); code != 200 {
		t.Errorf("under a lease: %d", code)
	}
}

// A direct-play response under way stops once the lease is past: the file is not
// handed over whole on one lease.
func TestALongResponseStopsWhenTheLeaseEnds(t *testing.T) {
	f := newTimeFixture(t)
	f.limit(f.now().Add(2*time.Hour), nil, f.sess, time.Minute)
	tok := mintToken(t, f.priv, auth.Claims{Aud: "streaming-direct", Sub: f.video, Usr: f.user, Lib: []string{f.lib}, TLS: f.sess})
	f.step = 5 * time.Second // every look at the clock while writing is 5 s later
	rec := httptest.NewRecorder()
	New(f.deps).ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+f.video+"?sig="+tok, nil))
	f.step = 0
	if n := rec.Body.Len(); n == 0 || n >= 4<<20 {
		t.Fatalf("%d of %d bytes were written under a 60 s lease", n, 4<<20)
	}
	if at, _ := f.reg.Served(f.user, f.sess); at.IsZero() || at.After(f.now()) {
		t.Fatalf("served time %v", at)
	}
}

// Review finding 4: a kept account cannot pull a whole file inside one lease. Direct
// play is paced to the file's byte rate with a read-ahead, on the registry's clock:
// under a 60 s lease the client gets the read-ahead and the 60 s that played, and
// the response ends with the lease. An account whose time is not kept is never paced.
func TestDirectPlayIsPacedForAKeptAccount(t *testing.T) {
	f := newTimeFixture(t)
	f.row.BitrateKbps = 100 // 12 500 bytes a second: the 4 MiB file is 335 s of media
	f.pb.Set(f.row)
	const rate = 12500
	var slept time.Duration
	f.reg.Sleep = func(_ context.Context, d time.Duration) error {
		f.pass(d)
		f.mu.Lock()
		slept += d
		f.mu.Unlock()
		return nil
	}
	f.limit(f.now().Add(2*time.Hour), nil, f.sess, time.Minute)
	tok := mintToken(t, f.priv, auth.Claims{Aud: "streaming-direct", Sub: f.video, Usr: f.user, Lib: []string{f.lib}, Tk: "1", TLS: f.sess})
	rec := httptest.NewRecorder()
	New(f.deps).ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+f.video+"?sig="+tok, nil))
	got := rec.Body.Len()
	least := int(timelimit.ReadAhead/time.Second) * rate
	most := int((timelimit.ReadAhead+time.Minute+3*time.Second)/time.Second) * rate
	if got < least || got > most {
		t.Fatalf("%d bytes under a 60 s lease, want between %d (the read-ahead) and %d (the read-ahead and the 60 s played)", got, least, most)
	}
	if slept < 55*time.Second || slept > 65*time.Second {
		t.Fatalf("the response waited %s in all, want the lease's 60 s", slept)
	}
	// the next request of the same video, on a new lease, starts with no read-ahead:
	// what was served ahead has to play out first
	f.limit(f.now().Add(2*time.Hour), nil, f.sess, time.Minute)
	before := slept
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/stream/direct/"+f.video+"?sig="+tok, nil)
	req.Header.Set("Range", "bytes=0-124999") // ten seconds of media
	New(f.deps).ServeHTTP(rec, req)
	if rec.Code != 206 || rec.Body.Len() != 125000 {
		t.Fatalf("a range after the read-ahead was spent: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if d := slept - before; d < 8*time.Second || d > 12*time.Second {
		t.Fatalf("ten seconds of media took %s with the read-ahead spent, want about 10 s", d)
	}

	// an account whose time is not kept gets the file as fast as it asks
	before = slept
	free := mintToken(t, f.priv, auth.Claims{Aud: "streaming-direct", Sub: f.video, Usr: uuid.NewString(), Lib: []string{f.lib}, Tk: "0"})
	rec = httptest.NewRecorder()
	New(f.deps).ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+f.video+"?sig="+free, nil))
	if rec.Body.Len() != 4<<20 || slept != before {
		t.Fatalf("an account with no limit: %d bytes, %s waited", rec.Body.Len(), slept-before)
	}
}

// The same for a transcode's segments, by their length: past the read-ahead a
// segment waits until what was served before has played, the same segment again (or
// its audio, or another rendition of it) costs nothing, and a client asking far
// faster than it plays is refused.
func TestSegmentsArePacedForAKeptAccount(t *testing.T) {
	f := newTimeFixture(t)
	dir := f.deps.Layout.HLSDir(f.sess)
	for i := 0; i < 40; i++ {
		for _, stream := range []string{"0", "1"} {
			name := "chunk-stream" + stream + "-" + fmt.Sprintf("%05d", i) + ".m4s"
			if err := os.WriteFile(filepath.Join(dir, name), []byte("CHUNK"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	var slept time.Duration
	advance := true
	f.reg.Sleep = func(_ context.Context, d time.Duration) error {
		if advance {
			f.pass(d)
		}
		slept += d
		return nil
	}
	get := func(usr, tls, name string) int {
		c := auth.Claims{Aud: "streaming", Sub: f.sess, Usr: usr, Lib: []string{f.lib}, Tk: "0"}
		if tls != "" {
			c.Tk, c.TLS = "1", tls
		}
		rec := httptest.NewRecorder()
		New(f.deps).ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+f.sess+"/"+name+"?sig="+mintToken(t, f.priv, c), nil))
		return rec.Code
	}
	seg := func(stream string, i int) string { return fmt.Sprintf("chunk-stream%s-%05d.m4s", stream, i) }
	lease := func() { f.limit(f.now().Add(4*time.Hour), nil, f.sess, time.Minute) }

	// while the account's time is not kept (nothing is held for it, and its URL
	// says so) it is never paced, however much it asks for
	for i := 0; i < 40; i++ {
		if code := get(f.user, "", seg("0", i)); code != 200 {
			t.Fatalf("an account with no limit, segment %d: %d", i, code)
		}
	}
	if slept != 0 {
		t.Fatalf("an account with no limit waited %s", slept)
	}

	// the read-ahead: 120 s of media, twenty 6 s segments, video and audio, at once
	lease()
	for i := 0; i < 20; i++ {
		for _, stream := range []string{"0", "1"} {
			if code := get(f.user, f.sess, seg(stream, i)); code != 200 {
				t.Fatalf("segment %d of stream %s inside the read-ahead: %d", i, stream, code)
			}
		}
	}
	if slept != 0 {
		t.Fatalf("the read-ahead waited %s", slept)
	}
	// from there on a segment comes when the one before has played
	for i := 20; i < 30; i++ {
		lease()
		if code := get(f.user, f.sess, seg("0", i)); code != 200 {
			t.Fatalf("segment %d past the read-ahead: %d", i, code)
		}
	}
	if slept < 50*time.Second || slept > 60*time.Second {
		t.Fatalf("ten segments past the read-ahead waited %s, want about a minute", slept)
	}
	// a segment already served costs nothing more
	before := slept
	lease()
	for _, name := range []string{seg("0", 5), seg("1", 29), "init-stream0.m4s"} {
		get(f.user, f.sess, name)
	}
	if slept != before {
		t.Fatalf("segments already paid for waited %s", slept-before)
	}
	// a client that asks for far more than plays (its waits do not pass: it asks in
	// parallel) is refused, not queued without end
	advance = false
	lease()
	refused := false
	for i := 30; i < 40; i++ {
		if code := get(f.user, f.sess, seg("0", i)); code == 429 {
			refused = true
			break
		} else if code != 200 {
			t.Fatalf("segment %d: %d", i, code)
		}
	}
	if !refused {
		t.Fatal("ten segments asked for at once past the read-ahead were all served")
	}
}
