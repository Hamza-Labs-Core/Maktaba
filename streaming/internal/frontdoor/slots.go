package frontdoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
)

// Slots is the front door's routing to its host's TV slots (Epic 33 story 33.1,
// design §2.4): the channels placed on this host are served by the slot whose
// role is live; the hot standby runs the same release, warm on the channels
// people watch (rules.tv_staging.standby_warm, within the encoder budget), and
// becomes live the moment the live slot stops answering — every channel at
// once, a new routing epoch: HLS continues across one discontinuity (the
// stitcher), TS clients and the push relays are spliced onto the standby's TS
// (Retimer). The crashed instance comes back (the supervisor restarts it) as the
// new standby. A crash never promotes a staged change: the standby always runs
// the live release, and staging is never a failover target.
type Slots struct {
	// Host is this host's id (tv_slots.host_id).
	Host string
	// Addrs: instance -> host:port (loopback).
	Addrs  map[string]string
	Pool   *pgxpool.Pool
	Client *http.Client
	Logger *slog.Logger
	// Settings: rules.tv_staging (nil: the defaults).
	Settings func(ctx context.Context) StagingSettings
	// Budget is the host's encoder budget: channels on air at once (with the
	// admin's override) and the NVENC / VideoToolbox session caps (0: none).
	Budget func(ctx context.Context) (capacity, nvencMax, vtMax int)
	// Demand lists the channels with demand (the front door's tracker).
	Demand func() []ChannelDemand
	// RemoteViewers: remote (Cloudflare) viewers per channel, to rank pushes
	// (nil: none).
	RemoteViewers func(ctx context.Context) map[uuid.UUID]int
	// Supervisor is the supervisor's admin base URL ("" : none): the front door
	// asks it to start or stop the standby (hot_standby) and the staging slot.
	Supervisor string
	// OnSwitch runs after the live slot changed (relays are kicked).
	OnSwitch func(from, to string, epoch int64)
	// ProbeEvery is the health probe period (default 500 ms); FailAfter the
	// failed probes in a row that end a live slot (default 2).
	ProbeEvery time.Duration
	FailAfter  int
	// StreamSample is how much of a staging channel's live.ts a sample reads
	// (0: 3 s; tests shorten it).
	StreamSample time.Duration
	// OnEvent receives the failover and staging crash-loop events for the bell
	// (story 33.13 AC1; nil: they are written to tv_slot_events when Pool is set,
	// where the API's TV change controller turns them into notices).
	OnEvent func(ctx context.Context, e SlotEvent)

	clientOnce sync.Once
	mu         sync.Mutex
	live       string
	standby    string
	staging    string
	epoch      int64
	health     map[string]*slotHealth
	failing    map[string]bool // in a failover right now
	switches   int
	lastWarm   time.Time
	wantStby   int // 0: not asked yet, 1: started, -1: stopped (supervisor)
	// story 33.7: the swap or rollback running now (swap.go), and the old live
	// slot kept warm after a swap until previousUntil (the warm rollback target)
	op            *swapOp
	previous      string
	previousUntil time.Time
	boundAt       map[string]time.Time
	// rolesDirty: the last write of the roles to tv_slots failed; Run writes
	// them again until it succeeds (a crashed slot must not stay "live" there:
	// on its restart readRole would make it live again)
	rolesDirty bool
	stage      *stagingRun
	lastSample time.Time
}

// slotHealth is what the last probes of a slot said.
type slotHealth struct {
	ok       bool
	fails    int
	at       time.Time
	status   slot.Status
	upAt     time.Time // first good probe after a bad one
	reported bool      // an exit report from the supervisor
	// staging crash loop (story 33.13 AC1): the process id the last good probe
	// saw, the restarts within crashLoopWindow, and whether the loop was reported
	pid      int
	restarts []time.Time
	looped   bool
}

// SlotEvent is a front-door event for the bell (tv_slot_events, slot 0137).
type SlotEvent struct {
	Kind   string
	Slot   string
	Detail map[string]any
}

// Slot event kinds.
const (
	SlotEventFailover  = "failover"
	SlotEventCrashLoop = "crash_loop"
)

// A staging slot restarted crashLoopRestarts times within crashLoopWindow is in a
// crash loop (the verification's own bound is the same three restarts).
const (
	crashLoopRestarts = 3
	crashLoopWindow   = 10 * time.Minute
)

// emit hands an event to OnEvent, or writes it to tv_slot_events (best effort,
// off the probe path).
func (s *Slots) emit(ctx context.Context, e SlotEvent) {
	if s.OnEvent != nil {
		s.OnEvent(ctx, e)
		return
	}
	if s.Pool == nil {
		return
	}
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	blob, _ := json.Marshal(detail)
	go func() {
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := s.Pool.Exec(qctx, `INSERT INTO tv_slot_events (host_id, kind, slot, detail) VALUES ($1, $2, $3, $4::jsonb)`,
			s.Host, e.Kind, e.Slot, string(blob)); err != nil {
			s.log().Warn("tv: the slot event was not written", "kind", e.Kind, "slot", e.Slot, "err", err.Error(),
				"event", "tv_slot_event_failed")
		}
	}()
}

// noteRestart records a restart of the staging slot and reports a crash loop once
// (until the restarts within the window drop below the bound again). Under s.mu;
// returns the event to emit after unlocking.
func (s *Slots) noteRestartLocked(h *slotHealth, name string, now time.Time) *SlotEvent {
	h.restarts = append(h.restarts, now)
	keep := h.restarts[:0]
	for _, t := range h.restarts {
		if now.Sub(t) <= crashLoopWindow {
			keep = append(keep, t)
		}
	}
	h.restarts = keep
	if len(h.restarts) < crashLoopRestarts {
		h.looped = false
		return nil
	}
	if h.looped {
		return nil
	}
	h.looped = true
	s.log().Warn("tv: the staging slot keeps crashing", "slot", name, "restarts", len(h.restarts),
		"window", crashLoopWindow.String(), "event", "tv_slot_crash_loop")
	return &SlotEvent{Kind: SlotEventCrashLoop, Slot: name, Detail: map[string]any{
		"restarts": len(h.restarts), "window_sec": int(crashLoopWindow.Seconds())}}
}

// ErrNoLiveSlot: no slot answers for the host's channels.
var ErrNoLiveSlot = errors.New("frontdoor: no live TV slot")

func (s *Slots) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Slots) client() *http.Client {
	s.clientOnce.Do(func() {
		if s.Client == nil {
			s.Client = NewClient()
		}
	})
	return s.Client
}

func (s *Slots) settings(ctx context.Context) StagingSettings {
	if s.Settings == nil {
		return DefaultStagingSettings()
	}
	return s.Settings(ctx)
}

func (s *Slots) probeEvery() time.Duration {
	if s.ProbeEvery > 0 {
		return s.ProbeEvery
	}
	return 500 * time.Millisecond
}

func (s *Slots) failAfter() int {
	if s.FailAfter > 0 {
		return s.FailAfter
	}
	return 2
}

// Base is an instance's base URL ("" when unknown).
func (s *Slots) Base(name string) string {
	if a := s.Addrs[name]; a != "" {
		return "http://" + a
	}
	return ""
}

// Live is the live slot and the routing epoch.
func (s *Slots) Live() (string, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live, s.epoch
}

// Roles is the current routing (live, standby, staging, epoch).
func (s *Slots) Roles() (live, standby, staging string, epoch int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live, s.standby, s.staging, s.epoch
}

// Switches is how many times the live slot changed.
func (s *Slots) Switches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.switches
}

// Bootstrap adopts the host's roles from tv_slots, or — a first start (EC2) —
// makes blue live and green the standby under epoch 1.
func (s *Slots) Bootstrap(ctx context.Context) error {
	s.mu.Lock()
	if s.health == nil {
		s.health = map[string]*slotHealth{}
		s.failing = map[string]bool{}
	}
	s.mu.Unlock()
	roles := map[string]string{}
	var epoch int64
	if s.Pool != nil {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		rows, err := s.Pool.Query(qctx, `SELECT slot, role, role_epoch FROM tv_slots WHERE host_id = $1`, s.Host)
		if err == nil {
			for rows.Next() {
				var name, role string
				var e int64
				if rows.Scan(&name, &role, &e) == nil {
					roles[name] = role
					if e > epoch {
						epoch = e
					}
				}
			}
			rows.Close()
			err = rows.Err()
		}
		cancel()
		if err != nil {
			return err
		}
	}
	live, standby, staging, previous := "", "", "", ""
	for name, role := range roles {
		if _, ok := s.Addrs[name]; !ok {
			continue
		}
		switch role {
		case slot.RoleLive:
			live = name
		case slot.RoleStandby:
			standby = name
		case slot.RoleStaging:
			staging = name
		case slot.RolePrevious:
			previous = name
		}
	}
	fresh := live == ""
	if fresh {
		live = slot.Blue
		if _, ok := s.Addrs[live]; !ok {
			for _, n := range slot.Names {
				if _, ok := s.Addrs[n]; ok {
					live = n
					break
				}
			}
		}
		epoch++
	}
	var until time.Time
	if previous != "" && s.Pool != nil {
		// a front door restarted inside a swap's grace keeps the previous slot
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		var t *time.Time
		if s.Pool.QueryRow(qctx, `SELECT previous_until FROM tv_routing WHERE scope = 'cluster'`).Scan(&t) == nil && t != nil {
			until = *t
		}
		cancel()
	}
	if previous != "" && !until.After(time.Now()) {
		previous = ""
	}
	if (standby == "" && previous == "") || standby == live {
		standby = s.otherThan(live, staging)
		if standby == previous {
			standby = ""
		}
	}
	s.mu.Lock()
	s.live, s.standby, s.staging, s.epoch = live, standby, staging, epoch
	s.previous, s.previousUntil = previous, until
	s.mu.Unlock()
	if fresh {
		s.log().Info("tv: slots set up", "live", live, "standby", standby, "epoch", epoch, "event", "tv_slot_bootstrap")
	}
	return s.persist(ctx)
}

// otherThan is an instance that is neither a nor b.
func (s *Slots) otherThan(a, b string) string {
	for _, n := range slot.Names {
		if _, ok := s.Addrs[n]; ok && n != a && n != b {
			return n
		}
	}
	return ""
}

// persist writes the roles under the host's advisory lock; a newer epoch in the
// table (another front door instance moved on) is adopted instead.
func (s *Slots) persist(ctx context.Context) error {
	if s.Pool == nil {
		return nil
	}
	err := s.persistOnce(ctx)
	s.mu.Lock()
	// a newer epoch in the table is another front door's: nothing to retry
	s.rolesDirty = err != nil && !errors.Is(err, errRolesStale)
	s.mu.Unlock()
	return err
}

// errRolesStale: tv_slots has a newer routing epoch than this front door.
var errRolesStale = errors.New("frontdoor: tv_slots has a newer routing epoch")

// persistDirty writes the roles again when the last write failed.
func (s *Slots) persistDirty(ctx context.Context) {
	s.mu.Lock()
	dirty := s.rolesDirty
	s.mu.Unlock()
	if !dirty {
		return
	}
	if err := s.persist(ctx); err != nil {
		s.log().Warn("tv: writing the slot roles failed again", "err", err.Error(), "event", "tv_slot_roles_failed")
		return
	}
	s.log().Info("tv: the slot roles were written", "event", "tv_slot_roles_written")
}

func (s *Slots) persistOnce(ctx context.Context) error {
	live, standby, staging, epoch := s.Roles()
	previous, _ := s.Previous()
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(qctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(qctx) }()
	if _, err := tx.Exec(qctx, `SELECT pg_advisory_xact_lock(hashtext('tv_slots:' || $1))`, s.Host); err != nil {
		return err
	}
	var cur int64
	if err := tx.QueryRow(qctx, `SELECT COALESCE(max(role_epoch), 0) FROM tv_slots WHERE host_id = $1`, s.Host).Scan(&cur); err != nil {
		return err
	}
	if cur > epoch {
		return fmt.Errorf("%w: %d > %d", errRolesStale, cur, epoch)
	}
	if _, err := tx.Exec(qctx, `UPDATE tv_slots SET role = 'idle' WHERE host_id = $1 AND role = 'live'`, s.Host); err != nil {
		return err
	}
	for _, n := range slot.Names {
		if _, ok := s.Addrs[n]; !ok {
			continue
		}
		role := slot.RoleIdle
		switch n {
		case live:
			role = slot.RoleLive
		case standby:
			role = slot.RoleStandby
		case staging:
			role = slot.RoleStaging
		case previous:
			role = slot.RolePrevious
		}
		if _, err := tx.Exec(qctx, `INSERT INTO tv_slots (host_id, slot, role, role_epoch, base_url)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (host_id, slot) DO UPDATE SET role = EXCLUDED.role, role_epoch = EXCLUDED.role_epoch`,
			s.Host, n, role, epoch, s.Base(n)); err != nil {
			return err
		}
	}
	return tx.Commit(qctx)
}

// Run probes the slots, fails over and keeps the warm sets until ctx ends.
func (s *Slots) Run(ctx context.Context) {
	t := time.NewTicker(s.probeEvery())
	defer t.Stop()
	for {
		s.probeAll(ctx)
		s.persistDirty(ctx)
		if time.Since(s.lastWarmAt()) >= 2*time.Second {
			s.SyncWarm(ctx)
		}
		s.mu.Lock()
		due := s.stage != nil && time.Since(s.lastSample) >= stagingSampleEvery
		if due {
			s.lastSample = time.Now()
		}
		s.mu.Unlock()
		if due {
			go s.sampleStaging(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Slots) lastWarmAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastWarm
}

// probe asks one slot for its status.
func (s *Slots) probe(ctx context.Context, name string) (slot.Status, error) {
	base := s.Base(name)
	if base == "" {
		return slot.Status{}, errors.New("frontdoor: unknown slot " + name)
	}
	pctx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, base+"/internal/tv/slot/status", nil)
	if err != nil {
		return slot.Status{}, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return slot.Status{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return slot.Status{}, fmt.Errorf("frontdoor: slot %s answered %s", name, resp.Status)
	}
	var st slot.Status
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&st); err != nil {
		return slot.Status{}, err
	}
	return st, nil
}

func (s *Slots) probeAll(ctx context.Context) {
	live, standby, staging, _ := s.Roles()
	previous, _ := s.Previous()
	_, _, _, to := s.Operation()
	seen := map[string]bool{}
	for _, n := range []string{live, standby, staging, previous, to} {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		s.check(ctx, n)
	}
}

// check probes a slot and fails over when it is the live one and gone.
func (s *Slots) check(ctx context.Context, name string) bool {
	st, err := s.probe(ctx, name)
	now := time.Now()
	s.mu.Lock()
	h := s.health[name]
	if h == nil {
		h = &slotHealth{}
		s.health[name] = h
	}
	var loop *SlotEvent
	if err == nil {
		if !h.ok {
			h.upAt = now
		}
		if name == s.staging {
			// a new process id without an exit report is a restart the supervisor
			// did not tell (an exit report already counted it)
			if h.pid != 0 && st.PID != 0 && st.PID != h.pid && !h.reported {
				loop = s.noteRestartLocked(h, name, now)
			}
		} else {
			h.restarts, h.looped = nil, false
		}
		if st.PID != 0 {
			h.pid = st.PID
		}
		h.ok, h.fails, h.status, h.reported = true, 0, st, false
	} else {
		h.ok = false
		h.fails++
	}
	h.at = now
	fails, reported := h.fails, h.reported
	isLive := name == s.live || (s.op != nil && name == s.op.To)
	s.mu.Unlock()
	if loop != nil {
		s.emit(ctx, *loop)
	}
	if err != nil && isLive && (fails >= s.failAfter() || reported) {
		s.Failover(ctx, name, "probe: "+err.Error())
	}
	if err == nil && st.Release == "" {
		s.ensureBound(ctx, name)
	}
	return err == nil
}

// Suspect re-probes a slot at once (a proxied request to it failed).
func (s *Slots) Suspect(ctx context.Context, name string) {
	if s.check(context.WithoutCancel(ctx), name) {
		return
	}
	live, _ := s.Live()
	if _, _, _, to := s.Operation(); name == live || name == to {
		// a refused connection is a dead process: no second probe
		if _, err := s.probe(context.WithoutCancel(ctx), name); err != nil {
			s.Failover(ctx, name, "upstream: "+err.Error())
		}
	}
}

// Exited is the supervisor's report that a slot process ended.
func (s *Slots) Exited(ctx context.Context, name string) {
	s.mu.Lock()
	var loop *SlotEvent
	h := s.health[name]
	if h == nil && name != "" && name == s.staging {
		h = &slotHealth{}
		s.health[name] = h
	}
	if h != nil {
		h.ok, h.reported = false, true
		if name == s.staging {
			loop = s.noteRestartLocked(h, name, time.Now())
		}
	}
	isLive := name == s.live || (s.op != nil && name == s.op.To)
	s.mu.Unlock()
	if loop != nil {
		s.emit(ctx, *loop)
	}
	if isLive {
		s.Failover(ctx, name, "exited")
	}
}

// Failover makes the hot standby live in place of from (it must still be the
// live slot), under a new routing epoch. The standby must have answered its
// last probe; without one the routing stays and the supervisor's restart of
// the live slot is awaited (viewers stall for a moment, TS connections are
// kept).
func (s *Slots) Failover(ctx context.Context, from, why string) bool {
	// story 33.7/33.9: a slot lost while a swap or rollback runs, or the new
	// live slot lost inside the grace (automatic rollback to the previous slot;
	// never a staged change: the staging slot is no failover target)
	if s.failoverInOp(ctx, from, why) {
		return true
	}
	if s.autoRollback(ctx, from, why) {
		return true
	}
	s.mu.Lock()
	if s.live != from || s.failing[from] {
		s.mu.Unlock()
		return false
	}
	to := s.standby
	h := s.health[to]
	if to == "" || h == nil || !h.ok {
		s.mu.Unlock()
		s.log().Warn("tv: the live slot is down and no hot standby answers; waiting for its restart",
			"slot", from, "why", why, "event", "tv_slot_failover_unavailable")
		return false
	}
	if ok, liveRel := standbyAirsLive(s.health[from], h); !ok {
		s.mu.Unlock()
		s.log().Warn("tv: the live slot is down and the hot standby does not run the live release; waiting for its restart",
			"slot", from, "standby", to, "live_release", liveRel, "standby_release", h.status.Release,
			"mismatch", h.status.Mismatch, "why", why, "event", "tv_slot_failover_refused")
		return false
	}
	s.failing[from] = true
	s.epoch++
	epoch := s.epoch
	s.live, s.standby = to, from
	s.switches++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.failing, from)
		s.mu.Unlock()
	}()
	s.log().Warn("tv: live slot lost; the hot standby is live", "from", from, "to", to, "epoch", epoch,
		"why", why, "event", "tv_slot_failover")
	if s.OnSwitch != nil {
		s.OnSwitch(from, to, epoch)
	}
	s.emit(ctx, SlotEvent{Kind: SlotEventFailover, Slot: from, Detail: map[string]any{"to": to, "epoch": epoch, "why": why}})
	go func() {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.persist(pctx); err != nil {
			s.log().Warn("tv: writing the slot roles failed", "err", err.Error(), "event", "tv_slot_roles_failed")
		}
	}()
	s.SyncWarm(context.WithoutCancel(ctx))
	return true
}

// Switch makes slot to live under epoch (the swap and rollback of story 33.7
// call it with the epoch they committed; a failover uses Failover). The old
// live slot becomes the standby. An epoch not above the current one is refused.
func (s *Slots) Switch(ctx context.Context, to string, epoch int64) error {
	if _, ok := s.Addrs[to]; !ok {
		return fmt.Errorf("frontdoor: unknown slot %q", to)
	}
	if err := s.opRunning(); err != nil {
		return err
	}
	s.mu.Lock()
	if epoch <= s.epoch {
		cur := s.epoch
		s.mu.Unlock()
		return fmt.Errorf("frontdoor: epoch %d is not above the current %d", epoch, cur)
	}
	from := s.live
	s.epoch = epoch
	if to != from {
		s.live = to
		if s.standby == to {
			s.standby = from
		}
		if s.staging == to {
			s.staging = ""
			s.stage = nil // the staged release is live now (story 33.7)
		}
		s.switches++
	}
	s.mu.Unlock()
	if to != from && s.OnSwitch != nil {
		s.OnSwitch(from, to, epoch)
	}
	err := s.persist(ctx)
	s.SyncWarm(ctx)
	return err
}

// StartStaging gives the third instance the staging role and asks the
// supervisor to start it (only while staging is on and built in).
func (s *Slots) StartStaging(ctx context.Context) (string, error) {
	if !s.settings(ctx).StagingActive() {
		return "", errors.New("frontdoor: staging is off (rules.tv_staging.enabled, or not in this build)")
	}
	s.mu.Lock()
	if s.staging == "" {
		s.staging = s.freeInstanceLocked()
	}
	name := s.staging
	s.mu.Unlock()
	if name == "" {
		return "", errors.New("frontdoor: no free slot instance for staging")
	}
	s.supervise(ctx, name, true)
	return name, s.persist(ctx)
}

// StopStaging retires the staging slot.
func (s *Slots) StopStaging(ctx context.Context) error {
	s.mu.Lock()
	name := s.staging
	s.staging = ""
	s.stage = nil
	s.mu.Unlock()
	if name == "" {
		return nil
	}
	s.supervise(ctx, name, false)
	return s.persist(ctx)
}

// standbyAirsLive reports whether a standby may take over from the live slot:
// it is not refusing its release (Mismatch) and, when the live slot was bound
// to a release, it is bound to the same one — never an older (or staged)
// release. An unbound live slot (no release yet) accepts an unbound standby.
func standbyAirsLive(live, standby *slotHealth) (bool, string) {
	if standby == nil || standby.status.Mismatch {
		return false, ""
	}
	liveRel := ""
	if live != nil {
		liveRel = live.status.Release
	}
	if liveRel == "" {
		return true, ""
	}
	return standby.status.Release == liveRel, liveRel
}

// supervise asks the supervisor to start or stop an instance. A start always
// carries the instance's binding (the supervisor keeps an instance's last
// binding otherwise: a former staging slot would start its staged release as
// the standby): the standby is bound to the live slot's release, any other
// start is unbound (it binds itself to the live release).
func (s *Slots) supervise(ctx context.Context, name string, start bool) {
	if s.Supervisor == "" {
		return
	}
	if start {
		s.mu.Lock()
		rel := ""
		if name == s.standby {
			if h := s.health[s.live]; h != nil {
				rel = h.status.Release
			}
		}
		s.mu.Unlock()
		s.superviseRelease(ctx, name, rel)
		return
	}
	verb := "stop"
	if start {
		verb = "start"
	}
	sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sctx, http.MethodPost,
		strings.TrimRight(s.Supervisor, "/")+"/slots/"+name+"/"+verb, nil)
	if err != nil {
		return
	}
	resp, err := s.client().Do(req)
	if err != nil {
		s.log().Warn("tv: the supervisor did not take the slot request", "slot", name, "action", verb,
			"err", err.Error(), "event", "tv_slot_supervisor_failed")
		return
	}
	_ = resp.Body.Close()
}

// warmPlan is what SyncWarm sends each slot.
type warmPlan struct {
	live, standby        []uuid.UUID
	liveBudget, stBudget slot.Budget
}

// planWarm computes the warm sets (design §14): live first — every channel with
// demand, within the host's capacity; then the standby — what the budget leaves
// (capacity minus live's stations), on the channels live airs: with
// standby_warm = viewers the channels with human viewers first, then the pushes
// by remote viewers; with demand every channel with demand. Live never loses a
// session to the standby: the standby's NVENC share is what live's stations
// leave (two sessions per channel: a block's pre-start holds two for a moment).
func planWarm(set StagingSettings, demands []ChannelDemand, remote map[uuid.UUID]int, liveOnAir []uuid.UUID,
	capacity, nvencMax, vtMax int) warmPlan {
	var p warmPlan
	sort.SliceStable(demands, func(i, j int) bool {
		a, b := demands[i], demands[j]
		if a.Humans != b.Humans {
			return a.Humans > b.Humans
		}
		if remote[a.Channel] != remote[b.Channel] {
			return remote[a.Channel] > remote[b.Channel]
		}
		return a.Channel.String() < b.Channel.String()
	})
	for _, d := range demands {
		p.live = append(p.live, d.Channel)
	}
	if capacity > 0 && len(p.live) > capacity {
		p.live = p.live[:capacity]
	}
	p.liveBudget = slot.Budget{MaxActive: capacity, NVENCMax: nvencMax, VTMax: vtMax}
	onAir := map[uuid.UUID]bool{}
	for _, id := range liveOnAir {
		onAir[id] = true
	}
	room := capacity - len(liveOnAir)
	if nvencMax > 0 {
		if hw := (nvencMax - 2*len(liveOnAir)) / 2; hw < room {
			room = hw
		}
	}
	if !set.HotStandby || room <= 0 {
		p.stBudget = slot.Budget{MaxActive: 1, NVENCMax: 1, VTMax: 1}
		return p
	}
	cands := make([]uuid.UUID, 0, len(liveOnAir))
	seen := map[uuid.UUID]bool{}
	for _, d := range demands {
		if d.Humans == 0 && d.Pushes == 0 {
			continue
		}
		cands = append(cands, d.Channel)
		seen[d.Channel] = true
	}
	if set.StandbyWarm == StandbyWarmDemand {
		// demand: also what live keeps warm after its last viewer left
		rest := make([]uuid.UUID, 0, len(liveOnAir))
		for _, id := range liveOnAir {
			if !seen[id] {
				rest = append(rest, id)
			}
		}
		sort.Slice(rest, func(i, j int) bool { return rest[i].String() < rest[j].String() })
		cands = append(cands, rest...)
	}
	for _, id := range cands {
		if len(p.standby) >= room {
			break
		}
		if !onAir[id] {
			continue // not on this host's live slot (a node's, or not started yet)
		}
		p.standby = append(p.standby, id)
	}
	p.stBudget = slot.Budget{MaxActive: maxInt(room, 1)}
	if nvencMax > 0 {
		p.stBudget.NVENCMax = maxInt(2*len(p.standby), 1)
	}
	if vtMax > 0 {
		p.stBudget.VTMax = maxInt(minInt(2*len(p.standby), vtMax-2*len(liveOnAir)), 1)
	}
	return p
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SyncWarm sends each slot its role, budget and warm set.
func (s *Slots) SyncWarm(ctx context.Context) {
	s.mu.Lock()
	s.lastWarm = time.Now()
	live, standby, staging, epoch := s.live, s.standby, s.staging, s.epoch
	var liveOnAir []uuid.UUID
	if h := s.health[live]; h != nil && h.ok {
		for _, c := range h.status.Stations {
			if id, err := uuid.Parse(c); err == nil {
				liveOnAir = append(liveOnAir, id)
			}
		}
	}
	s.mu.Unlock()
	set := s.settings(ctx)
	capacity, nvencMax, vtMax := 8, 0, 0
	if s.Budget != nil {
		capacity, nvencMax, vtMax = s.Budget(ctx)
	}
	var demands []ChannelDemand
	if s.Demand != nil {
		demands = s.Demand()
	}
	var remote map[uuid.UUID]int
	if s.RemoteViewers != nil && set.StandbyWarm == StandbyWarmViewers {
		remote = s.RemoteViewers(ctx)
	}
	plan := planWarm(set, demands, remote, liveOnAir, capacity, nvencMax, vtMax)
	if gone := s.rollingSwitched(); len(gone) > 0 {
		// a rolling swap releases a channel on the old live slot right after its
		// switch (design §8.6): the extra encodes stay within the budget
		kept := plan.live[:0:0]
		for _, id := range plan.live {
			if !gone[id] {
				kept = append(kept, id)
			}
		}
		plan.live = kept
	}
	if st := s.sendWarm(ctx, live, slot.WarmRequest{Role: slot.RoleLive, Epoch: epoch,
		Channels: ids(plan.live), Budget: plan.liveBudget}); st != nil {
		s.remember(live, *st)
	}
	s.ensureStandby(ctx, set.HotStandby, standby)
	if standby != "" && set.HotStandby {
		if st := s.sendWarm(ctx, standby, slot.WarmRequest{Role: slot.RoleStandby, Epoch: epoch,
			Channels: ids(plan.standby), Budget: plan.stBudget, Exclusive: true}); st != nil {
			s.remember(standby, *st)
		}
	}
	// story 33.7: a swap's (or rollback's) target warms the watched channels
	// before their instants — in a rolling swap only shortly before — and keeps
	// those switched to it; the previous slot keeps the watched channels warm
	// for a warm rollback (design §8.6, §10). A target that is the staging slot of
	// a staged change keeps story 33.5's warm set (it holds the watched channels).
	to, toRole, toChans, prev, prevChans := s.opWarm(plan.live, demands, time.Now())
	staged := s.stageActive()
	if staging != "" && (staging != to || staged) {
		stby := 0
		if set.HotStandby && standby != "" {
			stby = len(plan.standby)
		}
		s.syncStaging(ctx, staging, epoch, capacity, nvencMax, vtMax, len(liveOnAir), stby, demands)
	}
	if to == staging && staged {
		to = ""
	}
	if to != "" {
		if st := s.sendWarm(ctx, to, slot.WarmRequest{Role: toRole, Epoch: epoch, Channels: ids(toChans),
			Budget: extraBudget(len(toChans), nvencMax, vtMax)}); st != nil {
			s.remember(to, *st)
		}
	}
	if prev != "" && prev != to {
		if st := s.sendWarm(ctx, prev, slot.WarmRequest{Role: slot.RolePrevious, Epoch: epoch, Channels: ids(prevChans),
			Budget: extraBudget(len(prevChans), nvencMax, vtMax), Exclusive: true}); st != nil {
			s.remember(prev, *st)
		}
	}
}

// extraBudget is the encoder budget of a slot that warms n channels besides
// live's (a swap's target, the previous slot): exactly what it holds.
func extraBudget(n, nvencMax, vtMax int) slot.Budget {
	b := slot.Budget{MaxActive: maxInt(n, 1)}
	if nvencMax > 0 {
		b.NVENCMax = maxInt(2*n, 1)
	}
	if vtMax > 0 {
		b.VTMax = maxInt(2*n, 1)
	}
	return b
}

// opWarm is the warm set of a running operation's target and of the previous
// slot.
func (s *Slots) opWarm(liveSet []uuid.UUID, demands []ChannelDemand, now time.Time) (to, toRole string,
	toChans []uuid.UUID, prev string, prevChans []uuid.UUID) {
	watched := map[uuid.UUID]bool{}
	for _, d := range demands {
		if d.Humans > 0 || d.Pushes > 0 {
			watched[d.Channel] = true
		}
	}
	for _, id := range liveSet {
		watched[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if op := s.op; op != nil && op.To != "" && op.To != op.From {
		to, toRole = op.To, slot.RoleStaging
		if op.To == s.previous {
			toRole = slot.RolePrevious
		}
		for ch, st := range op.state {
			if op.removed[ch] || st == switchSkipped {
				continue
			}
			if st == switchSwitched || (watched[ch] && (!op.rolling || op.at[ch].Sub(now) < rollingLead)) {
				toChans = append(toChans, ch)
			}
		}
		sort.Slice(toChans, func(i, j int) bool { return toChans[i].String() < toChans[j].String() })
	}
	if s.previous != "" && s.previous != s.live {
		prev = s.previous
		for ch := range watched {
			prevChans = append(prevChans, ch)
		}
		sort.Slice(prevChans, func(i, j int) bool { return prevChans[i].String() < prevChans[j].String() })
	}
	return
}

// ensureStandby starts or stops the standby instance with hot_standby.
func (s *Slots) ensureStandby(ctx context.Context, on bool, name string) {
	if name == "" {
		return
	}
	want := -1
	if on {
		want = 1
	}
	s.mu.Lock()
	changed := s.wantStby != want
	s.wantStby = want
	s.mu.Unlock()
	if changed {
		s.supervise(ctx, name, on)
	}
}

// EncodersUsed is the hardware encoder sessions the slots held when they
// last reported (VOD counts them against the channels' cap).
func (s *Slots) EncodersUsed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, h := range s.health {
		if h != nil {
			n += h.status.EncodersUsed
		}
	}
	return n
}

func (s *Slots) remember(name string, st slot.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h := s.health[name]; h != nil {
		h.status = st
	}
}

func ids(in []uuid.UUID) []string {
	out := make([]string, 0, len(in))
	for _, id := range in {
		out = append(out, id.String())
	}
	return out
}

func (s *Slots) sendWarm(ctx context.Context, name string, w slot.WarmRequest) *slot.Status {
	base := s.Base(name)
	if base == "" {
		return nil
	}
	body, _ := json.Marshal(w)
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(wctx, http.MethodPost, base+"/internal/tv/slot/warm", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client().Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var st slot.Status
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&st) != nil {
		return nil
	}
	return &st
}

// Healthy reports the last probe of each slot (internal status route).
func (s *Slots) Healthy() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for n, h := range s.health {
		out[n] = h.ok
	}
	return out
}

// LiveStations is the channels on air on the live slot (its last status).
func (s *Slots) LiveStations() []uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []uuid.UUID
	if h := s.health[s.live]; h != nil && h.ok {
		for _, c := range h.status.Stations {
			if id, err := uuid.Parse(c); err == nil {
				out = append(out, id)
			}
		}
	}
	return out
}

// SlotView is one slot in the internal status.
type SlotView struct {
	Slot    string      `json:"slot"`
	Role    string      `json:"role"`
	Healthy bool        `json:"healthy"`
	Status  slot.Status `json:"status"`
}

// View is the routing and each slot's last status (GET /internal/tv/slots).
func (s *Slots) View() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var views []SlotView
	for _, n := range slot.Names {
		if _, ok := s.Addrs[n]; !ok {
			continue
		}
		role := slot.RoleIdle
		switch n {
		case s.live:
			role = slot.RoleLive
		case s.standby:
			role = slot.RoleStandby
		case s.staging:
			role = slot.RoleStaging
		case s.previous:
			role = slot.RolePrevious
		}
		v := SlotView{Slot: n, Role: role}
		if h := s.health[n]; h != nil {
			v.Healthy, v.Status = h.ok, h.status
		}
		views = append(views, v)
	}
	out := map[string]any{"host_id": s.Host, "epoch": s.epoch, "live": s.live, "standby": s.standby,
		"staging": s.staging, "switches": s.switches, "slots": views, "previous": s.previous}
	if !s.previousUntil.IsZero() && s.previous != "" {
		out["previous_until"] = s.previousUntil.UTC().Format(time.RFC3339)
	}
	if op := s.op; op != nil {
		sw, sk, pl := op.counts()
		out["operation"] = map[string]any{"id": op.ID, "kind": op.Kind, "change_id": op.ChangeID, "from": op.From,
			"to": op.To, "switched": sw, "skipped": sk, "planned": pl, "rolling": op.rolling}
	}
	return out
}
