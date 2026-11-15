//go:build broadcastrehearsal

package rehearsal

// The scenarios whose cloud part is the cloud's own posts: the plan pushes (50.20), the access
// posts and invites (50.26), the revocation lists of a takedown (50.24, 50.25) and the usage
// posts the edge sends back (50.20 §3, 14.8). As in broadcast-kill, the test signs them with the
// cloud's TEST key in the cloud's exact format (tools/broadcast-viewers/tokens.go, checked byte
// for byte against cloud/internal/broadcast/testdata), and the real edge in Miniflare and the
// real home's broadcaster do the rest. Viewers come in through watch.maktaba.tv's API (redeem,
// Turnstile, play, heartbeat) as the web player does, then play from the edge.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	viewers "github.com/Hamza-Labs-Core/Maktaba/tools/broadcast-viewers"
)

// ---- the helpers -------------------------------------------------------------------------

// watchAnswer is any watch.maktaba.tv API answer the scenarios read.
type watchAnswer struct {
	Code       int         `json:"-"`
	Raw        string      `json:"-"`
	Error      string      `json:"error"`
	Pass       string      `json:"pass"`
	Session    string      `json:"session"`
	SessionID  string      `json:"session_id"`
	BeatKey    string      `json:"beat_key"`
	MasterURL  string      `json:"master_url"`
	State      string      `json:"state"`
	Reused     bool        `json:"reused"`
	Programmes []programme `json:"programmes"`
}

type attribution struct {
	Title   string `json:"title"`
	By      string `json:"by"`
	Licence string `json:"licence"`
	URL     string `json:"url"`
}

type programme struct {
	Start       string       `json:"start"`
	End         string       `json:"end"`
	Title       string       `json:"title"`
	Attribution *attribution `json:"attribution,omitempty"`
}

// watch calls watch.maktaba.tv's API on the edge with a credential (a guest pass or an open
// session) as the apps send it.
func (s *stack) watch(method, path string, body any, cred string) watchAnswer {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	hdr := map[string]string{"Content-Type": "application/json"}
	if cred != "" {
		hdr["Authorization"] = "Bearer " + cred
	}
	code, out, err := s.do(method, s.edge+path, raw, hdr)
	a := watchAnswer{Code: code, Raw: string(out)}
	if err != nil {
		a.Error = err.Error()
		return a
	}
	_ = json.Unmarshal(out, &a)
	return a
}

func (s *stack) redeem(invite, cred string) watchAnswer {
	return s.watch(http.MethodPost, "/api/v1/redeem", map[string]string{"invite": invite}, cred)
}

func (s *stack) playOn(ch, cred string) watchAnswer {
	return s.watch(http.MethodPost, "/api/v1/play", map[string]string{"publisher": pub, "channel": ch}, cred)
}

func (s *stack) watchBeat(a watchAnswer) watchAnswer {
	return s.watch(http.MethodPost, "/api/v1/play/"+a.SessionID+"/beat", map[string]string{"publisher": pub, "beat_key": a.BeatKey}, "")
}

// pushPlan sends a plan push as the cloud does on every billing change (PUT /v1/control/plan).
func (s *stack) pushPlan(p viewers.Plan) (int, string) {
	p.Pub = pub
	post, err := viewers.PlanPost(s.key, p, time.Now().Add(s.skew))
	s.must("a plan push signed as the cloud", err == nil, err)
	code, raw, err := s.do(http.MethodPut, s.edge+"/v1/control/plan", []byte(post), nil)
	if err != nil {
		return 0, err.Error()
	}
	return code, string(raw)
}

// installAccess sends the publisher's viewer access as the cloud's push does (PUT /v1/control/access).
func (s *stack) installAccess(a viewers.AccessState) {
	a.Pub = pub
	post, err := viewers.AccessPost(s.key, a, time.Now().Add(s.skew))
	s.must("an access post signed as the cloud", err == nil, err)
	code, raw, err := s.do(http.MethodPut, s.edge+"/v1/control/access", []byte(post), nil)
	s.must(fmt.Sprintf("the edge takes access v%d", a.Version), err == nil && code == http.StatusOK, code, string(raw), err)
}

// invite is an invite the publisher made in the cloud (IssueInvite), for chs (none: all).
func (s *stack) invite(inv string, max int, chs ...string) string {
	tok, err := viewers.Invite(s.key, viewers.InviteClaims{Pub: pub, Channels: chs, Inv: inv, Max: max, IssuedAt: time.Now().Add(s.skew)})
	s.must("an invite signed as the cloud", err == nil, err)
	return tok
}

// tokenOf is the viewer token in a play answer's master URL (https://live…/<token>/<ch>/master.m3u8).
func tokenOf(master string) string {
	u, err := url.Parse(master)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[0]
}

// passID is a guest pass's random id (its payload's id), the id the cloud revokes.
func passID(pass string) string {
	body, _, _ := strings.Cut(strings.TrimPrefix(pass, "mkbg1."), ".")
	raw, err := b64url(body)
	if err != nil {
		return ""
	}
	var p struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.ID
}

func b64url(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// variants is the rungs a session's master lists.
func (s *stack) variants(ch, master string) []string {
	code, raw, err := s.do(http.MethodGet, s.edge+"/"+tokenOf(master)+"/"+ch+"/master.m3u8", nil, nil)
	if err != nil || code != http.StatusOK {
		return nil
	}
	return viewers.Variants(string(raw))
}

// players are viewers who came in through watch's API and play with the token it gave them.
type players struct {
	wg   sync.WaitGroup
	mu   sync.Mutex
	list []*viewers.Player
	by   map[string]*viewers.Player
}

func (ps *players) start(ctx context.Context, s *stack, name, ch, master string) *viewers.Player {
	tok := tokenOf(master)
	ps.mu.Lock()
	loc := len(ps.list) % 5
	ps.mu.Unlock()
	p := viewers.NewPlayer(viewers.Config{Base: s.edge, Pub: pub, Channel: ch, Session: name, Location: loc,
		Token: func(string) (string, error) { return tok, nil }, Published: s.publishedAt,
		HTTP: &http.Client{Timeout: 10 * time.Second}})
	ps.mu.Lock()
	if ps.by == nil {
		ps.by = map[string]*viewers.Player{}
	}
	ps.list = append(ps.list, p)
	ps.by[name] = p
	ps.mu.Unlock()
	ps.wg.Add(1)
	go func() {
		defer ps.wg.Done()
		p.Run(ctx)
	}()
	return p
}

func (ps *players) stats() []viewers.PlayerStats {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := make([]viewers.PlayerStats, 0, len(ps.list))
	for _, p := range ps.list {
		out = append(out, p.Stats())
	}
	return out
}

// sinceAll says how long after t0 every player's mark (FirstSlate, FirstOffAir, LiveAgain) came,
// and how many have it.
func sinceAll(snap []viewers.PlayerStats, mark func(viewers.PlayerStats) time.Time, t0 time.Time) (time.Duration, int) {
	var worst time.Duration
	n := 0
	for _, p := range snap {
		if at := mark(p); !at.IsZero() && !at.Before(t0) {
			n++
			worst = max(worst, at.Sub(t0))
		}
	}
	return worst, n
}

func firstSlate(p viewers.PlayerStats) time.Time  { return p.FirstSlate }
func firstOffAir(p viewers.PlayerStats) time.Time { return p.FirstOffAir }
func liveAgain(p viewers.PlayerStats) time.Time   { return p.LiveAgain }

// slateNow: the rung's playlist ends in the slate.
func (s *stack) slateNow(ch string) bool {
	code, _, pl := s.playlist(ch, "src")
	return code == http.StatusOK && len(pl.Segments) > 0 && pl.Segments[len(pl.Segments)-1].Slate
}

func aeTotal(c edgeCounters, event string) int {
	n := 0
	for _, w := range c.Workers {
		n += w.AEByEvent[event]
	}
	return n
}

func (s *stack) homePutsAfter(t time.Time) int {
	n := 0
	for _, p := range s.homeMetrics().Puts {
		if p.Status == http.StatusCreated && p.Rung != "slate" && p.AtMS > t.UnixMilli() {
			n++
		}
	}
	return n
}

// ---- broadcast-plans (50.20) -------------------------------------------------------------

// plans: the cloud drives the publisher through full, lowest and wait, then adds pay-as-you-go
// (full again). Each new viewer gets the plan's mode in that order (a lowest session's master
// lists the lowest rung only; wait admits no one), and no session already playing is stopped:
// each keeps its mode, its heartbeats and its segments through every change. An older plan never
// replaces a newer one.
func plans(s *stack) {
	stepFor := time.Duration(envInt("BROADCAST_PLAN_STEP_SECONDS", 30)) * time.Second
	ch := newChannel("plans")
	s.publish(ch)
	s.waitLive(ch)
	s.installAccess(viewers.AccessState{Version: 1, Channels: map[string]viewers.AccessChannel{ch: {Access: "invite"}}, MaxSessionsPerIP: 3})
	inv := s.invite("inv-plans", 20, ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ps players
	type session struct {
		name, mode string
		a          watchAnswer
		p          *viewers.Player
	}
	var playing []*session
	beatAll := func(when string) {
		for _, x := range playing {
			b := s.watchBeat(x.a)
			s.check(fmt.Sprintf("%s keeps its %s session (%s)", x.name, x.mode, when), b.Code == http.StatusOK && b.State == x.mode,
				"session_mode", b.Code, b.Error, b.State)
		}
	}
	segs := func() map[string]int {
		out := map[string]int{}
		for _, x := range playing {
			out[x.name] = x.p.Stats().Segments
		}
		return out
	}

	base := time.Now().UnixMilli()
	steps := []struct{ mode, why string }{
		{"full", "Broadcaster"}, {"lowest", "the included minutes used"}, {"wait", "the cap reached without pay-as-you-go"},
		{"full", "pay-as-you-go added"},
	}
	var order []string
	for i, st := range steps {
		code, raw := s.pushPlan(viewers.Plan{Version: base + int64(i), Mode: st.mode})
		s.must(fmt.Sprintf("the edge takes plan v%d: %s (%s)", i+1, st.mode, st.why), code == http.StatusOK, code, raw)
		name := fmt.Sprintf("viewer-%d", i+1)
		d := s.redeem(inv, "")
		s.must(name+" redeems the invite", d.Code == http.StatusOK && d.Pass != "", d.Code, d.Error)
		a := s.playOn(ch, d.Pass)
		if st.mode == "wait" {
			s.check("a new viewer waits while the plan is wait", a.Code == http.StatusTooManyRequests && a.Error == "broadcast_full",
				"plan_mode", a.Code, a.Error)
			if a.Code == http.StatusTooManyRequests {
				order = append(order, "wait")
			}
			before := segs()
			time.Sleep(stepFor)
			after := segs()
			for _, x := range playing {
				s.check(x.name+" plays on while the plan is wait", after[x.name] > before[x.name], "session_segments",
					fmt.Sprintf("%d → %d segments", before[x.name], after[x.name]))
			}
			beatAll("wait")
			continue
		}
		ok := s.check(fmt.Sprintf("a new viewer gets %s", st.mode), a.Code == http.StatusOK && a.State == st.mode, "plan_mode", a.Code, a.Error, a.State)
		if !ok {
			continue
		}
		order = append(order, a.State)
		want := len(rungs)
		if st.mode == "lowest" {
			want = 1
		}
		v := s.variants(ch, a.MasterURL)
		s.check(fmt.Sprintf("a %s session's master lists %d rung(s)", st.mode, want), len(v) == want, "master_rungs", v)
		playing = append(playing, &session{name: name, mode: st.mode, a: a, p: ps.start(ctx, s, name, ch, a.MasterURL)})
		time.Sleep(stepFor)
		beatAll(st.mode)
	}
	code, raw := s.pushPlan(viewers.Plan{Version: base + 1, Mode: "wait"})
	s.check("an older plan never replaces a newer", code == http.StatusConflict, "plan_version", code, raw)
	a := s.playOn(ch, s.redeem(inv, "").Pass)
	s.check("still the newest plan's mode after the older push", a.Code == http.StatusOK && a.State == "full", "plan_mode", a.Code, a.Error, a.State)
	s.check("new viewers' modes in order: full, lowest, wait, full", strings.Join(order, " ") == "full lowest wait full", "plan_mode", order)

	cancel()
	ps.wg.Wait()
	snap := ps.stats()
	sum := viewers.Summarise(snap, nil)
	stopped := 0
	for _, p := range snap {
		if !p.FirstOffAir.IsZero() || p.State != viewers.StateLive {
			stopped++
		}
	}
	s.check("no playing session stopped", stopped == 0, "sessions_stopped", fmt.Sprintf("%d of %d stopped or not live", stopped, len(snap)), sum.States)
	s.check("no player error", sum.Errors == 0, "viewer_errors", sum.Errors, sum.FirstErrors)
	s.r.Latency = Latency(sum)
}

// ---- broadcast-access (50.26) ------------------------------------------------------------

// access: an invite-only channel and an open one. An invite admits its devices up to its uses
// (a device again reuses its pass); a short code redeems its invite, 10 tries a minute per
// network; open sessions start only after Turnstile (good once); the limits (open sessions per
// network, viewers per channel, daily minutes) refuse new sessions only, never one playing; a
// revoked pass or invite ends its sessions at their next heartbeat and lets no one new in.
func access(s *stack) {
	open := newChannel("open")
	inv := newChannel("inv")
	s.publish(open)
	s.waitLive(open)
	codeInvite := s.invite("inv-code", 5)
	st := viewers.AccessState{Version: 1, OpenAllowed: true, MaxSessionsPerIP: 2,
		Channels: map[string]viewers.AccessChannel{open: {Access: "open", MaxViewers: 4}, inv: {Access: "invite"}},
		Codes:    map[string]string{"MK-RH01": codeInvite}}
	s.installAccess(st)
	code, raw := s.pushPlan(viewers.Plan{Version: time.Now().UnixMilli(), Mode: "full"})
	s.must("the edge takes the plan", code == http.StatusOK, code, raw)

	// invites: two devices, then used up
	invA := s.invite("inv-a", 2, inv)
	d1 := s.redeem(invA, "")
	again := s.redeem(invA, d1.Pass)
	d2 := s.redeem(invA, "")
	d3 := s.redeem(invA, "")
	s.check("an invite admits its devices", d1.Code == http.StatusOK && !d1.Reused && d2.Code == http.StatusOK, "invite_uses", d1.Code, d1.Error, d2.Code, d2.Error)
	s.check("a device redeeming again keeps its pass and uses nothing", again.Code == http.StatusOK && again.Reused, "invite_uses", again.Code, again.Error, again.Reused)
	s.check("an invite used up admits no more", d3.Code == http.StatusGone && d3.Error == "invite_used", "invite_uses", d3.Code, d3.Error)
	p1 := s.playOn(inv, d1.Pass)
	s.check("a guest pass plays its channel", p1.Code == http.StatusOK && p1.State == "full", "play", p1.Code, p1.Error)
	other := s.playOn(open, d1.Pass)
	s.check("a guest pass opens only its invite's channels", other.Code == http.StatusForbidden && other.Error == "not_invited", "play", other.Code, other.Error)

	// no credential
	n1 := s.playOn(inv, "")
	n2 := s.playOn(open, "")
	s.check("an invite channel asks for an invite", n1.Code == http.StatusUnauthorized && n1.Error == "invite_required", "access", n1.Code, n1.Error)
	s.check("an open channel asks for Turnstile", n2.Code == http.StatusForbidden && n2.Error == "turnstile_required", "access", n2.Code, n2.Error)

	// short codes: the code redeems its invite; 10 tries a minute per network
	c := s.watch(http.MethodPost, "/api/v1/redeem", map[string]string{"publisher": pub, "code": "MK-RH01"}, "")
	s.check("a short code redeems its invite", c.Code == http.StatusOK && c.Pass != "", "short_code", c.Code, c.Error)
	unknown, limited := 0, 0
	for range 10 {
		w := s.watch(http.MethodPost, "/api/v1/redeem", map[string]string{"publisher": pub, "code": "MK-ZZZZ"}, "")
		switch {
		case w.Code == http.StatusNotFound && w.Error == "code_unknown":
			unknown++
		case w.Code == http.StatusTooManyRequests && w.Error == "rate_limited":
			limited++
		}
	}
	s.check("a network's 11th code try in a minute is refused", unknown == 9 && limited == 1, "short_code_tries",
		fmt.Sprintf("%d unknown, %d rate-limited of 10 wrong tries after one good one", unknown, limited))

	// Turnstile: a bad token fails; a good one is good once
	bad := s.watch(http.MethodPost, "/api/v1/open/turnstile", map[string]string{"token": "robot"}, "")
	s.check("a failed Turnstile check opens nothing", bad.Code == http.StatusForbidden && bad.Error == "turnstile_failed", "turnstile", bad.Code, bad.Error)
	opens := make([]watchAnswer, 0, 5)
	turnstile := func() watchAnswer {
		return s.watch(http.MethodPost, "/api/v1/open/turnstile", map[string]string{"token": "pass-" + newChannel("t")}, "")
	}
	first := s.watch(http.MethodPost, "/api/v1/open/turnstile", map[string]string{"token": "pass-replayed"}, "")
	replay := s.watch(http.MethodPost, "/api/v1/open/turnstile", map[string]string{"token": "pass-replayed"}, "")
	s.check("a Turnstile token opens a session once", first.Code == http.StatusOK && first.Session != "" &&
		replay.Code == http.StatusForbidden && replay.Error == "turnstile_failed", "turnstile", first.Code, first.Error, replay.Code, replay.Error)
	opens = append(opens, first)

	// open sessions: 2 per network, then 4 per channel; the playing ones play on
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ps players
	admit := func(o watchAnswer, name string) watchAnswer {
		a := s.playOn(open, o.Session)
		if a.Code == http.StatusOK {
			ps.start(ctx, s, name, open, a.MasterURL)
		}
		return a
	}
	o2 := turnstile()
	opens = append(opens, o2)
	a1, a2 := admit(first, "open-1"), admit(o2, "open-2")
	o3 := turnstile()
	a3 := admit(o3, "open-3")
	s.check("open sessions play after Turnstile", a1.Code == http.StatusOK && a2.Code == http.StatusOK, "play", a1.Code, a1.Error, a2.Code, a2.Error)
	s.check("a network's third open session is refused (2 per network)", a3.Code == http.StatusTooManyRequests && a3.Error == "broadcast_ip_limit",
		"limit_per_network", a3.Code, a3.Error)
	time.Sleep(10 * time.Second)
	st.Version, st.MaxSessionsPerIP = 2, 10
	s.installAccess(st)
	a3 = admit(o3, "open-3")
	a4 := admit(turnstile(), "open-4")
	a5 := s.playOn(open, turnstile().Session)
	s.check("a raised network limit admits more", a3.Code == http.StatusOK && a4.Code == http.StatusOK, "limit_per_network", a3.Code, a3.Error, a4.Code, a4.Error)
	s.check("a channel's fifth viewer is refused (4 per channel)", a5.Code == http.StatusTooManyRequests && a5.Error == "broadcast_full",
		"limit_per_channel", a5.Code, a5.Error)

	// revocation: a pass, then an invite
	st.Version, st.RevokedPasses = 3, []string{passID(d1.Pass)}
	s.installAccess(st)
	b1 := s.watchBeat(p1)
	r1 := s.playOn(inv, d1.Pass)
	s.check("a revoked pass's session ends at its next heartbeat", b1.Code == http.StatusGone && b1.Error == "revoked", "revoked_pass", b1.Code, b1.Error)
	s.check("a revoked pass plays nothing new", r1.Code == http.StatusForbidden && r1.Error == "pass_revoked", "revoked_pass", r1.Code, r1.Error)
	st.Version, st.RevokedInvites = 4, []string{"inv-a"}
	s.installAccess(st)
	r2 := s.playOn(inv, d2.Pass)
	r3 := s.redeem(invA, "")
	s.check("a revoked invite's passes play nothing new", r2.Code == http.StatusForbidden && r2.Error == "pass_revoked", "revoked_invite", r2.Code, r2.Error)
	s.check("a revoked invite admits no new device", r3.Code == http.StatusGone && r3.Error == "invite_revoked", "revoked_invite", r3.Code, r3.Error)

	// daily minutes: a pass with its minute used gets no new session that day
	st.Version, st.DailyMinutes = 5, 1
	s.installAccess(st)
	dc := s.playOn(inv, c.Pass)
	bc := s.watchBeat(dc)
	dc2 := s.playOn(inv, c.Pass)
	s.check("the daily minutes refuse a new session once used", dc.Code == http.StatusOK && bc.Code == http.StatusOK &&
		dc2.Code == http.StatusTooManyRequests && dc2.Error == "broadcast_daily_limit", "limit_daily", dc.Code, bc.Code, dc2.Code, dc2.Error)

	time.Sleep(20 * time.Second)
	cancel()
	ps.wg.Wait()
	snap := ps.stats()
	sum := viewers.Summarise(snap, nil)
	s.check("the limits stopped no viewer already playing", sum.States[viewers.StateLive] == len(snap) && len(snap) == 4, "sessions_stopped",
		fmt.Sprintf("%d players", len(snap)), sum.States)
	s.check("no player error", sum.Errors == 0, "viewer_errors", sum.Errors, sum.FirstErrors)
	s.r.Latency = Latency(sum)
}

// ---- broadcast-takedown (50.24) ----------------------------------------------------------

// takedown: a valid notice on the item airing now: the cloud pushes a programme entry and every
// viewer has the slate within 60 s while the home keeps uploading; the counter-notice restores
// it (live again); a second strike's slate ends with its slot; the third strike ends
// broadcasting: the publisher's entry takes every viewer off air within 60 s, the home stops,
// and no one new gets in.
func takedown(s *stack) {
	ch := newChannel("takedown")
	s.publish(ch)
	s.waitLive(ch)
	s.installAccess(viewers.AccessState{Version: 1, Channels: map[string]viewers.AccessChannel{ch: {Access: "invite"}}, MaxSessionsPerIP: 3})
	code, raw := s.pushPlan(viewers.Plan{Version: time.Now().UnixMilli(), Mode: "full"})
	s.must("the edge takes the plan", code == http.StatusOK, code, raw)
	inv := s.invite("inv-takedown", 20, ch)
	held := s.redeem(inv, "")
	s.must("a viewer holds a pass", held.Code == http.StatusOK, held.Code, held.Error)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.follow(ctx)
	fl := s.fleet(ch, 5)
	fl.Add(ctx, envInt("BROADCAST_TAKEDOWN_VIEWERS", 20))
	time.Sleep(15 * time.Second)
	snap := func() []viewers.PlayerStats { return fl.Snapshot() }
	rfc := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }

	// 1. the notice accepted: the item airing now is taken down (a programme entry to its end)
	t0 := time.Now()
	s.installList(1, []viewers.RevocationEntry{{ID: "td-1", Kind: "programme", Ref: ch, Action: "slate", Reason: "copyright",
		Since: rfc(t0), Until: rfc(t0.Add(10 * time.Minute))}})
	_, ok := waitFor(75*time.Second, func() bool { _, n := sinceAll(snap(), firstSlate, t0); return n == len(snap()) })
	worst, n := sinceAll(snap(), firstSlate, t0)
	s.timing("slate_after_takedown", worst)
	s.check("every viewer has the slate within 60 s of the takedown", ok && worst <= 60*time.Second, "slate_s_takedown",
		fmt.Sprintf("%d of %d, the last after %s", n, len(snap()), worst.Round(100*time.Millisecond)))
	time.Sleep(10 * time.Second)
	m := s.homeMetrics()
	s.check("the home keeps uploading under a programme's slate", m.State == "live" && s.homePutsAfter(t0.Add(5*time.Second)) > 0, "home_puts_under_slate",
		m.State, m.Detail, s.homePutsAfter(t0.Add(5*time.Second)), "segments stored")

	// 2. the counter-notice: restored
	t1 := time.Now()
	s.installList(2, nil)
	_, ok = waitFor(75*time.Second, func() bool { _, n := sinceAll(snap(), liveAgain, t1); return n == len(snap()) })
	worst, n = sinceAll(snap(), liveAgain, t1)
	s.timing("live_after_restore", worst)
	s.check("every viewer is live again within 60 s of the restore", ok && worst <= 60*time.Second, "live_again_restore",
		fmt.Sprintf("%d of %d, the last after %s", n, len(snap()), worst.Round(100*time.Millisecond)))

	// 3. a second strike: another item's slot slated; the slate ends with the slot
	t2 := time.Now()
	slot := 40 * time.Second
	s.installList(3, []viewers.RevocationEntry{{ID: "td-2", Kind: "programme", Ref: ch, Action: "slate", Reason: "copyright",
		Since: rfc(t2), Until: rfc(t2.Add(slot))}})
	d, ok := waitFor(60*time.Second, func() bool { return s.slateNow(ch) })
	s.check("the second takedown slates its slot", ok, "slate_s_second", d.Round(100*time.Millisecond))
	if wait := slot - time.Since(t2); wait > 0 {
		time.Sleep(wait)
	}
	d, ok = waitFor(60*time.Second, func() bool { return s.liveOnEveryRung(ch) })
	s.timing("live_after_slot", d)
	s.check("live again when the slot ends, with no new list", ok, "live_after_slot", d.Round(100*time.Millisecond))

	// 4. the third strike: broadcasting ends (the publisher's entry)
	t3 := time.Now()
	s.installList(4, []viewers.RevocationEntry{
		{ID: "td-2", Kind: "programme", Ref: ch, Action: "slate", Reason: "copyright", Since: rfc(t2), Until: rfc(t2.Add(slot))},
		{ID: "strike-3", Kind: "publisher", Ref: pub, Action: "terminate", Reason: "repeat_infringer"},
	})
	_, ok = waitFor(75*time.Second, func() bool { _, n := sinceAll(snap(), firstOffAir, t3); return n == len(snap()) })
	worst, n = sinceAll(snap(), firstOffAir, t3)
	s.timing("off_air_after_third_strike", worst)
	s.check("the third strike takes every viewer off air within 60 s", ok && worst <= 60*time.Second, "stop_s_strikes",
		fmt.Sprintf("%d of %d, the last after %s", n, len(snap()), worst.Round(100*time.Millisecond)))
	time.Sleep(10 * time.Second)
	m = s.homeMetrics()
	lastPut := int64(0)
	for _, p := range m.Puts {
		if p.Status == http.StatusCreated && p.Rung != "slate" {
			lastPut = max(lastPut, p.AtMS)
		}
	}
	s.check("the home stops uploading", m.State == "revoked" && lastPut < t3.Add(60*time.Second).UnixMilli(), "home_puts_after_stop",
		fmt.Sprintf("home %s %s, last segment stored %s after the list", m.State, m.Detail, time.UnixMilli(lastPut).Sub(t3).Round(time.Second)))
	np := s.playOn(ch, held.Pass)
	nr := s.redeem(inv, "")
	s.check("no new session on a terminated publisher", np.Code == http.StatusForbidden && np.Error == "broadcast_off", "new_viewers", np.Code, np.Error)
	s.check("no invite redeems for a terminated publisher", nr.Code == http.StatusGone && nr.Error == "invite_revoked", "new_viewers", nr.Code, nr.Error)

	cancel()
	fl.Wait()
	sum := viewers.Summarise(fl.Snapshot(), nil)
	s.check("no player error", sum.Errors == 0, "viewer_errors", sum.Errors, sum.FirstErrors)
	s.r.Latency = Latency(sum)
}

// ---- broadcast-publishable (50.23) -------------------------------------------------------

// publishable: the home schedules a channel from mixed sources (CC BY, a standing permission,
// its own recording, and items with no licence) and publishes it; the guide it sends holds only
// the publishable items and the edge serves exactly that guide to an invited viewer (no item
// added, dropped or reordered, the attribution kept). The standing permission of the item airing
// now is withdrawn: the home's player slates the block (here its test pattern stops) and every
// viewer has the slate; at the top-up the home's new guide replaces the item and the channel is
// live again.
func publishable(s *stack) {
	ch := newChannel("publishable")
	s.publish(ch)
	s.waitLive(ch)
	s.installAccess(viewers.AccessState{Version: 1, Channels: map[string]viewers.AccessChannel{ch: {Access: "invite"}}, MaxSessionsPerIP: 3})
	pass := s.redeem(s.invite("inv-guide", 5, ch), "")
	s.must("an invited viewer holds a pass", pass.Code == http.StatusOK, pass.Code, pass.Error)

	// the schedule: what the home's catalog holds, each with why it may air ("" : it may not)
	type item struct {
		title, source, reason string
		attr                  *attribution
	}
	cc := &attribution{Title: "River Ecology, Lecture 3", By: "Open Lectures", Licence: "CC BY 4.0", URL: "https://example.org/river-ecology"}
	schedule := []item{
		{"Friday talk (standing permission)", "speaker:speaker-a", "standing_permission", nil},
		{"River Ecology, Lecture 3", "cc-by", "cc_by", cc},
		{"Studio film (no licence)", "studio", "", nil},
		{"Our own recording", "own", "own_work", nil},
		{"Concert (no permission)", "label:label-b", "", nil},
	}
	start := time.Now().Add(-2 * time.Minute).Truncate(time.Minute)
	guideOf := func(withdrawn string) (map[string]any, []string) {
		var progs []programme
		var titles []string
		at := start
		for _, it := range schedule {
			if it.reason == "" || it.source == withdrawn {
				continue // the home's API schedules only what may air (50.23 §3)
			}
			progs = append(progs, programme{Start: at.UTC().Format(time.RFC3339), End: at.Add(10 * time.Minute).UTC().Format(time.RFC3339),
				Title: it.title, Attribution: it.attr})
			titles = append(titles, it.title)
			at = at.Add(10 * time.Minute)
		}
		return map[string]any{"schema": 1, "publisher": map[string]any{"name": "Rehearsal"},
			"channel": map[string]any{"name": "Publishable", "number": 7}, "programmes": progs}, titles
	}
	postGuide := func(doc map[string]any) (int, string) {
		tok, err := viewers.IngestToken(s.key, viewers.IngestClaims{Pub: pub, Channel: ch, Home: pub, Rungs: rungs, Kbps: 10000, IssuedAt: time.Now()})
		s.must("an ingest token signed as the cloud", err == nil, err)
		body, _ := json.Marshal(doc)
		code, raw, err := s.do(http.MethodPost, s.edge+"/v1/ingest/"+ch+"/guide", body, map[string]string{
			"Authorization": "Bearer " + tok, "Content-Type": "application/json", "X-Maktaba-Time": fmt.Sprint(time.Now().UnixMilli())})
		if err != nil {
			return 0, err.Error()
		}
		return code, string(raw)
	}
	served := func() ([]string, watchAnswer) {
		g := s.watch(http.MethodGet, "/api/v1/guide?publisher="+pub+"&channel="+ch, nil, pass.Pass)
		var titles []string
		for _, p := range g.Programmes {
			titles = append(titles, p.Title)
		}
		return titles, g
	}
	notPublishable := func(titles []string) []string {
		var bad []string
		for _, t := range titles {
			for _, it := range schedule {
				if it.title == t && it.reason == "" {
					bad = append(bad, t)
				}
			}
		}
		return bad
	}

	doc, want := guideOf("")
	code, raw := postGuide(doc)
	s.must("the home's guide is stored", code == http.StatusOK, code, raw)
	got, g := served()
	s.check("the guide holds only the publishable items", len(notPublishable(got)) == 0 && len(got) > 0, "guide_publishable", got)
	s.check("the edge serves the home's guide as sent (nothing added, dropped or reordered)", strings.Join(got, "|") == strings.Join(want, "|"),
		"guide_as_sent", fmt.Sprintf("got %q, sent %q", got, want))
	var kept *attribution
	for _, p := range g.Programmes {
		if p.Title == cc.Title {
			kept = p.Attribution
		}
	}
	s.check("the CC BY item keeps its attribution", kept != nil && *kept == *cc, "guide_attribution", kept)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.follow(ctx)
	fl := s.fleet(ch, 5)
	fl.Add(ctx, envInt("BROADCAST_PUBLISHABLE_VIEWERS", 20))
	time.Sleep(15 * time.Second)

	// the standing permission of the item airing now is withdrawn
	t0 := time.Now()
	_, _, _ = s.do(http.MethodPost, s.home+"/encoder/stop", nil, nil)
	// as broadcast-home: the playlist has the slate within 15 s; each player then has it at its
	// next reload (a target duration later)
	slate, ok := waitFor(40*time.Second, func() bool { return s.slateNow(ch) })
	s.timing("slate_after_withdrawal", slate)
	s.check("the slate within 15 s of the withdrawal", ok && slate <= 15*time.Second, "slate_s_withdrawal", slate.Round(100*time.Millisecond))
	_, ok = waitFor(30*time.Second, func() bool { _, n := sinceAll(fl.Snapshot(), firstSlate, t0); return n == len(fl.Snapshot()) })
	worst, n := sinceAll(fl.Snapshot(), firstSlate, t0)
	s.timing("players_slate_after_withdrawal", worst)
	s.check("every viewer has the slate", ok, "players_slate",
		fmt.Sprintf("%d of %d, the last after %s", n, len(fl.Snapshot()), worst.Round(100*time.Millisecond)))
	time.Sleep(10 * time.Second)

	// the top-up: the home's new schedule replaces the item
	t1 := time.Now()
	doc, want = guideOf("speaker:speaker-a")
	code, raw = postGuide(doc)
	s.check("the top-up's guide is stored", code == http.StatusOK, "guide_topup", code, raw)
	_, _, _ = s.do(http.MethodPost, s.home+"/encoder/start", nil, nil)
	_, ok = waitFor(90*time.Second, func() bool { _, n := sinceAll(fl.Snapshot(), liveAgain, t1); return n == len(fl.Snapshot()) })
	worst, n = sinceAll(fl.Snapshot(), liveAgain, t1)
	s.timing("live_after_topup", worst)
	s.check("every viewer is live again after the top-up", ok, "live_again_topup",
		fmt.Sprintf("%d of %d, the last after %s", n, len(fl.Snapshot()), worst.Round(100*time.Millisecond)))
	got, _ = served()
	withdrawn := false
	for _, t := range got {
		withdrawn = withdrawn || t == schedule[0].title
	}
	s.check("the replaced guide holds neither the withdrawn item nor any unpublishable one",
		!withdrawn && len(notPublishable(got)) == 0 && strings.Join(got, "|") == strings.Join(want, "|"), "guide_topup", got)

	cancel()
	fl.Wait()
	sum := viewers.Summarise(fl.Snapshot(), nil)
	s.check("no player error", sum.Errors == 0, "viewer_errors", sum.Errors, sum.FirstErrors)
	s.r.Latency = Latency(sum)
}

// ---- broadcast-hetzner-down (14.8) -------------------------------------------------------

// usagePost is one post the fake cloud saw; cloudUsage is all it saw and took.
type usagePost struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	Body      string `json:"body"`
	Time      string `json:"time"`
	Signature string `json:"signature"`
}

type usageWindow struct {
	Key           string `json:"key"`
	Publisher     string `json:"publisher"`
	WindowStart   string `json:"window_start"`
	WindowEnd     string `json:"window_end"`
	MinutesFull   int    `json:"minutes_full"`
	MinutesLowest int    `json:"minutes_lowest"`
	SegmentsIn    int    `json:"segments_in"`
	PeakSessions  int    `json:"peak_sessions"`
}

type cloudUsage struct {
	Down    bool                   `json:"down"`
	Refused int                    `json:"refused"`
	Posts   []usagePost            `json:"posts"`
	Windows map[string]usageWindow `json:"windows"`
}

func (s *stack) cloud(action string) cloudUsage {
	method := http.MethodPost
	if action == "usage" {
		method = http.MethodGet
	}
	var u cloudUsage
	code, raw, err := s.do(method, s.edge+"/__rehearsal/cloud/"+action, nil, nil)
	if err != nil || code != http.StatusOK || json.Unmarshal(raw, &u) != nil {
		s.t.Logf("fake cloud %s: %d %v", action, code, err)
	}
	return u
}

func minutesTaken(u cloudUsage) (int, int) {
	n, segs := 0, 0
	for _, w := range u.Windows {
		if w.Publisher == pub {
			n += w.MinutesFull + w.MinutesLowest
			segs += w.SegmentsIn
		}
	}
	return n, segs
}

// hetznerDown: the cloud stack (the Hetzner pair) goes down mid-broadcast: the channel plays on
// from the edge with no viewer error, the edge's usage posts are refused and kept; when the
// cloud is back every 5-minute window arrives, each once, its viewer-minutes exactly the
// heartbeats the edge counted, every post signed with the edge's key.
func hetznerDown(s *stack) {
	outage := time.Duration(envInt("BROADCAST_OUTAGE_SECONDS", 900)) * time.Second
	ch := newChannel("hetzner")
	s.publish(ch)
	s.waitLive(ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.follow(ctx)
	fl := s.fleet(ch, 5)
	fl.Add(ctx, envInt("BROADCAST_HETZNER_VIEWERS", 20))
	time.Sleep(15 * time.Second)

	t0 := time.Now()
	s.must("the cloud goes down", s.cloud("down").Down)
	before := fl.Snapshot()
	time.Sleep(outage)
	during := viewers.Summarise(fl.Snapshot(), before)
	u := s.cloud("usage")
	s.check("the channel plays on with the cloud down", during.Errors == 0 && during.States[viewers.StateLive] == during.Viewers && during.Segments > 0,
		"viewer_errors", fmt.Sprintf("%d segments, %d errors over %s", during.Segments, during.Errors, outage), during.States, during.FirstErrors)
	s.check("the home uploads on with the cloud down", s.homePutsAfter(t0.Add(outage/2)) > 0, "home_puts", s.homePutsAfter(t0.Add(outage/2)))
	s.check("the edge's usage posts were refused and kept while the cloud was down", u.Refused > 0, "usage_refused",
		u.Refused, "refused posts")
	s.timing("outage", time.Since(t0))

	s.cloud("up")
	t1 := time.Now()
	cancel()
	fl.Wait()
	// every window with a heartbeat closes within 5 minutes; the edge posts every 5 minutes
	var counted, taken int
	d, ok := waitFor(12*time.Minute, func() bool {
		counted = aeTotal(s.counters(), "beat")
		taken, _ = minutesTaken(s.cloud("usage"))
		return counted > 0 && taken == counted
	})
	s.timing("metering_after_up", d)
	u = s.cloud("usage")
	_, segs := minutesTaken(u)
	s.check("the metering arrives exact once the cloud is back", ok, "metering_exact",
		fmt.Sprintf("%d viewer-minutes taken, %d heartbeats counted at the edge, %s after the cloud came back", taken, counted, time.Since(t1).Round(time.Second)))
	s.check("the windows carry the home's segments", segs > 0, "metering_segments", segs)
	pubKey, err := viewers.EdgePublicKey(viewers.RehearsalEdgeSeed)
	s.must("the edge's public key", err == nil, err)
	bad, keys, dup := 0, map[string]int{}, 0
	for _, p := range u.Posts {
		if !viewers.VerifyEdgePost(pubKey, p.Method, p.Path, p.Time, p.Signature, []byte(p.Body)) {
			bad++
		}
		var post struct {
			Windows []usageWindow `json:"windows"`
		}
		_ = json.Unmarshal([]byte(p.Body), &post)
		for _, w := range post.Windows {
			keys[w.Key]++
		}
	}
	var ks []string
	for k, n := range keys {
		if n > 1 {
			dup++
		}
		ks = append(ks, k)
	}
	sort.Strings(ks)
	s.check("every usage post is signed with the edge's key", bad == 0 && len(u.Posts) > 0, "usage_signature",
		fmt.Sprintf("%d of %d posts fail", bad, len(u.Posts)))
	s.check("each window was taken once", dup == 0 && len(u.Windows) == len(keys), "usage_windows",
		fmt.Sprintf("%d windows taken, %d posted more than once: %v", len(u.Windows), dup, ks))
	s.r.Latency = Latency(viewers.Summarise(fl.Snapshot(), nil))
}

// ---- broadcast-observe (50.28) -----------------------------------------------------------

// observe: the edge's part of 50.28 runs here: the canary publisher's ingest events carry
// glass-to-glass, ingest events stop when ingest breaks, and a revoked channel's viewers are
// written as blocked. The alerts (Prometheus over the cloud worker's Analytics Engine poll) and
// the status component are the cloud's monitoring, not in this stack, and the canary viewer probe
// is not built: those checks fail, so the scenario stays an expected failure until they are.
func observe(s *stack) {
	s.deploy(map[string]string{"CANARY_PUBLISHER": pub})
	ch := newChannel("observe")
	s.publish(ch)
	s.waitLive(ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fl := s.fleet(ch, 5)
	fl.Add(ctx, 10)
	time.Sleep(10 * time.Second)

	c0 := s.counters()
	time.Sleep(20 * time.Second)
	c1 := s.counters()
	glass := func(c edgeCounters) int {
		n := 0
		for _, w := range c.Workers {
			n += w.AEIngestGlass
		}
		return n
	}
	in := aeTotal(c1, "ingest") - aeTotal(c0, "ingest")
	g := glass(c1) - glass(c0)
	s.check("ingest events while the home sends", in > 0, "ae_ingest", in)
	// fails today: the home's PROGRAM-DATE-TIME (ffmpeg's program_date_time) is at or after the
	// edge's receive time for most segments, so glass-to-glass clamps to 0 (an open finding)
	s.check("the canary's ingest events carry glass-to-glass", g > 0 && g == in, "ae_glass_to_glass", fmt.Sprintf("%d of %d", g, in))
	plays := aeTotal(c1, "play")
	s.check("each viewer's first heartbeat is a play event", plays >= 10, "ae_play", plays)

	// ingest breaks: the events stop
	_, _, _ = s.do(http.MethodPost, s.home+"/encoder/stop", nil, nil)
	time.Sleep(15 * time.Second)
	c2 := s.counters()
	time.Sleep(15 * time.Second)
	c3 := s.counters()
	stalled := aeTotal(c3, "ingest") - aeTotal(c2, "ingest")
	s.check("ingest events stop when ingest breaks", stalled == 0, "ae_ingest_broken", stalled)
	s.check("the viewers have the slate", s.slateNow(ch), "slate", "")

	// a revoked channel: its viewers are written as blocked
	blocked := func(c edgeCounters) int {
		n := 0
		for _, w := range c.Workers {
			n += w.AEByCode["blocked:revoked"]
		}
		return n
	}
	b0 := blocked(s.counters())
	s.installList(1, []viewers.RevocationEntry{{ID: "obs-1", Kind: "channel", Ref: ch, Action: "off_air", Reason: "notice"}})
	time.Sleep(30 * time.Second)
	s.check("a revoked channel's viewers are written as blocked", blocked(s.counters()) > b0, "ae_blocked", blocked(s.counters())-b0)

	// the cloud's monitoring: not in this stack
	s.check("the alerts fire (broadcast_canary_down, broadcast_ingest_lag_high, broadcast_revocation_list_stale)", false, "alerts",
		"not in this stack: 50.10's Prometheus over the cloud worker's Analytics Engine poll, and the canary probe (deploy/cloud-relay/probes/broadcast_probe) is not built")
	s.check("the status component changes", false, "status_component", "not in this stack: 50.11's status component")
	cancel()
	fl.Wait()
}
