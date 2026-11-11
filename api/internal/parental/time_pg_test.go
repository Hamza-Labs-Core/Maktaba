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
	endAt, next               *time.Time
}

type fakePusher struct {
	mu   sync.Mutex
	got  []pushed
	fail error
}

func (f *fakePusher) SetTimeLimit(_ context.Context, userID, libraryID string, endAt *time.Time, reason string, next *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.got = append(f.got, pushed{userID, libraryID, reason, endAt, next})
	return nil
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
}

// setupTime: the child is restricted, the home is in Riyadh, and the clock stands
// at 17:00 on Tuesday 10 November 2026.
func setupTime(t *testing.T) *timeEnv {
	t.Helper()
	e := &timeEnv{env: setup(t), loc: homeZone(t), push: &fakePusher{}}
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

// post is one progress post of a session, after the clock moved on by d.
func (e *timeEnv) post(d time.Duration, session, video string, pos float64) {
	e.t.Helper()
	e.clock = e.clock.Add(d)
	if err := e.keeper.Progress(context.Background(), uChild, session, video, pos); err != nil {
		e.t.Fatalf("Progress: %v", err)
	}
}

// T02: progress posts from two sessions both count; a paused one adds none; one
// post never adds more than the heartbeat interval.
func TestUsageCountsEverySessionAndNoPause(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	v1, v2 := e.video(libOpen), e.video(libClosed)

	e.post(0, "s-ipad", v1, 100) // the first post of a session only marks it
	e.post(0, "s-tablet", v2, 0)
	if e.used(libOpen)+e.used(libClosed) != 0 {
		t.Fatalf("a first post counted %d + %d seconds", e.used(libOpen), e.used(libClosed))
	}
	e.post(10*time.Second, "s-ipad", v1, 110)
	e.post(0, "s-tablet", v2, 10)
	e.post(10*time.Second, "s-ipad", v1, 120)
	e.post(0, "s-tablet", v2, 20)
	if e.used(libOpen) != 20 || e.used(libClosed) != 20 {
		t.Fatalf("two sessions playing 20 s each: %d and %d", e.used(libOpen), e.used(libClosed))
	}
	// the iPad pauses: the position stands still, the tablet plays on
	e.post(10*time.Second, "s-ipad", v1, 120)
	e.post(0, "s-tablet", v2, 30)
	e.post(10*time.Second, "s-ipad", v1, 120)
	e.post(0, "s-tablet", v2, 40)
	if e.used(libOpen) != 20 || e.used(libClosed) != 40 {
		t.Fatalf("paused iPad, playing tablet: %d and %d, want 20 and 40", e.used(libOpen), e.used(libClosed))
	}
	// a post after a long gap adds the heartbeat interval and no more
	e.post(10*time.Minute, "s-tablet", v2, 50)
	if e.used(libClosed) != 100 {
		t.Fatalf("after a ten minute gap: %d, want 100", e.used(libClosed))
	}
	// the account's limit sees both libraries
	st, err := e.keeper.Time(ctx, e.child, libOpen)
	if err != nil || !st.Allowed || st.Remaining != time.Hour-120*time.Second {
		t.Fatalf("Time = %+v (%v), want 58 minutes left", st, err)
	}
	// streaming was told the same end
	if p := e.push.last(t); p.userID != uChild || p.endAt == nil || !p.endAt.Equal(e.clock.Add(st.Remaining)) {
		t.Fatalf("streaming was told %+v, want the end at %v", p, e.clock.Add(st.Remaining))
	}
	// the count is the database's: a new keeper (the home restarted) goes on from it
	again := parental.NewTimeKeeper(parental.NewCache(e.store), nil)
	if st2, err := again.Time(ctx, e.child, libOpen); err != nil || st2.Remaining != st.Remaining {
		t.Fatalf("after a restart: %+v (%v)", st2, err)
	}
	// an account with no policy is never counted
	if err := e.keeper.Progress(ctx, uViewer, "s-viewer", v1, 5); err != nil {
		t.Fatal(err)
	}
	e.clock = e.clock.Add(10 * time.Second)
	if err := e.keeper.Progress(ctx, uViewer, "s-viewer", v1, 15); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM parental_time_usage WHERE user_id = $1`, uViewer); n != 0 {
		t.Fatalf("an unrestricted account was counted: %d rows", n)
	}
}

// The first acceptance case: 60 minutes watched in one session closes a new stream
// anywhere, and the stream still running is told to end now.
func TestLimitReachedClosesEveryDevice(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	v := e.video(libOpen)
	e.post(0, "s-ipad", v, 0)
	for i := 1; i <= 60; i++ {
		e.post(time.Minute, "s-ipad", v, float64(i*60))
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
	e.post(0, "s1", v, 0)
	e.post(time.Minute, "s1", v, 60)
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
		e.post(time.Minute, "s1", v, float64(60+i*60))
	}
	if n := e.count(`SELECT used_seconds FROM parental_time_grants WHERE id = $1`, g.ID); n != 1200 {
		t.Fatalf("the grant has %d seconds used, want 1200", n)
	}
	if st, _ := e.keeper.Time(ctx, e.child, libOpen); !st.Allowed || st.Remaining != 10*time.Minute {
		t.Fatalf("20 minutes into the grant: %+v", st)
	}
	for i := 21; i <= 30; i++ {
		e.post(time.Minute, "s1", v, float64(60+i*60))
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
	e.post(0, "s2", v, 0)
	for i := 1; i <= 15; i++ {
		e.post(time.Minute, "s2", v, float64(i*60))
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
	e.post(0, "s1", v, 0)
	for i := 1; i <= 30; i++ {
		e.post(time.Minute, "s1", v, float64(i*60))
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
	loc, err := e.store.HomeLocation(ctx)
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
	if loc, _ := e.store.HomeLocation(ctx); loc.String() != "Asia/Riyadh" {
		t.Fatalf("the zone changed in the middle of the day: %v", loc)
	}
	e.rules(limit(everyDay, 60, ""))
	v := e.video(libOpen)
	e.post(0, "s1", v, 0)
	e.post(time.Minute, "s1", v, 60)
	if n := e.count(`SELECT seconds FROM parental_time_usage WHERE user_id = $1 AND day = '2026-11-10'`, uChild); n != 60 {
		t.Fatalf("the current day's count: %d", n)
	}
	e.clock = at(t, e.loc, "2026-11-11 04:00:00") // the old zone's day line
	if loc, _ := e.store.HomeLocation(ctx); loc.String() != "Asia/Tokyo" {
		t.Fatalf("after the day line: %v", loc)
	}
	// an unknown stored value falls back to the server's zone rather than failing
	e.exec(`UPDATE app_settings SET value = '"Mars/Olympus"' WHERE key = 'home.timezone'`)
	if loc, err := e.store.HomeLocation(ctx); err != nil || loc != time.Local {
		t.Fatalf("an unknown zone: %v (%v)", loc, err)
	}
}

func TestTimeUsageRange(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	v := e.video(libOpen)
	e.post(0, "s1", v, 0)
	e.post(30*time.Second, "s1", v, 30)
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
