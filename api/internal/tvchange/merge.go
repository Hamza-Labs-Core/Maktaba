package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"
)

// How updates to the TV add up in one change (the owner's rule, story 33.12 AC3).
//
// An update starts a change; every update after it joins that same change (same id,
// same number, updated_at bumped, an "append" event naming it) until the change is
// approved: the open change is the newest draft no one approved (Store.OpenDraft).
// A change approved, or beyond draft, takes no more updates: the next one opens a new
// change, queued behind it. A change that fails verification goes back to draft
// (Controller.failToDraft) and is the open change again; a draft opened while it was
// in flight joins it (MergePending).
//
// MergePending is the pipeline's tvrelease/merge.py, rule for rule (that module's
// docstring is the full text):
//   - a change's channels are a plan layer (pending.plan_channels, the newest channel
//     plan's definitions) and a hand layer (the fields a person changed over the plan's
//     definition, else the live one; a removal; a channel added by hand, whole);
//   - a newer plan replaces the older plan's part whole (channels, links, unlinks,
//     pools; the change set and digest are the row's); the hand layer is laid over it
//     field by field, a plan-made channel's edit moving to the newer plan's channel of
//     the same plan key;
//   - a plan never drops a hand edit; newer hand edits win over older ones field by
//     field; art per channel and kind, settings per section, filler per id;
//   - sched, edited and follow only grow (union); other keys: the newer's.

// PlanChannels is the pending key holding the newest channel plan's definitions.
const PlanChannels = "plan_channels"

var (
	planKeys  = []string{"links", "unlinks", "pools"}
	unionKeys = []string{"sched", "edited", "follow"}
	ownKeys   = map[string]bool{"channels": true, PlanChannels: true, "art": true, "settings": true,
		"filler": true, "links": true, "unlinks": true, "pools": true, "sched": true, "edited": true, "follow": true}
)

type handEntry struct {
	kind string // removed | whole | fields
	v    map[string]any
}

// WithPlanLayer is pending with its plan layer: a change made before the rule that
// holds a plan (a digest) but no plan_channels has only its plan's channels.
func WithPlanLayer(pending map[string]any, digest *string) map[string]any {
	if pending == nil {
		pending = map[string]any{}
	}
	if _, ok := pending[PlanChannels]; ok || digest == nil || *digest == "" {
		return pending
	}
	out := make(map[string]any, len(pending)+1)
	for k, v := range pending {
		out[k] = v
	}
	out[PlanChannels] = copyMap(mapOf(pending["channels"]))
	return out
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func handLayer(p map[string]any, live map[string]map[string]any) map[string]handEntry {
	plan := mapOf(p[PlanChannels])
	out := map[string]handEntry{}
	for id, raw := range mapOf(p["channels"]) {
		d, _ := raw.(map[string]any)
		pv, inPlan := plan[id]
		var base map[string]any
		if inPlan {
			base, _ = pv.(map[string]any)
		} else if l, ok := live[id]; ok {
			base = l
		}
		_, isLive := live[id]
		switch {
		case raw == nil || d == nil:
			if base != nil {
				out[id] = handEntry{kind: "removed"}
			}
		case base == nil || (inPlan && !isLive):
			if !reflect.DeepEqual(d, base) {
				out[id] = handEntry{kind: "whole", v: d}
			}
		default:
			f := map[string]any{}
			for k, v := range d {
				if bv, ok := base[k]; !ok || !reflect.DeepEqual(bv, v) {
					f[k] = v
				}
			}
			if len(f) > 0 {
				out[id] = handEntry{kind: "fields", v: f}
			}
		}
	}
	return out
}

func overHand(old *handEntry, n handEntry) handEntry {
	if old == nil || n.kind != "fields" {
		return n
	}
	switch old.kind {
	case "fields":
		return handEntry{kind: "fields", v: overlayMap(old.v, n.v)}
	case "whole":
		return handEntry{kind: "whole", v: overlayMap(old.v, n.v)}
	}
	return n
}

func overlayMap(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func planLinks(p map[string]any) map[string]string { // channel id -> key
	out := map[string]string{}
	xs, _ := p["links"].([]any)
	for _, x := range xs {
		m, _ := x.(map[string]any)
		k, _ := m["key"].(string)
		id, _ := m["channel_id"].(string)
		if k != "" && id != "" {
			out[id] = k
		}
	}
	return out
}

func remapPlan(old, n map[string]any) map[string]string {
	byKey := map[string]string{}
	for id, k := range planLinks(n) {
		byKey[k] = id
	}
	out := map[string]string{}
	for id, k := range planLinks(old) {
		if nid, ok := byKey[k]; ok && nid != id {
			out[id] = nid
		}
	}
	return out
}

// MergePending joins the update n into the change's pending edits old (live: the
// live channels' definitions the editor edits over).
func MergePending(old, n map[string]any, live map[string]map[string]any) map[string]any {
	if old == nil {
		old = map[string]any{}
	}
	if n == nil {
		n = map[string]any{}
	}
	_, newPlan := n[PlanChannels]
	src := old
	if newPlan {
		src = n
	}
	plan := copyMap(mapOf(src[PlanChannels]))
	hand := handLayer(old, live)
	if newPlan {
		if remap := remapPlan(old, n); len(remap) > 0 {
			oldPlan := mapOf(old[PlanChannels])
			for o, nid := range remap {
				h, ok := hand[o]
				if !ok {
					continue
				}
				delete(hand, o)
				if h.kind == "whole" {
					base, _ := oldPlan[o].(map[string]any)
					f := map[string]any{}
					for k, v := range h.v {
						if bv, ok := base[k]; k != "id" && (!ok || !reflect.DeepEqual(bv, v)) {
							f[k] = v
						}
					}
					if len(f) == 0 {
						continue
					}
					h = handEntry{kind: "fields", v: f}
				}
				hand[nid] = h
			}
			old = copyMap(old)
			art := map[string]any{}
			for id, v := range mapOf(old["art"]) {
				if nid, ok := remap[id]; ok {
					id = nid
				}
				art[id] = v
			}
			old["art"] = art
			for _, k := range unionKeys {
				ids := (&Pending{M: old}).list(k)
				for i, id := range ids {
					if nid, ok := remap[id]; ok {
						ids[i] = nid
					}
				}
				if _, had := old[k]; had {
					old[k] = ids
				}
			}
		}
	}
	for id, h := range handLayer(n, live) {
		var prev *handEntry
		if o, ok := hand[id]; ok {
			prev = &o
		}
		hand[id] = overHand(prev, h)
	}
	out := map[string]any{}
	for k, v := range old {
		if !ownKeys[k] {
			out[k] = v
		}
	}
	for k, v := range n {
		if !ownKeys[k] {
			out[k] = v
		}
	}
	for _, k := range planKeys {
		if v, ok := src[k]; ok {
			out[k] = v
		}
	}
	_, oldPlan := old[PlanChannels]
	if len(plan) > 0 || newPlan || oldPlan {
		out[PlanChannels] = plan
	}
	channels := copyMap(plan)
	for id, h := range hand {
		switch h.kind {
		case "removed":
			channels[id] = nil
		case "whole":
			channels[id] = h.v
		default:
			below, _ := plan[id].(map[string]any)
			if below == nil {
				below = live[id]
			}
			if below != nil {
				channels[id] = overlayMap(below, h.v)
			}
		}
	}
	if len(channels) > 0 {
		out["channels"] = channels
	}
	for _, k := range unionKeys {
		set := map[string]bool{}
		for _, id := range (&Pending{M: old}).list(k) {
			set[id] = true
		}
		for _, id := range (&Pending{M: n}).list(k) {
			set[id] = true
		}
		_, inOld := old[k]
		_, inNew := n[k]
		if len(set) == 0 && !inOld && !inNew {
			continue
		}
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		out[k] = ids
	}
	art := map[string]any{}
	for _, p := range []map[string]any{old, n} {
		for id, kinds := range mapOf(p["art"]) {
			art[id] = overlayMap(mapOf(art[id]), mapOf(kinds))
		}
	}
	if len(art) > 0 {
		out["art"] = art
	}
	if s := overlayMap(mapOf(old["settings"]), mapOf(n["settings"])); len(s) > 0 {
		out["settings"] = s
	}
	filler := map[string]any{}
	for _, part := range []string{"pools", "items"} {
		got := overlayMap(mapOf(mapOf(old["filler"])[part]), mapOf(mapOf(n["filler"])[part]))
		if len(got) > 0 {
			filler[part] = got
		}
	}
	if len(filler) > 0 {
		out["filler"] = filler
	}
	return out
}

// LiveDefs are the live lineup's definitions: the live release's snapshot
// (tv_release_channels.def, the shape the editor writes), empty before the first swap.
func (s *Store) LiveDefs(ctx context.Context, q queryer) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	rows, err := q.QueryContext(ctx, `SELECT rc.channel_id, rc.def FROM tv_release_channels rc
		JOIN tv_routing r ON r.live_release_id = rc.release_id WHERE r.scope = 'cluster'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var def []byte
		if err := rows.Scan(&id, &def); err != nil {
			return nil, err
		}
		m := map[string]any{}
		if json.Unmarshal(def, &m) == nil {
			out[id] = m
		}
	}
	return out, rows.Err()
}

// failToDraft is a failed verification (story 33.12 AC3, the owner's rule): the change
// goes back to draft - the open change again, its report and reasons kept, its
// approval withdrawn (the owner fixes it or approves it again), no stage asked - with
// a fail event. A draft opened while it was in flight joins it (MergePending), and
// ends superseded by it. Nothing ends "failed": that state stays for the rows made
// before this rule. A concurrent admin action (a cancel) wins silently.
func (c *Controller) failToDraft(ctx context.Context, ch *Change, detail map[string]any, reason string) error {
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if !IsSQLite(c.DB) {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey()); err != nil {
			return err
		}
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["reason"] = reason
	diff := copyMap(ch.Diff)
	delete(diff, "stage") // the owner stages it again: no retry loop
	fields := map[string]any{"approved_by": []string{}, "approved_at": (*time.Time)(nil), "diff": diff}
	if err := c.Store.Move(ctx, tx, ch, StateDraft, ActionFail, nil, detail, fields); err != nil {
		var ce *ConflictError
		if errors.As(err, &ce) {
			return nil
		}
		return err
	}
	ch.Diff, ch.ApprovedBy, ch.ApprovedAt = diff, []string{}, nil
	if err := c.absorbNewer(ctx, tx, ch); err != nil {
		return err
	}
	return tx.Commit()
}

// absorbNewer joins into ch the drafts no one approved that were opened after it (while
// it was in flight), oldest first.
func (c *Controller) absorbNewer(ctx context.Context, tx *sql.Tx, ch *Change) error {
	rows, err := tx.QueryContext(ctx, `SELECT `+changeCols+` FROM tv_changes WHERE state = 'draft'
		AND id <> $1 AND number > $2 ORDER BY number`, ch.ID, ch.Number)
	if err != nil {
		return err
	}
	var newer []*Change
	for rows.Next() {
		n, err := scanChange(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		if len(n.ApprovedBy) == 0 {
			newer = append(newer, n)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil || len(newer) == 0 {
		return err
	}
	live, err := c.Store.LiveDefs(ctx, tx)
	if err != nil {
		return err
	}
	pending := WithPlanLayer(ch.Pending().M, ch.Digest)
	fields := map[string]any{}
	for _, n := range newer {
		pending = MergePending(pending, WithPlanLayer(n.Pending().M, n.Digest), live)
		if n.Digest != nil {
			// the newer plan's: its approval is the queue's Apply of that digest
			fields["digest"], fields["source"] = *n.Digest, n.Source
			if len(n.ChangeSet) > 0 {
				fields["change_set"] = string(n.ChangeSet)
			}
		}
		at := c.now()
		if err := c.Store.Move(ctx, tx, n, StateSuperseded, ActionSupersede, nil,
			map[string]any{"joined": ch.ID, "number": ch.Number},
			map[string]any{"superseded_by": ch.ID, "ended_at": at, "end_reason": "joined_change"}); err != nil {
			return err
		}
		st := StateDraft
		if err := c.Store.Event(ctx, tx, ch.ID, Event{ActorSource: "controller", FromState: &st, ToState: &st,
			Action: "append", Detail: map[string]any{"what": "joined_change", "kind": n.SourceKind(),
				"change_id": n.ID, "number": n.Number}}); err != nil {
			return err
		}
	}
	pd := &Pending{M: pending}
	diff := copyMap(ch.Diff)
	diff["pending"] = pending
	diff["summary"] = Summarize(pd)
	fields["diff"] = diff
	ch.Diff = diff
	return c.Store.Set(ctx, tx, ch.ID, fields)
}
