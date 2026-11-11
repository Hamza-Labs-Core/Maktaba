package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
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
	tok := mintToken(f.t, f.priv, auth.Claims{Aud: "streaming", Sub: f.sess, Usr: f.user, Lib: []string{f.lib}, Tls: leaseOf})
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

// A direct-play response under way stops once the lease is past: the file is not
// handed over whole on one lease.
func TestALongResponseStopsWhenTheLeaseEnds(t *testing.T) {
	f := newTimeFixture(t)
	f.limit(f.now().Add(2*time.Hour), nil, f.sess, time.Minute)
	tok := mintToken(t, f.priv, auth.Claims{Aud: "streaming-direct", Sub: f.video, Usr: f.user, Lib: []string{f.lib}, Tls: f.sess})
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
