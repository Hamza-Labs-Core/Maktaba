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

	clientOnce sync.Once
	mu         sync.Mutex
	live       string
	standby  string
	staging  string
	epoch    int64
	health   map[string]*slotHealth
	failing  map[string]bool // in a failover right now
	switches int
	lastWarm time.Time
	wantStby int // 0: not asked yet, 1: started, -1: stopped (supervisor)
}

// slotHealth is what the last probes of a slot said.
type slotHealth struct {
	ok       bool
	fails    int
	at       time.Time
	status   slot.Status
	upAt     time.Time // first good probe after a bad one
	reported bool      // an exit report from the supervisor
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
	live, standby, staging := "", "", ""
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
	if standby == "" || standby == live {
		standby = s.otherThan(live, staging)
	}
	s.mu.Lock()
	s.live, s.standby, s.staging, s.epoch = live, standby, staging, epoch
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
	live, standby, staging, epoch := s.Roles()
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
		return fmt.Errorf("frontdoor: routing epoch %d in tv_slots is newer than %d", cur, epoch)
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
		if time.Since(s.lastWarmAt()) >= 2*time.Second {
			s.SyncWarm(ctx)
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
	for _, n := range []string{live, standby, staging} {
		if n == "" {
			continue
		}
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
	if err == nil {
		if !h.ok {
			h.upAt = now
		}
		h.ok, h.fails, h.status, h.reported = true, 0, st, false
	} else {
		h.ok = false
		h.fails++
	}
	h.at = now
	fails, reported := h.fails, h.reported
	isLive := name == s.live
	s.mu.Unlock()
	if err != nil && isLive && (fails >= s.failAfter() || reported) {
		s.Failover(ctx, name, "probe: "+err.Error())
	}
	return err == nil
}

// Suspect re-probes a slot at once (a proxied request to it failed).
func (s *Slots) Suspect(ctx context.Context, name string) {
	if s.check(context.WithoutCancel(ctx), name) {
		return
	}
	live, _ := s.Live()
	if name == live {
		// a refused connection is a dead process: no second probe
		if _, err := s.probe(context.WithoutCancel(ctx), name); err != nil {
			s.Failover(ctx, name, "upstream: "+err.Error())
		}
	}
}

// Exited is the supervisor's report that a slot process ended.
func (s *Slots) Exited(ctx context.Context, name string) {
	s.mu.Lock()
	if h := s.health[name]; h != nil {
		h.ok, h.reported = false, true
	}
	isLive := name == s.live
	s.mu.Unlock()
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
		s.staging = s.otherThan(s.live, s.standby)
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
	s.mu.Unlock()
	if name == "" {
		return nil
	}
	s.supervise(ctx, name, false)
	return s.persist(ctx)
}

// supervise asks the supervisor to start or stop an instance.
func (s *Slots) supervise(ctx context.Context, name string, start bool) {
	if s.Supervisor == "" {
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
	live, standby       []uuid.UUID
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
	if staging != "" {
		_ = s.sendWarm(ctx, staging, slot.WarmRequest{Role: slot.RoleStaging, Epoch: epoch})
	}
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
		}
		v := SlotView{Slot: n, Role: role}
		if h := s.health[n]; h != nil {
			v.Healthy, v.Status = h.ok, h.status
		}
		views = append(views, v)
	}
	return map[string]any{"host_id": s.Host, "epoch": s.epoch, "live": s.live, "standby": s.standby,
		"staging": s.staging, "switches": s.switches, "slots": views}
}
