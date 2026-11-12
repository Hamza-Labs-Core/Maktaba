package parental_test

// 57.14 against a real migrated Postgres (slot 0223): the count (T02), a grant
// (T05), the home's zone, and what streaming is told.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

type pushed struct {
	userID, libraryID, reason string
	endAt, next, lease        *time.Time
	cleared                   bool
	session                   string // the stream session the lease is for
}

// fakePusher is streaming as the keeper sees it: it keeps what it is told, and says
// what it served each session.
type fakePusher struct {
	mu      sync.Mutex
	got     []pushed
	fail    error
	served  map[string]time.Time // by stream session id
	unknown bool                 // streaming holds no lease for the session
}

// lease is the last lease streaming was given for a session, nil when none.
func (f *fakePusher) lease(session string) *time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.got) - 1; i >= 0; i-- {
		if f.got[i].session == session && f.got[i].lease != nil {
			return f.got[i].lease
		}
	}
	return nil
}

func (f *fakePusher) SetTimeLimit(_ context.Context, userID, libraryID string, lim *parental.TimeLimit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	if lim == nil {
		f.got = append(f.got, pushed{userID: userID, libraryID: libraryID, cleared: true})
		return nil
	}
	f.got = append(f.got, pushed{userID, libraryID, lim.Reason, lim.EndAt, lim.NextAllowedAt, lim.LeaseUntil, false, lim.SessionID})
	return nil
}

func (f *fakePusher) TimeServed(_ context.Context, _, _, sessionID, _ string) (time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return time.Time{}, false, f.fail
	}
	return f.served[sessionID], !f.unknown, nil
}

func (f *fakePusher) last(t *testing.T) pushed {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		t.Fatal("streaming was told nothing")
	}
	return f.got[len(f.got)-1]
}

type timeEnv struct {
	*env
	clock  time.Time
	loc    *time.Location
	push   *fakePusher
	keeper *parental.TimeKeeper
	child  *principal.Principal
	open   map[string]bool
}

// setupTime: the child is restricted, the home is in Riyadh, and the clock stands
// at 17:00 on Tuesday 10 November 2026.
func setupTime(t *testing.T) *timeEnv {
	t.Helper()
	e := &timeEnv{env: setup(t), loc: homeZone(t), push: &fakePusher{served: map[string]time.Time{}}, open: map[string]bool{}}
	e.clock = at(t, e.loc, "2026-11-10 17:00:00")
	e.store.Now = func() time.Time { return e.clock }
	e.restrict(uChild)
	e.exec(`INSERT INTO app_settings (key, value) VALUES ('home.timezone', '"Asia/Riyadh"')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	e.keeper = parental.NewTimeKeeper(parental.NewCache(e.store), e.push)
	e.child = &principal.Principal{UserID: uChild, Libraries: []string{libOpen}, Source: principal.SourceJWT}
	return e
}

func (e *timeEnv) video(lib string) string {
	e.t.Helper()
	id := uuid.NewString()
	e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
		VALUES ($1::uuid, $2::uuid, '/media/v/' || $1::text || '.mp4', $1::text || '.mp4',
		        encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), 1000, now(), 'ready', 't', 7200, now(), now())`, id, lib)
	return id
}

func (e *timeEnv) rules(rules ...parental.TimeRule) {
	e.t.Helper()
	if _, err := e.store.SetTimeRules(context.Background(), uGuardian, uChild, rules); err != nil {
		e.t.Fatalf("SetTimeRules: %v", err)
	}
}

func (e *timeEnv) used(lib string) int {
	e.t.Helper()
	return e.count(`SELECT COALESCE(sum(seconds), 0)::int FROM parental_time_usage WHERE user_id = $1 AND library_id = $2`, uChild, lib)
}

// session is a stream session row of userID, opened now.
func (e *timeEnv) session(userID, video string) string {
	e.t.Helper()
	id := uuid.NewString()
	e.exec(`INSERT INTO streaming_sessions (id, user_id, video_id, opened_at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4)`,
		id, userID, video, e.clock)
	return id
}

// post is one call for a session of the child after the clock moved on by d: the
// first one opens it (the API marks it), each later one is a progress post.
func (e *timeEnv) post(d time.Duration, session, video string) {
	e.t.Helper()
	e.clock = e.clock.Add(d)
	ctx := context.Background()
	if !e.open[session] {
		e.open[session] = true
		var lib string
		if err := e.store.DB.QueryRow(`SELECT library_id::text FROM videos WHERE id = $1`, video).Scan(&lib); err != nil {
			e.t.Fatal(err)
		}
		if err := e.keeper.Opened(ctx, uChild, lib, video, session); err != nil {
			e.t.Fatalf("Opened: %v", err)
		}
		return
	}
	if err := e.keeper.Progress(ctx, uChild, session); err != nil {
		e.t.Fatalf("Progress: %v", err)
	}
}

// T02: calls from two sessions both count; a paused one (it stops calling, and
// streaming serves it nothing) adds none; one call never adds more than the
// heartbeat interval.
func TestUsageCountsEverySessionAndNoPause(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	v1, v2 := e.video(libOpen), e.video(libClosed)

	e.post(0, "s-ipad", v1) // opening a session only marks it
	e.post(0, "s-tablet", v2)
	if e.used(libOpen)+e.used(libClosed) != 0 {
		t.Fatalf("opening counted %d + %d seconds", e.used(libOpen), e.used(libClosed))
	}
	e.post(10*time.Second, "s-ipad", v1)
	e.post(0, "s-tablet", v2)
	e.post(10*time.Second, "s-ipad", v1)
	e.post(0, "s-tablet", v2)
	if e.used(libOpen) != 20 || e.used(libClosed) != 20 {
		t.Fatalf("two sessions playing 20 s each: %d and %d", e.used(libOpen), e.used(libClosed))
	}
	// the iPad pauses for ten minutes: it stops calling and streaming serves it
	// nothing. The tablet plays on.
	for i := 0; i < 60; i++ {
		e.post(10*time.Second, "s-tablet", v2)
	}
	if e.used(libOpen) != 20 || e.used(libClosed) != 620 {
		t.Fatalf("paused iPad, playing tablet: %d and %d, want 20 and 620", e.used(libOpen), e.used(libClosed))
	}
	e.post(0, "s-ipad", v1) // it plays again: the pause is not counted
	e.post(10*time.Second, "s-ipad", v1)
	if e.used(libOpen) != 30 {
		t.Fatalf("after the pause: %d, want 30", e.used(libOpen))
	}
	// a call after a long gap adds what streaming served in the lease, and the
	// heartbeat interval, no more, when streaming cannot say
	e.push.served["s-tablet"] = e.clock.Add(15 * time.Second)
	e.post(10*time.Minute, "s-tablet", v2)
	if e.used(libClosed) != 645 {
		t.Fatalf("after a gap streaming served 25 s of: %d, want 645", e.used(libClosed))
	}
	e.push.unknown = true
	e.post(10*time.Minute, "s-tablet", v2)
	if e.used(libClosed) != 705 {
		t.Fatalf("after a gap streaming knows nothing of: %d, want 705", e.used(libClosed))
	}
	e.push.unknown = false
	// the account's limit sees both libraries
	total := time.Duration(e.used(libOpen)+e.used(libClosed)) * time.Second
	st, err := e.keeper.Time(ctx, e.child, libOpen)
	if err != nil || !st.Allowed || st.Remaining != time.Hour-total {
		t.Fatalf("Time = %+v (%v), want %v left", st, err, time.Hour-total)
	}
	// streaming was told the same end, and serves for one interval more
	if p := e.push.last(t); p.userID != uChild || p.endAt == nil || !p.endAt.Equal(e.clock.Add(st.Remaining)) ||
		p.lease == nil || !p.lease.Equal(e.clock.Add(parental.HeartbeatCap)) {
		t.Fatalf("streaming was told %+v, want the end at %v and a lease of one interval", p, e.clock.Add(st.Remaining))
	}
	// the count is the database's: a new keeper (the home restarted) goes on from it
	again := parental.NewTimeKeeper(parental.NewCache(e.store), nil)
	if st2, err := again.Time(ctx, e.child, libOpen); err != nil || st2.Remaining != st.Remaining {
		t.Fatalf("after a restart: %+v (%v)", st2, err)
	}
	// an account with no policy is never counted
	if err := e.keeper.Opened(ctx, uViewer, libOpen, v1, "s-viewer"); err != nil {
		t.Fatal(err)
	}
	e.clock = e.clock.Add(10 * time.Second)
	if err := e.keeper.Progress(ctx, uViewer, "s-viewer"); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM parental_time_usage WHERE user_id = $1`, uViewer); n != 0 {
		t.Fatalf("an unrestricted account was counted: %d rows", n)
	}
}

// Security finding 1: when the keeper holds as many session marks as it keeps, a
// new session is still counted (the oldest mark is counted and dropped, never the
// new one skipped), and a session id the home did not open for the account neither
// makes a mark nor counts for anyone.
func TestFullMarksStillCount(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	e.keeper.MarksKept = 2
	v := e.video(libOpen)
	ids := []string{e.session(uChild, v), e.session(uChild, v), e.session(uChild, v), e.session(uChild, v)}
	for _, id := range ids { // four sessions open, two marks kept
		if err := e.keeper.Opened(ctx, uChild, libOpen, v, id); err != nil {
			t.Fatalf("Opened: %v", err)
		}
	}
	for i := 0; i < 3; i++ { // all four play for 30 seconds
		e.clock = e.clock.Add(10 * time.Second)
		for _, id := range ids {
			if err := e.keeper.Progress(ctx, uChild, id); err != nil {
				t.Fatalf("Progress: %v", err)
			}
		}
	}
	if got := e.used(libOpen); got < 4*30 {
		t.Fatalf("four sessions played 30 s each with two marks kept: %d s counted, want at least 120", got)
	}

	before := e.used(libOpen)
	pushes := len(e.push.got)
	foreign := e.session(uGuardian, v)
	for _, id := range []string{uuid.NewString(), foreign, "not-a-session"} {
		e.clock = e.clock.Add(10 * time.Second)
		if err := e.keeper.Progress(ctx, uChild, id); !errors.Is(err, parental.ErrUnknownSession) {
			t.Fatalf("Progress(%s) = %v, want ErrUnknownSession", id, err)
		}
	}
	if e.used(libOpen) != before || len(e.push.got) != pushes {
		t.Fatalf("a session that is not the child's was counted or renewed the lease: %d s, %d pushes", e.used(libOpen)-before, len(e.push.got)-pushes)
	}
	// the child's own sessions were not let off by those calls
	e.clock = e.clock.Add(10 * time.Second)
	if err := e.keeper.Progress(ctx, uChild, ids[0]); err != nil {
		t.Fatal(err)
	}
	if e.used(libOpen) < before+40 {
		t.Fatalf("after the foreign calls the child's session counted %d s, want at least 40", e.used(libOpen)-before)
	}
}

// Security finding 2: the count does not hang on the client. A session that never
// posts progress is served for one heartbeat interval only (the lease), and what
// streaming served it is counted with the account's next stream; nothing but a
// counted call renews the lease; and when the count cannot be written the lease is
// not renewed and the seconds stay owed.
func TestTimeCountsWithoutTheClient(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	v := e.video(libOpen)

	// a client opens a stream and never posts progress
	st, err := e.keeper.Admit(ctx, e.child, libOpen)
	if err != nil || !st.Allowed {
		t.Fatalf("Admit = %+v (%v)", st, err)
	}
	if p := e.push.last(t); p.lease != nil || p.endAt == nil {
		t.Fatalf("streaming was told %+v, want the account's end and no lease before a session is open", p)
	}
	e.post(0, "s-silent", v)
	if p := e.push.last(t); p.session != "s-silent" || p.lease == nil || !p.lease.Equal(e.clock.Add(parental.HeartbeatCap)) {
		t.Fatalf("streaming was told %+v, want a lease of one heartbeat interval for the session", p)
	}
	// the rules change and the child reads its own time: neither is a counted call
	if err := e.keeper.Refresh(ctx, uChild, libOpen); err != nil {
		t.Fatal(err)
	}
	if p := e.push.last(t); p.lease != nil {
		t.Fatalf("a refresh renewed the lease: %+v", p)
	}
	// streaming served it until the lease ran out; ten minutes later it opens again
	e.push.served["s-silent"] = e.clock.Add(parental.HeartbeatCap)
	e.clock = e.clock.Add(10 * time.Minute)
	if st, err = e.keeper.Admit(ctx, e.child, libOpen); err != nil || st.Remaining != 59*time.Minute {
		t.Fatalf("after a silent stream: %+v (%v), want 59 minutes left", st, err)
	}
	if e.used(libOpen) != 60 {
		t.Fatalf("a stream with no progress posts counted %d s, want 60", e.used(libOpen))
	}
	// closing a session counts it up to its close: nothing more was served
	if err := e.keeper.Closed(ctx, "s-silent"); err != nil || e.used(libOpen) != 60 {
		t.Fatalf("Closed: %v, %d s counted", err, e.used(libOpen))
	}
	if p := e.push.last(t); p.session != "s-silent" || p.lease == nil || p.lease.After(e.clock) {
		t.Fatalf("the session closed and its lease goes on: %+v", p)
	}
	// and again, with streaming unable to say what it served: the whole interval
	e.post(0, "s-silent-2", v)
	e.push.fail = errors.New("streaming is away")
	e.clock = e.clock.Add(10 * time.Minute)
	if _, err = e.keeper.Admit(ctx, e.child, libOpen); err == nil {
		t.Fatal("streaming could not be told, and the stream was admitted")
	}
	if e.used(libOpen) != 120 {
		t.Fatalf("with streaming away the silent stream counted %d s, want 120", e.used(libOpen))
	}
	e.push.fail = nil

	// the count cannot be written: no lease is renewed, and the seconds stay owed
	e.post(0, "s-ipad", v)
	e.exec(`ALTER TABLE parental_time_usage RENAME TO parental_time_usage_away`)
	pushes := len(e.push.got)
	e.clock = e.clock.Add(20 * time.Second)
	err = e.keeper.Progress(ctx, uChild, "s-ipad")
	e.exec(`ALTER TABLE parental_time_usage_away RENAME TO parental_time_usage`)
	if err == nil || len(e.push.got) != pushes {
		t.Fatalf("the count failed, and Progress said %v and told streaming %d times", err, len(e.push.got)-pushes)
	}
	e.post(10*time.Second, "s-ipad", v)
	if e.used(libOpen) != 150 {
		t.Fatalf("after the write came back: %d s, want 150", e.used(libOpen))
	}
}

// Security finding 3: a session is never served without a mark that counts it. The
// lease is the session's own; a mark is let go (the marks are full, or it was idle)
// only after what streaming served it is counted and its lease is ended; when that
// cannot be done the mark stays and the new session is refused; a mark streaming
// still serves is kept; and a session let go is served again only through a counted
// call.
func TestNoSessionServedWithoutItsMark(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 600, ""))
	v := e.video(libOpen)
	s1, s2, s3 := e.session(uChild, v), e.session(uChild, v), e.session(uChild, v)
	ended := func(session string) bool {
		l := e.push.lease(session)
		return l != nil && !l.After(e.clock)
	}

	// each session has its own lease: one that posts does not carry another
	e.post(0, s1, v)
	e.post(0, s2, v)
	e.post(30*time.Second, s2, v)
	if l1, l2 := e.push.lease(s1), e.push.lease(s2); l1 == nil || l2 == nil || !l2.Equal(l1.Add(30*time.Second)) {
		t.Fatalf("leases %v and %v: a call for one session renewed the other, or gave none", l1, l2)
	}
	if err := e.keeper.Closed(ctx, s2); err != nil {
		t.Fatal(err)
	}
	if !ended(s2) || ended(s1) {
		t.Fatal("closing one session must end its lease and no other")
	}
	base := e.used(libOpen) // s2 played 30 s; s1 is still to count

	// the marks are full: the oldest is counted from what streaming served it, and
	// its lease is ended, before the new session gets its own
	e.keeper.MarksKept = 1
	e.push.served[s1] = e.clock.Add(20 * time.Second)
	e.clock = e.clock.Add(5 * time.Minute)
	if err := e.keeper.Opened(ctx, uChild, libOpen, v, s3); err != nil {
		t.Fatalf("Opened: %v", err)
	}
	if got := e.used(libOpen) - base; got != 50 {
		t.Fatalf("the mark let go was counted %d s, want the 50 streaming served it", got)
	}
	if !ended(s1) || ended(s3) {
		t.Fatalf("after the marks filled: lease of the one let go %v, of the new one %v", e.push.lease(s1), e.push.lease(s3))
	}

	// streaming cannot be told the lease ended: the mark stays, the new one is refused
	s4 := e.session(uChild, v)
	e.push.fail = errors.New("streaming is away")
	e.clock = e.clock.Add(10 * time.Second)
	err := e.keeper.Opened(ctx, uChild, libOpen, v, s4)
	e.push.fail = nil
	if err == nil || e.push.lease(s4) != nil {
		t.Fatalf("Opened = %v with the marks full and streaming away, lease %v: want an error and no lease", err, e.push.lease(s4))
	}
	if err := e.keeper.Progress(ctx, uChild, s3); err != nil || e.used(libOpen)-base != 60 {
		t.Fatalf("the mark that had to stay: %v, %d s counted, want 60", err, e.used(libOpen)-base)
	}

	// a session let go comes back through the API: counted again, with a new lease
	e.clock = e.clock.Add(10 * time.Second)
	if err := e.keeper.Progress(ctx, uChild, s1); err != nil {
		t.Fatalf("Progress for a session let go: %v", err)
	}
	if ended(s1) || !ended(s3) {
		t.Fatal("the session that called has no lease, or the one let go for it still has one")
	}

	// an idle mark that streaming still serves is kept and counted; once streaming
	// serves it nothing it is let go, its lease ended
	e.keeper.MarksKept = 0
	base = e.used(libOpen)
	e.clock = e.clock.Add(7 * time.Hour)
	e.push.served[s1] = e.clock.Add(-time.Second)
	if _, err := e.keeper.Admit(ctx, e.child, libOpen); err != nil {
		t.Fatal(err)
	}
	if e.used(libOpen) == base {
		t.Fatal("an idle session that streaming still served was not counted")
	}
	base = e.used(libOpen)
	e.clock = e.clock.Add(2 * time.Minute)
	e.push.served[s1] = e.clock // still served: the mark was kept, so this is counted
	if _, err := e.keeper.Admit(ctx, e.child, libOpen); err != nil {
		t.Fatal(err)
	}
	if got := e.used(libOpen) - base; got != 120 {
		t.Fatalf("the idle mark was dropped while streaming served it: %d s counted, want 120", got)
	}
	pushes := len(e.push.got)
	e.clock = e.clock.Add(2 * time.Minute) // nothing more served
	if _, err := e.keeper.Admit(ctx, e.child, libOpen); err != nil {
		t.Fatal(err)
	}
	let := false
	for _, p := range e.push.got[pushes:] {
		let = let || (p.session == s1 && p.lease != nil && !p.lease.After(e.clock))
	}
	if !let {
		t.Fatal("the idle mark was not let go with its lease ended")
	}
}

// The first acceptance case: 60 minutes watched in one session closes a new stream
// anywhere, and the stream still running is told to end now.
func TestLimitReachedClosesEveryDevice(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	v := e.video(libOpen)
	e.post(0, "s-ipad", v)
	for i := 1; i <= 60; i++ {
		e.post(time.Minute, "s-ipad", v)
	}
	if e.used(libOpen) != 3600 {
		t.Fatalf("after an hour: %d seconds", e.used(libOpen))
	}
	st, err := e.keeper.Admit(ctx, e.child, libClosed)
	if err != nil || st.Allowed || st.Reason != parental.ReasonLimitReached {
		t.Fatalf("Admit = %+v (%v), want limit_reached", st, err)
	}
	want := at(t, e.loc, "2026-11-11 04:00:00")
	if st.NextAllowedAt == nil || !st.NextAllowedAt.Equal(want) {
		t.Fatalf("next allowed %v, want %v", st.NextAllowedAt, want)
	}
	if p := e.push.last(t); p.endAt == nil || p.endAt.After(e.clock) || p.reason != parental.ReasonLimitReached {
		t.Fatalf("streaming was told %+v, want an end no later than now", p)
	}
	// the day line opens it again
	e.clock = want
	if st, err := e.keeper.Time(ctx, e.child, libOpen); err != nil || !st.Allowed || st.Remaining != time.Hour {
		t.Fatalf("after the day line: %+v (%v)", st, err)
	}
}

// T05: a grant after the limit allows the granted minutes that day and no more,
// and opens a closed slot until it runs out.
func TestGrantAllowsTheGrantedMinutes(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 1, ""))
	v := e.video(libOpen)
	e.post(0, "s1", v)
	e.post(time.Minute, "s1", v)
	if st, _ := e.keeper.Time(ctx, e.child, libOpen); st.Allowed {
		t.Fatalf("the limit is reached: %+v", st)
	}
	for _, m := range []int{0, -5, parental.MaxGrantMinutes + 1} {
		if _, err := e.store.GrantTime(ctx, uGuardian, uChild, m); !errors.Is(err, parental.ErrInvalid) {
			t.Fatalf("GrantTime(%d) = %v, want ErrInvalid", m, err)
		}
	}
	if _, err := e.store.GrantTime(ctx, uGuardian, uViewer, 30); !errors.Is(err, parental.ErrNotRestricted) {
		t.Fatalf("a grant to an unrestricted account: %v", err)
	}
	g, err := e.store.GrantTime(ctx, uGuardian, uChild, 30)
	if err != nil || g.Day != "2026-11-10" || g.Minutes != 30 || g.GrantedBy != uGuardian {
		t.Fatalf("GrantTime = %+v (%v)", g, err)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = $1`, parental.AuditTimeGranted); n != 1 {
		t.Fatalf("%d audit rows for the grant", n)
	}
	st, err := e.keeper.Admit(ctx, e.child, libOpen)
	if err != nil || !st.Allowed || st.Remaining != 30*time.Minute {
		t.Fatalf("after the grant: %+v (%v), want 30 minutes", st, err)
	}
	// 20 minutes of it are watched
	for i := 1; i <= 20; i++ {
		e.post(time.Minute, "s1", v)
	}
	if n := e.count(`SELECT used_seconds FROM parental_time_grants WHERE id = $1`, g.ID); n != 1200 {
		t.Fatalf("the grant has %d seconds used, want 1200", n)
	}
	if st, _ := e.keeper.Time(ctx, e.child, libOpen); !st.Allowed || st.Remaining != 10*time.Minute {
		t.Fatalf("20 minutes into the grant: %+v", st)
	}
	for i := 21; i <= 30; i++ {
		e.post(time.Minute, "s1", v)
	}
	if st, _ := e.keeper.Time(ctx, e.child, libOpen); st.Allowed || st.Reason != parental.ReasonLimitReached {
		t.Fatalf("the grant is used up: %+v", st)
	}
	// that day only: a grant not used is gone at the day line
	if _, err := e.store.GrantTime(ctx, uGuardian, uChild, 60); err != nil {
		t.Fatal(err)
	}
	e.clock = at(t, e.loc, "2026-11-11 04:00:00")
	if st, _ := e.keeper.Time(ctx, e.child, libOpen); !st.Allowed || st.Remaining != time.Minute {
		t.Fatalf("the next day has its own minute and no grant: %+v", st)
	}

	// a closed slot opens for the granted minutes
	e.rules(slot(everyDay, "07:00", "20:00"))
	e.clock = at(t, e.loc, "2026-11-11 21:00:00")
	if st, _ := e.keeper.Time(ctx, e.child, libOpen); st.Allowed || st.Reason != parental.ReasonOutsideSlot {
		t.Fatalf("outside the slot: %+v", st)
	}
	if _, err := e.store.GrantTime(ctx, uGuardian, uChild, 15); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.keeper.Time(ctx, e.child, libOpen); !st.Allowed || st.Remaining != 15*time.Minute {
		t.Fatalf("a grant outside the slot: %+v", st)
	}
	e.post(0, "s2", v)
	for i := 1; i <= 15; i++ {
		e.post(time.Minute, "s2", v)
	}
	if st, _ := e.keeper.Time(ctx, e.child, libOpen); st.Allowed {
		t.Fatalf("the grant outside the slot is used up: %+v", st)
	}
}

// A library rule narrows the account's: that library stops at its own limit and
// the others go on.
func TestLibraryRuleNarrows(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 120, ""), limit(everyDay, 30, libOpen))
	v := e.video(libOpen)
	e.post(0, "s1", v)
	for i := 1; i <= 30; i++ {
		e.post(time.Minute, "s1", v)
	}
	if st, _ := e.keeper.Time(ctx, e.child, libOpen); st.Allowed || st.Reason != parental.ReasonLimitReached {
		t.Fatalf("the library is at its 30 minutes: %+v", st)
	}
	if st, _ := e.keeper.Time(ctx, e.child, libClosed); !st.Allowed || st.Remaining != 90*time.Minute {
		t.Fatalf("another library goes on to 120: %+v", st)
	}
}

func TestTimeRulesAreKeptAndChecked(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	mins := 45
	got, err := e.store.SetTimeRules(ctx, uGuardian, uChild, []parental.TimeRule{
		{Days: 62, SlotStart: "16:00", SlotEnd: "19:30", DailyMinutes: &mins},
		{Days: 65, SlotStart: "21:00", SlotEnd: "01:00", LibraryID: libOpen},
	})
	if err != nil || len(got) != 2 {
		t.Fatalf("SetTimeRules = %+v (%v)", got, err)
	}
	if r := got[0]; r.LibraryID != "" || r.Days != 62 || r.SlotStart != "16:00" || r.SlotEnd != "19:30" || r.DailyMinutes == nil || *r.DailyMinutes != 45 || r.ID == "" {
		t.Fatalf("the account rule came back as %+v", r)
	}
	if r := got[1]; r.LibraryID != libOpen || r.SlotStart != "21:00" || r.SlotEnd != "01:00" || r.DailyMinutes != nil {
		t.Fatalf("the library rule came back as %+v", r)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = $1`, parental.AuditTimeRulesSet); n != 1 {
		t.Fatalf("%d audit rows for the rules", n)
	}
	// the policy's version is the content filter's: time rules leave it alone
	if n := e.count(`SELECT version FROM parental_policies WHERE user_id = $1`, uChild); n != 1 {
		t.Fatalf("the policy version moved to %d", n)
	}
	neg, big := -1, 1441
	for what, r := range map[string]parental.TimeRule{
		"no days":           {Days: 0, DailyMinutes: &mins},
		"days past 127":     {Days: 128, DailyMinutes: &mins},
		"half a slot":       {Days: 1, SlotStart: "16:00"},
		"an empty slot":     {Days: 1, SlotStart: "16:00", SlotEnd: "16:00"},
		"a bad time":        {Days: 1, SlotStart: "25:00", SlotEnd: "16:00"},
		"words for a time":  {Days: 1, SlotStart: "noon", SlotEnd: "16:00"},
		"nothing":           {Days: 1},
		"negative minutes":  {Days: 1, DailyMinutes: &neg},
		"too many minutes":  {Days: 1, DailyMinutes: &big},
		"a bad library id":  {Days: 1, DailyMinutes: &mins, LibraryID: "x"},
		"a missing library": {Days: 1, DailyMinutes: &mins, LibraryID: "00000000-0000-4000-8000-0000005700ff"},
	} {
		if _, err := e.store.SetTimeRules(ctx, uGuardian, uChild, []parental.TimeRule{r}); !errors.Is(err, parental.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", what, err)
		}
	}
	if kept, err := e.store.TimeRules(ctx, uChild); err != nil || len(kept) != 2 {
		t.Fatalf("a refused change touched the rules: %+v (%v)", kept, err)
	}
	if _, err := e.store.SetTimeRules(ctx, uGuardian, uViewer, nil); !errors.Is(err, parental.ErrNotRestricted) {
		t.Fatalf("rules on an unrestricted account: %v", err)
	}
	if _, err := e.store.TimeRules(ctx, uViewer); !errors.Is(err, parental.ErrNotRestricted) {
		t.Fatalf("rules of an unrestricted account: %v", err)
	}
	// none: no time limit, and streaming is told nothing ends the stream
	if got, err := e.store.SetTimeRules(ctx, uGuardian, uChild, nil); err != nil || len(got) != 0 {
		t.Fatalf("clearing: %+v (%v)", got, err)
	}
	st, err := e.keeper.Admit(ctx, e.child, libOpen)
	if err != nil || !st.Allowed || st.EndsAt != nil || st.Limited {
		t.Fatalf("no rules: %+v (%v)", st, err)
	}
	if p := e.push.last(t); p.endAt != nil {
		t.Fatalf("streaming was told an end with no rules: %+v", p)
	}
	// lifting the restriction takes the rules with it
	e.rules(limit(everyDay, 60, ""))
	if err := e.store.Lift(ctx, uAdmin, uChild); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM parental_time_rules WHERE user_id = $1`, uChild); n != 0 {
		t.Fatalf("%d rules left after lifting", n)
	}
}

// An admin and an unrestricted account are allowed with no time query; a failure
// to read, or to tell streaming, refuses.
func TestTimeFailsClosed(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	admin := &principal.Principal{UserID: uAdmin, IsAdmin: true, AccessAllLibraries: true}
	viewer := &principal.Principal{UserID: uViewer, Source: principal.SourceJWT}
	for _, p := range []*principal.Principal{admin, viewer} {
		if st, err := e.keeper.Admit(ctx, p, libOpen); err != nil || !st.Allowed || st.EndsAt != nil {
			t.Fatalf("%s: %+v (%v)", p.UserID, st, err)
		}
	}
	if len(e.push.got) != 0 {
		t.Fatalf("streaming was told about an unrestricted account: %+v", e.push.got)
	}
	if _, err := e.keeper.Time(ctx, nil, libOpen); err == nil {
		t.Fatal("no principal was answered")
	}
	e.push.fail = errors.New("streaming is away")
	if st, err := e.keeper.Admit(ctx, e.child, libOpen); err == nil || st.Allowed {
		t.Fatalf("streaming could not be told, and Admit said %+v (%v)", st, err)
	}
	e.push.fail = nil
	e.exec(`ALTER TABLE parental_time_usage RENAME TO parental_time_usage_away`)
	defer e.exec(`ALTER TABLE parental_time_usage_away RENAME TO parental_time_usage`)
	if st, err := e.keeper.Admit(ctx, e.child, libOpen); err == nil || st.Allowed {
		t.Fatalf("the count could not be read, and Admit said %+v (%v)", st, err)
	}
}

// The home's zone: read from app_settings; a change keeps the current day and
// applies from the next day line.
func TestHomeTimezone(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	loc, err := e.store.HomeZone(ctx)
	if err != nil || loc.String() != "Asia/Riyadh" {
		t.Fatalf("HomeLocation = %v (%v)", loc, err)
	}
	for _, bad := range []string{"", "Local", "Mars/Olympus"} {
		if err := e.store.SetHomeTimezone(ctx, uAdmin, bad); !errors.Is(err, parental.ErrInvalid) {
			t.Fatalf("SetHomeTimezone(%q) = %v", bad, err)
		}
	}
	// 17:00 Tuesday in Riyadh; Tokyo is six hours ahead
	if err := e.store.SetHomeTimezone(ctx, uAdmin, "Asia/Tokyo"); err != nil {
		t.Fatal(err)
	}
	if loc, _ := e.store.HomeZone(ctx); loc.String() != "Asia/Riyadh" {
		t.Fatalf("the zone changed in the middle of the day: %v", loc)
	}
	e.rules(limit(everyDay, 60, ""))
	v := e.video(libOpen)
	e.post(0, "s1", v)
	e.post(time.Minute, "s1", v)
	if n := e.count(`SELECT seconds FROM parental_time_usage WHERE user_id = $1 AND day = '2026-11-10'`, uChild); n != 60 {
		t.Fatalf("the current day's count: %d", n)
	}
	e.clock = at(t, e.loc, "2026-11-11 04:00:00") // the old zone's day line
	if loc, _ := e.store.HomeZone(ctx); loc.String() != "Asia/Tokyo" {
		t.Fatalf("after the day line: %v", loc)
	}
	// an unknown stored value falls back to the server's zone rather than failing
	e.exec(`UPDATE app_settings SET value = '"Mars/Olympus"' WHERE key = 'home.timezone'`)
	if loc, err := e.store.HomeZone(ctx); err != nil || loc != time.Local {
		t.Fatalf("an unknown zone: %v (%v)", loc, err)
	}
}

func TestTimeUsageRange(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	v := e.video(libOpen)
	e.post(0, "s1", v)
	e.post(30*time.Second, "s1", v)
	if _, err := e.store.GrantTime(ctx, uGuardian, uChild, 15); err != nil {
		t.Fatal(err)
	}
	u, err := e.store.TimeUsage(ctx, uChild, "", "")
	if err != nil || u.From != "2026-11-04" || u.To != "2026-11-10" || u.Today != "2026-11-10" || u.TimeZone != "Asia/Riyadh" {
		t.Fatalf("TimeUsage = %+v (%v)", u, err)
	}
	if len(u.Usage) != 1 || u.Usage[0].Seconds != 30 || u.Usage[0].LibraryID != libOpen || u.Usage[0].Day != "2026-11-10" {
		t.Fatalf("usage %+v", u.Usage)
	}
	if len(u.Grants) != 1 || u.Grants[0].Minutes != 15 {
		t.Fatalf("grants %+v", u.Grants)
	}
	if u, err := e.store.TimeUsage(ctx, uChild, "2026-11-01", "2026-11-09"); err != nil || len(u.Usage) != 0 || len(u.Grants) != 0 {
		t.Fatalf("a range before today: %+v (%v)", u, err)
	}
	for _, r := range [][2]string{{"2026-11-10", "2026-11-01"}, {"x", "2026-11-01"}, {"2026-01-01", "2026-11-01"}, {"2026-11-01", "soon"}} {
		if _, err := e.store.TimeUsage(ctx, uChild, r[0], r[1]); !errors.Is(err, parental.ErrInvalid) {
			t.Fatalf("range %v: %v, want ErrInvalid", r, err)
		}
	}
	if _, err := e.store.TimeUsage(ctx, uViewer, "", ""); !errors.Is(err, parental.ErrNotRestricted) {
		t.Fatalf("usage of an unrestricted account: %v", err)
	}
}
