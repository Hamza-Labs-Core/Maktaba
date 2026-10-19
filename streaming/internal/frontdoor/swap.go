package frontdoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

// The swap in the front door (Epic 33 stories 33.7 and 33.9, design §8, §10).
//
// The database side of a swap is one transaction that is not the front door's:
// the change controller's swap commit (pipeline tvrelease/commit.py) applies the
// release's lineup and settings, flips tv_routing to the new generation and
// writes a swap operation — tv_swap_ops (kind swap) with one tv_channel_switch
// row per channel and its planned switch instant (the end of the programme on
// the air: README "swap at each channel's programme boundary"). A rollback is
// the same operation the other way (kind rollback, every channel at once): the
// API writes it for an admin's warm rollback, and this front door writes it
// itself when the new live slot crashes inside the grace (automatic rollback);
// the controller then runs the reverse commit (tvrelease/rollback.py).
//
// The front door runs the operation on its own host's slots, per channel and
// make-before-break: the target slot (the one running the operation's
// release) warms the watched channels; at a channel's instant its HLS playlist
// continues on the target after one EXT-X-DISCONTINUITY (Stitcher.Plan), every
// open TS connection — Plex's tuners, VLC, and the push relay feeding
// Cloudflare — is spliced onto the target's TS at the IDR both slots have at
// that instant (spliceTS), without closing the connection. A channel nobody
// watches switches at once. When every channel is switched the target is the
// live slot, the old live slot is the previous slot (warm on the watched
// channels until tv_routing.previous_until: the warm rollback target), and the
// old hot standby drains; at the end of the grace the previous slot stops and
// a new standby starts on the live release.

// Operation kinds (tv_swap_ops.kind).
const (
	OpSwap     = "swap"
	OpRollback = "rollback"
)

// Channel switch states (tv_channel_switch.state).
const (
	switchPlanned  = "planned"
	switchSwitched = "switched"
	switchSkipped  = "skipped"
)

// Swap timings.
const (
	// swapPrepLead: a splicer opens the target's TS this long before the
	// channel's instant, so it is aligned before the live one is let go.
	swapPrepLead = 10 * time.Second
	// swapSettle: an operation is done this long after its last instant (the
	// splicers met their IDRs).
	swapSettle = 8 * time.Second
	// swapTargetWait: a swap whose target slot does not answer by then fails
	// (the controller rolls the commit back).
	swapTargetWait = 90 * time.Second
	// switchRetry: a channel whose target is down at its instant is tried once
	// more this much later, then skipped (story 33.7 EC2).
	switchRetry = 5 * time.Second
	// rollingLead: in a rolling swap a channel is warmed on the target this
	// long before its instant (first segment ready + margin, design §8.6).
	rollingLead = 20 * time.Second
	opsPoll     = time.Second
)

// swapOp is the operation the front door runs now.
type swapOp struct {
	ID       int64
	ChangeID string
	Kind     string
	Release  string
	Reason   string
	From, To string
	at       map[uuid.UUID]time.Time
	state    map[uuid.UUID]string
	removed  map[uuid.UUID]bool
	retried  map[uuid.UUID]bool
	rolling  bool
	created  time.Time
	started  time.Time
}

func (o *swapOp) lastAt() time.Time {
	var t time.Time
	for _, at := range o.at {
		if at.After(t) {
			t = at
		}
	}
	return t
}

func (o *swapOp) counts() (switched, skipped, planned int) {
	for _, st := range o.state {
		switch st {
		case switchSwitched:
			switched++
		case switchSkipped:
			skipped++
		default:
			planned++
		}
	}
	return
}

// SwitchPlan is a channel's planned switch (the splicers' view).
type SwitchPlan struct {
	OpID     int64
	Kind     string
	From, To string
	At       time.Time
	// Removed: the channel is not in the new lineup; its streams end at At.
	Removed bool
}

// Route is the slot a channel is read from now: the live slot, or — while an
// operation runs — the target once the channel's instant passed (a skipped
// channel stays on the old live slot).
func (s *Slots) Route(ch uuid.UUID) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.routeLocked(ch, time.Now())
}

func (s *Slots) routeLocked(ch uuid.UUID, now time.Time) string {
	op := s.op
	if op == nil || op.To == "" {
		return s.live
	}
	at, planned := op.at[ch]
	if !planned || op.state[ch] == switchSkipped {
		return op.From
	}
	if !now.Before(at) {
		return op.To
	}
	return op.From
}

// Plan is a channel's switch in the running operation (false: none, or the
// channel was skipped).
func (s *Slots) Plan(ch uuid.UUID) (SwitchPlan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := s.op
	if op == nil || op.To == "" {
		return SwitchPlan{}, false
	}
	at, ok := op.at[ch]
	if !ok || op.state[ch] == switchSkipped {
		return SwitchPlan{}, false
	}
	return SwitchPlan{OpID: op.ID, Kind: op.Kind, From: op.From, To: op.To, At: at, Removed: op.removed[ch]}, true
}

// Previous is the previous slot (the warm rollback target) and until when it
// is kept ("" : none).
func (s *Slots) Previous() (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.previous, s.previousUntil
}

// Operation reports the running operation (id 0: none).
func (s *Slots) Operation() (id int64, kind, from, to string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.op == nil {
		return 0, "", "", ""
	}
	return s.op.ID, s.op.Kind, s.op.From, s.op.To
}

// RunOps runs the swap and rollback operations of this host until ctx ends.
func (s *Slots) RunOps(ctx context.Context) {
	t := time.NewTicker(opsPoll)
	defer t.Stop()
	for {
		s.OpsOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// OpsOnce is one pass: pick up a new operation, advance the running one, end
// the grace.
func (s *Slots) OpsOnce(ctx context.Context) {
	if s.Pool != nil {
		if err := s.loadOps(ctx); err != nil && ctx.Err() == nil {
			s.log().Debug("tv: reading the swap operations failed", "err", err.Error(), "event", "tv_swap_read_failed")
		}
	}
	s.advance(ctx, time.Now())
	s.graceCheck(ctx, time.Now())
}

type opRow struct {
	id                                int64
	change, kind, release, reason, st string
	created                           time.Time
}

func (s *Slots) loadOps(ctx context.Context) error {
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := s.Pool.Query(qctx, `SELECT id, COALESCE(change_id::text, ''), kind, COALESCE(release_id::text, ''),
			reason, state, created_at
		FROM tv_swap_ops WHERE state IN ('planned', 'switching') AND (scope = 'cluster' OR scope = $1)
		ORDER BY id`, "host:"+s.Host)
	if err != nil {
		return err
	}
	var ops []opRow
	for rows.Next() {
		var o opRow
		if err := rows.Scan(&o.id, &o.change, &o.kind, &o.release, &o.reason, &o.st, &o.created); err != nil {
			rows.Close()
			return err
		}
		ops = append(ops, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range ops {
		s.mu.Lock()
		cur := s.op
		s.mu.Unlock()
		if cur != nil && cur.ID == o.id {
			continue
		}
		if cur != nil {
			if o.kind == OpRollback && cur.Kind == OpSwap {
				// story 33.9 EC1: a rollback during a swap — switched channels go
				// back, unswitched ones simply are not switched
				s.endOp(ctx, cur, "cancelled", map[string]any{"by_rollback": o.id})
			} else if o.id > cur.ID {
				continue // one at a time
			}
		}
		s.startOp(ctx, o)
		s.mu.Lock()
		started := s.op != nil && s.op.ID == o.id
		s.mu.Unlock()
		if started {
			break
		}
	}
	return nil
}

// target is the slot that runs release (not the live one), healthy.
func (s *Slots) targetFor(release string, kind string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := func(n string) bool {
		h := s.health[n]
		return n != "" && h != nil && h.ok && (release == "" || h.status.Release == release)
	}
	if kind == OpRollback {
		if release == "" {
			// no release to go back to (no rollback point): never "any healthy
			// slot", which could be the very release being rolled back
			return "", false
		}
		if ok(s.previous) {
			return s.previous, true
		}
		if ok(s.live) {
			return s.live, true // nothing to switch (a swap that never finished)
		}
	} else {
		if release != "" && ok(s.live) {
			// the live slot already runs the swap's release: the operation
			// finished before this front door restarted, or its 'done' write was
			// lost — nothing to switch, it just ends (never failed, which would
			// roll a good swap back)
			return s.live, true
		}
		if ok(s.staging) && s.staging != s.live {
			return s.staging, true
		}
	}
	for _, n := range slot.Names {
		if n != s.live && n != s.standby && ok(n) {
			return n, true
		}
	}
	if kind == OpRollback && ok(s.standby) {
		return s.standby, true // the standby runs the live release
	}
	return "", false
}

func (s *Slots) startOp(ctx context.Context, o opRow) {
	now := time.Now()
	to, found := s.targetFor(o.release, o.kind)
	if !found {
		if now.Sub(o.created) > swapTargetWait {
			s.failRow(ctx, o.id, "no healthy slot runs release "+o.release)
			return
		}
		if o.kind == OpSwap {
			s.ensureTarget(ctx, o.release)
		}
		return
	}
	rows, err := s.switchRows(ctx, o.id)
	if err != nil {
		return
	}
	s.runOp(ctx, o, to, rows, now)
}

// runOp starts an operation on its target with its channel rows.
func (s *Slots) runOp(ctx context.Context, o opRow, to string, rows []switchRow, now time.Time) {
	set := s.settings(ctx)
	watched := map[uuid.UUID]bool{}
	if s.Demand != nil {
		for _, d := range s.Demand() {
			if d.Humans > 0 || d.Pushes > 0 {
				watched[d.Channel] = true
			}
		}
	}
	s.mu.Lock()
	from := s.live
	op := &swapOp{ID: o.id, ChangeID: o.change, Kind: o.kind, Release: o.release, Reason: o.reason,
		From: from, To: to, at: map[uuid.UUID]time.Time{}, state: map[uuid.UUID]string{},
		removed: map[uuid.UUID]bool{}, retried: map[uuid.UUID]bool{}, created: o.created, started: now}
	for _, r := range rows {
		at := r.at
		if o.kind == OpRollback || !watched[r.ch] || at.Before(now) {
			at = now // nothing to splice, or no boundary to wait for (design §8.2, §10)
		}
		op.at[r.ch] = at
		op.state[r.ch] = r.state
		if r.state == "" || r.state == "switching" {
			op.state[r.ch] = switchPlanned
		}
		op.removed[r.ch] = r.removed
	}
	if to == from {
		for ch := range op.state {
			op.state[ch] = switchSwitched
		}
	}
	budget := s.targetBudgetLocked(set)
	op.rolling = planRolling(op, watched, budget, set.RollingSwap, now)
	if o.kind == OpSwap && s.staging == "" && to != from {
		s.staging = to
	}
	s.op = op
	s.epoch++
	s.mu.Unlock()
	s.log().Info("tv: swap operation started", "op", o.id, "kind", o.kind, "change_id", o.change, "from", from, "to", to,
		"channels", len(rows), "rolling", op.rolling, "event", map[string]string{OpSwap: "tv_swap_planned",
			OpRollback: "tv_rollback_started"}[o.kind])
	s.exec(ctx, `UPDATE tv_swap_ops SET state = 'switching', started_at = COALESCE(started_at, now()), host_id = $2,
		from_slot = $3, to_slot = $4 WHERE id = $1`, o.id, s.Host, from, to)
	for ch, at := range op.at {
		s.exec(ctx, `UPDATE tv_channel_switch SET switch_at = $3 WHERE op_id = $1 AND channel_id = $2 AND state = 'planned'`,
			o.id, ch, at)
	}
	go func() { _ = s.persist(context.WithoutCancel(ctx)) }()
	s.SyncWarm(context.WithoutCancel(ctx))
}

// planRolling spreads the switch instants when the target's budget cannot hold
// every watched channel at once (design §8.6): the channels switch in order,
// budget at a time, rollingLead apart; each is warmed on the target only shortly
// before its instant (SyncWarm). Boundary mode already spreads them.
func planRolling(op *swapOp, watched map[uuid.UUID]bool, budget int, mode string, now time.Time) bool {
	var ws []uuid.UUID
	for ch := range op.at {
		if watched[ch] && op.state[ch] == switchPlanned && !op.removed[ch] {
			ws = append(ws, ch)
		}
	}
	if mode == "never" || budget <= 0 || (mode != "always" && len(ws) <= budget) {
		return false
	}
	sort.Slice(ws, func(i, j int) bool {
		if !op.at[ws[i]].Equal(op.at[ws[j]]) {
			return op.at[ws[i]].Before(op.at[ws[j]])
		}
		return ws[i].String() < ws[j].String()
	})
	var prev time.Time
	for i, ch := range ws {
		slotAt := now.Add(rollingLead + time.Duration(i/budget)*rollingLead)
		at := op.at[ch]
		if at.Before(slotAt) {
			at = slotAt
		}
		if at.Before(prev) {
			at = prev
		}
		op.at[ch], prev = at, at
	}
	return true
}

// targetBudgetLocked is how many channels the target slot may warm at once.
func (s *Slots) targetBudgetLocked(set StagingSettings) int {
	capacity := 8
	if s.Budget != nil {
		capacity, _, _ = s.Budget(context.Background())
	}
	used := 0
	if h := s.health[s.live]; h != nil {
		used = len(h.status.Stations)
	}
	room := capacity - used
	if set.MaxExtra > 0 && set.MaxExtra < room {
		room = set.MaxExtra
	}
	if room < 1 {
		room = 1
	}
	return room
}

type switchRow struct {
	ch      uuid.UUID
	at      time.Time
	state   string
	removed bool
}

func (s *Slots) switchRows(ctx context.Context, op int64) ([]switchRow, error) {
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := s.Pool.Query(qctx, `SELECT channel_id, switch_at, state, removed FROM tv_channel_switch WHERE op_id = $1`, op)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []switchRow
	for rows.Next() {
		var r switchRow
		if err := rows.Scan(&r.ch, &r.at, &r.state, &r.removed); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ensureTarget starts a slot instance on the release when none runs it (the
// staging slot of story 33.5 normally does: this covers a front door that
// restarted, or a staging slot that crashed and was not restarted).
func (s *Slots) ensureTarget(ctx context.Context, release string) {
	if s.Supervisor == "" || release == "" {
		return
	}
	s.mu.Lock()
	name := s.staging
	if name == "" {
		name = s.freeInstanceLocked()
		s.staging = name
	}
	s.mu.Unlock()
	if name == "" {
		return
	}
	s.superviseRelease(ctx, name, release)
}

// advance marks the channels whose instant passed and ends a finished operation.
func (s *Slots) advance(ctx context.Context, now time.Time) {
	s.mu.Lock()
	op := s.op
	if op == nil {
		s.mu.Unlock()
		return
	}
	toOK := false
	if h := s.health[op.To]; h != nil && h.ok {
		toOK = true
	}
	type upd struct {
		ch    uuid.UUID
		state string
		late  time.Duration
	}
	var ups []upd
	for ch, st := range op.state {
		if st != switchPlanned || now.Before(op.at[ch]) {
			continue
		}
		switch {
		case toOK || op.removed[ch]:
			op.state[ch] = switchSwitched
			ups = append(ups, upd{ch, switchSwitched, now.Sub(op.at[ch])})
		case !op.retried[ch]:
			op.retried[ch] = true
			op.at[ch] = now.Add(switchRetry)
		default:
			op.state[ch] = switchSkipped
			ups = append(ups, upd{ch, switchSkipped, 0})
		}
	}
	_, _, planned := op.counts()
	done := planned == 0 && !now.Before(op.lastAt().Add(swapSettle))
	s.mu.Unlock()
	for _, u := range ups {
		s.exec(ctx, `UPDATE tv_channel_switch SET state = $3, switched_at = now() WHERE op_id = $1 AND channel_id = $2`,
			op.ID, u.ch, u.state)
		tvmetrics.ChannelSwitch.Inc(op.Kind, u.state)
		if u.state == switchSwitched {
			tvmetrics.SwitchLatency.Set(u.late.Seconds(), op.Kind)
			s.log().Info("tv: channel switched", "op", op.ID, "kind", op.Kind, "channel_id", u.ch.String(),
				"to", op.To, "event", "tv_swap_channel_switched")
		} else {
			s.log().Warn("tv: channel skipped (its new slot did not answer)", "op", op.ID, "channel_id", u.ch.String(),
				"event", "tv_swap_channel_skipped")
		}
	}
	if done {
		s.finish(ctx, op)
	}
}

// finish ends an operation whose channels are all switched (or skipped).
func (s *Slots) finish(ctx context.Context, op *swapOp) {
	set := s.settings(ctx)
	switched, skipped, _ := op.counts()
	s.mu.Lock()
	if s.op != op {
		s.mu.Unlock()
		return
	}
	s.op = nil
	oldLive, oldStandby, oldPrevious := s.live, s.standby, s.previous
	var drain []string
	var standbyStart, standbyRelease string
	if op.To != op.From {
		s.live = op.To
		s.switches++
	}
	if s.staging == op.To {
		s.staging = ""
	}
	switch op.Kind {
	case OpSwap:
		if op.To != op.From {
			s.previous = op.From
			s.previousUntil = s.previousUntilLocked(ctx, set)
			if oldStandby != "" && oldStandby != op.To {
				drain = append(drain, oldStandby) // the old release's standby (design §2.4)
			}
			if oldPrevious != "" && oldPrevious != op.To && oldPrevious != op.From && oldPrevious != oldStandby {
				// a second swap inside the first one's grace: the first one's
				// previous slot is no rollback target any more (tv_routing's
				// previous is now op.From's release) and would run on, role-less
				drain = append(drain, oldPrevious)
			}
			s.standby = ""
		}
	case OpRollback:
		if oldLive != s.live && oldLive != "" {
			drain = append(drain, oldLive) // the rolled-back release
		}
		if oldPrevious == s.live {
			s.previous, s.previousUntil = "", time.Time{}
		}
		if s.standby == s.live || s.standby == "" {
			s.standby = ""
			if set.HotStandby {
				s.standby = s.otherThan(s.live, oldLive)
				if s.standby == "" {
					s.standby = oldLive
				}
				standbyStart = s.standby
			}
		}
		standbyRelease = op.Release
	}
	s.epoch++
	epoch := s.epoch
	s.mu.Unlock()
	if s.Pool != nil {
		detail, _ := json.Marshal(map[string]any{"switched": switched, "skipped": skipped, "rolling": op.rolling,
			"from_slot": op.From, "to_slot": op.To})
		s.exec(ctx, `UPDATE tv_swap_ops SET state = 'done', done_at = now(), detail = detail || $2::jsonb WHERE id = $1`,
			op.ID, string(detail))
	}
	tvmetrics.Swaps.Inc(op.Kind, "done")
	if op.Kind == OpRollback {
		tvmetrics.Rollbacks.Inc(reasonLabel(op.Reason))
	}
	s.log().Info("tv: swap operation done", "op", op.ID, "kind", op.Kind, "change_id", op.ChangeID, "live", op.To,
		"replaced", op.From, "switched", switched, "skipped", skipped, "epoch", epoch,
		"event", map[string]string{OpSwap: "tv_swap_done", OpRollback: "tv_rollback_done"}[op.Kind])
	for _, n := range drain {
		s.drainSlot(ctx, n)
	}
	if standbyStart != "" {
		s.restartAsStandby(ctx, standbyStart, standbyRelease)
	}
	_ = s.persist(ctx)
	s.SyncWarm(ctx)
}

// endOp ends an operation that did not finish (cancelled by a rollback, or its
// target died: failed). Its channels read the old live slot again.
func (s *Slots) endOp(ctx context.Context, op *swapOp, state string, detail map[string]any) {
	s.mu.Lock()
	if s.op != op {
		s.mu.Unlock()
		return
	}
	s.op = nil
	if s.staging == op.To {
		s.staging = ""
	}
	// a swap that did not finish leaves its target running the release that is
	// not going on the air (cancelled by a rollback, or failed): it stops, unless
	// it holds another role now
	abandoned := ""
	if op.Kind == OpSwap && op.To != "" && op.To != op.From && op.To != s.live && op.To != s.standby &&
		op.To != s.previous {
		abandoned = op.To
	}
	s.epoch++
	s.mu.Unlock()
	if abandoned != "" {
		s.drainSlot(ctx, abandoned)
	}
	if detail == nil {
		detail = map[string]any{}
	}
	switched, skipped, planned := op.counts()
	detail["switched"], detail["skipped"], detail["unswitched"] = switched, skipped, planned
	b, _ := json.Marshal(detail)
	s.exec(ctx, `UPDATE tv_swap_ops SET state = $2, done_at = now(), detail = detail || $3::jsonb WHERE id = $1`,
		op.ID, state, string(b))
	s.exec(ctx, `UPDATE tv_channel_switch SET state = 'skipped' WHERE op_id = $1 AND state = 'planned'`, op.ID)
	tvmetrics.Swaps.Inc(op.Kind, state)
	s.log().Warn("tv: swap operation ended unfinished", "op", op.ID, "kind", op.Kind, "state", state, "detail", detail,
		"event", "tv_swap_failed")
	_ = s.persist(ctx)
	s.SyncWarm(ctx)
}

func (s *Slots) failRow(ctx context.Context, id int64, why string) {
	b, _ := json.Marshal(map[string]any{"error": why})
	s.exec(ctx, `UPDATE tv_swap_ops SET state = 'failed', done_at = now(), detail = detail || $2::jsonb WHERE id = $1`, id, string(b))
	tvmetrics.Swaps.Inc("swap", "failed")
	s.log().Warn("tv: swap operation failed", "op", id, "why", why, "event", "tv_swap_failed")
}

// previousUntilLocked is tv_routing.previous_until (the commit's), else now +
// keep_warm_minutes.
func (s *Slots) previousUntilLocked(ctx context.Context, set StagingSettings) time.Time {
	until := time.Now().Add(set.KeepWarm)
	if s.Pool != nil {
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		var t *time.Time
		if s.Pool.QueryRow(qctx, `SELECT previous_until FROM tv_routing WHERE scope = 'cluster'`).Scan(&t) == nil &&
			t != nil && t.After(time.Now()) {
			until = *t
		}
		cancel()
	}
	return until
}

// graceCheck ends the grace: the previous slot drains and stops, and a hot
// standby starts on the live release (design §2.4).
func (s *Slots) graceCheck(ctx context.Context, now time.Time) {
	set := s.settings(ctx)
	s.mu.Lock()
	if s.previous == "" || s.op != nil || now.Before(s.previousUntil) {
		s.mu.Unlock()
		return
	}
	prev := s.previous
	s.previous, s.previousUntil = "", time.Time{}
	start := ""
	if set.HotStandby && s.standby == "" {
		s.standby = s.otherThan(s.live, prev)
		if s.standby == "" {
			s.standby = prev
		}
		start = s.standby
	}
	live := s.live
	s.epoch++
	s.mu.Unlock()
	s.log().Info("tv: the rollback grace ended; the previous slot stops", "previous", prev, "standby", start,
		"event", "tv_slot_previous_ended")
	if start != prev {
		s.drainSlot(ctx, prev)
	}
	if start != "" {
		s.restartAsStandby(ctx, start, s.slotRelease(live))
	}
	_ = s.persist(ctx)
	s.SyncWarm(ctx)
}

// slotRelease is the release a slot reported.
func (s *Slots) slotRelease(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h := s.health[name]; h != nil {
		return h.status.Release
	}
	return ""
}

// drainSlot stops a slot's stations and asks the supervisor to stop it.
func (s *Slots) drainSlot(ctx context.Context, name string) {
	if base := s.Base(name); base != "" {
		dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		req, err := http.NewRequestWithContext(dctx, http.MethodPost, base+"/internal/tv/slot/drain", nil)
		if err == nil {
			if resp, err := s.client().Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
		cancel()
	}
	s.supervise(ctx, name, false)
	s.log().Info("tv: slot drained", "slot", name, "event", "tv_slot_stopped")
}

// restartAsStandby restarts an instance on the live release as the hot standby.
func (s *Slots) restartAsStandby(ctx context.Context, name, release string) {
	if s.Supervisor == "" {
		return // no supervisor (tests): whoever runs the processes restarts it
	}
	s.supervise(ctx, name, false)
	s.superviseRelease(ctx, name, release)
}

// superviseRelease asks the supervisor to start an instance on release (POST
// /slots/{name}/start) bound to it: {release_id, version, sha256} (this
// binary's version: this binary, no checksum; another streaming version, story
// 33.14: that version's binary side by side). No release: unbound (the body
// clears an earlier binding).
func (s *Slots) superviseRelease(ctx context.Context, name, release string) {
	if s.Supervisor == "" {
		return
	}
	body := []byte(`{"release_id":""}`)
	if release != "" {
		// always bound to the release asked for: an unbound instance would bind
		// itself to tv_routing's live release at its start, which right after a
		// rollback is still the rolled-back one until the reverse commit runs
		// (a standby restarted then would be the bad release, put back on the
		// air by the next failover). This binary's version runs this binary.
		v, sum := version.Version, ""
		if s.Pool != nil {
			qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			var rv, rs string
			if s.Pool.QueryRow(qctx, `SELECT COALESCE(streaming_version, ''), COALESCE(streaming_sha256, '')
				FROM tv_releases WHERE id = $1`, release).Scan(&rv, &rs) == nil && rv != "" && rv != version.Version {
				v, sum = rv, rs
			}
			cancel()
		}
		body, _ = json.Marshal(map[string]string{"release_id": release, "version": v, "sha256": sum})
	}
	sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sctx, http.MethodPost,
		strings.TrimRight(s.Supervisor, "/")+"/slots/"+name+"/start", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client().Do(req)
	if err != nil {
		s.log().Warn("tv: the supervisor did not take the slot request", "slot", name, "action", "start",
			"err", err.Error(), "event", "tv_slot_supervisor_failed")
		return
	}
	_ = resp.Body.Close()
}

// exec runs a statement best-effort (the operation goes on in memory).
func (s *Slots) exec(ctx context.Context, sql string, args ...any) {
	if s.Pool == nil {
		return
	}
	qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if _, err := s.Pool.Exec(qctx, sql, args...); err != nil {
		s.log().Warn("tv: writing the swap state failed", "err", err.Error(), "event", "tv_swap_write_failed")
	}
}

func reasonLabel(r string) string {
	switch {
	case r == "":
		return "manual"
	case strings.HasPrefix(r, "auto:"):
		return r
	}
	return "manual"
}

// failoverInOp handles the loss of a slot while an operation runs: the old
// live slot gone → every channel moves to the target at once (the change is
// committed and approved); the target gone → the operation fails and every
// channel reads the old live slot again (the controller rolls the commit
// back). Reports whether it handled it.
func (s *Slots) failoverInOp(ctx context.Context, from, why string) bool {
	s.mu.Lock()
	op := s.op
	if op == nil || (from != op.From && from != op.To) || op.To == op.From {
		s.mu.Unlock()
		return false
	}
	if from == op.From {
		now := time.Now()
		for ch, st := range op.state {
			if st == switchPlanned && op.at[ch].After(now) {
				op.at[ch] = now
			}
		}
		s.mu.Unlock()
		s.log().Warn("tv: the old live slot was lost during the swap; every channel switches now", "op", op.ID,
			"slot", from, "why", why, "event", "tv_swap_channels_now")
		s.advance(ctx, time.Now())
		return true
	}
	s.mu.Unlock()
	s.endOp(ctx, op, "failed", map[string]any{"error": "the new slot was lost: " + why, "reason": "auto:crash"})
	return true
}

// autoRollback: the live slot was lost inside the grace — roll back to the
// previous slot at once (story 33.9 AC5). The operation is written for the
// controller, which runs the reverse commit (change rolled_back, reason
// auto:crash). Reports whether it rolled back.
func (s *Slots) autoRollback(ctx context.Context, from, why string) bool {
	set := s.settings(ctx)
	now := time.Now()
	s.mu.Lock()
	prev := s.previous
	h := s.health[prev]
	if !set.AutoRollback || s.op != nil || prev == "" || s.live != from || now.After(s.previousUntil) || h == nil || !h.ok {
		s.mu.Unlock()
		return false
	}
	if s.failing[from] {
		s.mu.Unlock()
		return true // another caller is rolling back right now
	}
	s.failing[from] = true
	release := h.status.Release
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.failing, from)
		s.mu.Unlock()
	}()
	reason := "auto:crash"
	var id int64
	var change string
	if s.Pool != nil {
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err := s.Pool.QueryRow(qctx, `WITH live AS (
				SELECT id FROM tv_changes WHERE state IN ('live', 'swapping') ORDER BY swapped_at DESC NULLS LAST, updated_at DESC LIMIT 1)
			INSERT INTO tv_swap_ops (change_id, kind, release_id, reason, state, created_by, host_id, from_slot, to_slot, detail)
			VALUES ((SELECT id FROM live), 'rollback', NULLIF($1, '')::uuid, $2, 'planned', $3, $4, $5, $6,
				jsonb_build_object('why', $7::text))
			RETURNING id, COALESCE(change_id::text, '')`, release, reason, "frontdoor:"+s.Host, s.Host, from, prev, why).Scan(&id, &change)
		if err == nil {
			_, err = s.Pool.Exec(qctx, `INSERT INTO tv_channel_switch (op_id, change_id, channel_id, switch_at)
				SELECT $1, o.change_id, c.id, now() FROM channels c, tv_swap_ops o
				WHERE o.id = $1 AND c.lifecycle = 'live'
				ON CONFLICT DO NOTHING`, id)
		}
		cancel()
		if err != nil {
			s.log().Warn("tv: writing the automatic rollback failed; rolling back the streams anyway",
				"err", err.Error(), "event", "tv_rollback_write_failed")
		}
	}
	s.log().Warn("tv: the live slot was lost inside the grace; rolling back to the previous slot", "from", from,
		"to", prev, "why", why, "op", id, "event", "tv_rollback_auto")
	o := opRow{id: id, change: change, kind: OpRollback, release: release, reason: reason, created: now}
	if id == 0 {
		// no database: run it from memory (every channel the lost slot had)
		var rows []switchRow
		s.mu.Lock()
		if hl := s.health[from]; hl != nil {
			for _, c := range hl.status.Stations {
				if ch, err := uuid.Parse(c); err == nil {
					rows = append(rows, switchRow{ch: ch, at: now, state: switchPlanned})
				}
			}
		}
		s.mu.Unlock()
		s.runOp(ctx, o, prev, rows, now)
	} else {
		s.startOp(ctx, o)
	}
	s.advance(ctx, time.Now())
	return true
}

// ErrSwapInProgress: a routing switch was asked for while an operation runs.
var ErrSwapInProgress = errors.New("frontdoor: swap_in_progress")

func (s *Slots) opRunning() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.op != nil {
		return fmt.Errorf("%w (operation %d)", ErrSwapInProgress, s.op.ID)
	}
	return nil
}

// ensureBound pins an unbound live slot or standby to the release it airs: the
// live release, or — once a swap was committed and is not finished — the
// release the swap replaces (tv_swap_ops.from_release_id). Unbound, a slot
// would follow the generation the commit flips (story 33.7).
func (s *Slots) ensureBound(ctx context.Context, name string) {
	if s.Pool == nil {
		return
	}
	s.mu.Lock()
	if name != s.live && name != s.standby {
		s.mu.Unlock()
		return
	}
	if s.boundAt == nil {
		s.boundAt = map[string]time.Time{}
	}
	if time.Since(s.boundAt[name]) < 10*time.Second {
		s.mu.Unlock()
		return
	}
	s.boundAt[name] = time.Now()
	s.mu.Unlock()
	go func() {
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var rel *string
		if err := s.Pool.QueryRow(qctx, `SELECT COALESCE(
				(SELECT from_release_id::text FROM tv_swap_ops WHERE kind = 'swap' AND state IN ('planned', 'switching')
				  AND from_release_id IS NOT NULL ORDER BY id LIMIT 1),
				(SELECT live_release_id::text FROM tv_routing WHERE scope = 'cluster'))`).Scan(&rel); err != nil || rel == nil {
			return
		}
		body, _ := json.Marshal(slot.BindRequest{ReleaseID: *rel})
		req, err := http.NewRequestWithContext(qctx, http.MethodPost, s.Base(name)+"/internal/tv/slot/bind", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.client().Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
}

// AdoptStaging gives an instance that is neither live, standby nor previous
// the staging role (it runs a staged release; the swap's target). A crash of
// the live slot never fails over to it.
func (s *Slots) AdoptStaging(ctx context.Context, name string) error {
	if _, ok := s.Addrs[name]; !ok {
		return fmt.Errorf("frontdoor: unknown slot %q", name)
	}
	s.mu.Lock()
	if name == s.live || name == s.standby || name == s.previous {
		s.mu.Unlock()
		return fmt.Errorf("frontdoor: slot %s is %s", name, map[bool]string{true: "live", false: "the standby or previous"}[name == s.live])
	}
	s.staging = name
	s.epoch++
	s.mu.Unlock()
	err := s.persist(ctx)
	s.SyncWarm(ctx)
	return err
}

// rollingSwitched is the channels a rolling swap already switched (the old live
// slot lets them go).
func (s *Slots) rollingSwitched() map[uuid.UUID]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := s.op
	if op == nil || !op.rolling {
		return nil
	}
	out := map[uuid.UUID]bool{}
	for ch, st := range op.state {
		if st == switchSwitched {
			out[ch] = true
		}
	}
	return out
}

// stageActive reports whether a staged change runs on the staging slot (story
// 33.5's warm-up and sampling).
func (s *Slots) stageActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stage != nil
}

// freeInstanceLocked is an instance that is neither live, standby nor previous
// (the previous slot is the warm rollback target: a change staged inside the
// grace must not take it). s.mu is held.
func (s *Slots) freeInstanceLocked() string {
	for _, n := range slot.Names {
		if _, ok := s.Addrs[n]; ok && n != s.live && n != s.standby && n != s.previous {
			return n
		}
	}
	return ""
}
