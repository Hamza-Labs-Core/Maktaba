package components

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Per-machine components (story 51.12). An override is one component_overrides row:
// (node, id, want, hold). Its want, when set, wins over the home's on that machine; its
// hold stops the component there (a hold on the home stops it everywhere, so both must
// be cleared for it to run). Mostly an override narrows: it turns off or holds on one
// machine what the home has on. It may also turn on, for one machine, a placement "any"
// component the home has off; never a placement "home" one (409 home_only), never a
// core.* one (409 not_switchable).
//
// A component with stages is written twice in one transaction: the override row, and
// one cluster_node_stages row per stage (enabled = false while the override keeps it off
// or holds it; no row while it follows the home or turns it on). A machine on a release
// before Epic 51 has no component runner but obeys those rows (switched_off), and the
// Machines page's stage table shows what the component switch says.

// WantFollow is a machine change's want that clears the override's want: the machine
// follows the home again.
const WantFollow = "follow"

// Refusal codes of the per-machine changes.
const (
	CodeHomeOnly       = "home_only"
	CodeUnknownMachine = "unknown_machine"
	CodeMixedMachines  = "mixed_machines"
)

// StaleAfter is how long a machine may stay silent before its status shows as old.
const StaleAfter = 90 * time.Second

// overrideAction is the audit action of every per-machine change.
const overrideAction = "component.override"

// machineOf is the machine every change of req names ("" for the home). A request that
// mixes machines, or applies a bundle to one, is refused.
func machineOf(req Request) (string, error) {
	node := ""
	for i, ch := range req.Changes {
		m := strings.ToLower(strings.TrimSpace(ch.Machine))
		if i > 0 && m != node {
			return "", refuse(http.StatusBadRequest, "", CodeMixedMachines, "the changes of one request are for one machine (or all for the home)")
		}
		node = m
	}
	if node != "" && req.Bundle != "" {
		return "", refuse(http.StatusBadRequest, "", CodeBadBundle, "a bundle is applied to the home; a machine's components are chosen one by one")
	}
	return node, nil
}

// Machine is the cluster_nodes row of a machine, as the components read it.
type Machine struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	State    string     `json:"state"`
	Platform string     `json:"platform,omitempty"`  // darwin-arm64 ... from its capabilities
	MemoryMB int64      `json:"memory_mb,omitempty"` // from its capabilities (0: unknown)
	GPU      bool       `json:"gpu"`                 // an NVIDIA GPU or Apple Silicon
	HeardAt  *time.Time `json:"heard_at,omitempty"`  // its last heartbeat or status report
	Stale    bool       `json:"stale"`               // silent for longer than StaleAfter
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// machine reads node (a refusal 404 unknown_machine when there is none, or it was
// rejected or revoked: those take no overrides).
func machine(ctx context.Context, q queryRower, node string) (Machine, error) {
	if _, err := uuid.Parse(node); err != nil {
		return Machine{}, refuse(http.StatusNotFound, "", CodeUnknownMachine, "no machine "+node)
	}
	var m Machine
	var caps, seen sql.NullString
	err := q.QueryRowContext(ctx, `SELECT CAST(id AS TEXT), name, state, CAST(capabilities AS TEXT),
		CAST(last_seen_at AS TEXT) FROM cluster_nodes WHERE CAST(id AS TEXT) = $1`, node).
		Scan(&m.ID, &m.Name, &m.State, &caps, &seen)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && m.State != cluster.StatePending && m.State != cluster.StateApproved) {
		return Machine{}, refuse(http.StatusNotFound, "", CodeUnknownMachine, "no machine "+node)
	}
	if err != nil {
		return Machine{}, err
	}
	if seen.Valid {
		if t, err := parseTime(seen.String); err == nil {
			m.HeardAt = &t
		}
	}
	var c struct {
		OS           string   `json:"os"`
		Arch         string   `json:"arch"`
		MemoryGB     *float64 `json:"memory_gb"`
		AppleSilicon bool     `json:"apple_silicon"`
		GPUs         []any    `json:"gpus"`
	}
	if caps.Valid && json.Unmarshal([]byte(caps.String), &c) == nil {
		m.Platform = platformOf(c.OS, c.Arch)
		if c.MemoryGB != nil {
			m.MemoryMB = int64(*c.MemoryGB * 1024)
		}
		m.GPU = c.AppleSilicon || len(c.GPUs) > 0
	}
	return m, nil
}

// platformOf is the catalog's name of an os and arch as the machine reports them
// ("" when either is unknown).
func platformOf(os, arch string) string {
	os = strings.ToLower(os)
	switch strings.ToLower(arch) {
	case "arm64", "aarch64":
		arch = "arm64"
	case "x86_64", "amd64", "x64":
		arch = "amd64"
	default:
		return ""
	}
	if os == "" {
		return ""
	}
	return os + "-" + arch
}

// stageState is a machine's stage switches (cluster_node_stages) and the stages the
// registry knows (job_stages: a switch needs its row).
type stageState struct {
	switches map[string]bool
	known    map[string]bool
}

func readStages(ctx context.Context, q queryRower, node string) (stageState, error) {
	st := stageState{switches: map[string]bool{}, known: map[string]bool{}}
	rows, err := q.QueryContext(ctx, `SELECT stage, enabled FROM cluster_node_stages WHERE CAST(node_id AS TEXT) = $1`, node)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var stage string
		var on bool
		if err := rows.Scan(&stage, &on); err != nil {
			_ = rows.Close()
			return st, err
		}
		st.switches[stage] = on
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = q.QueryContext(ctx, `SELECT stage FROM job_stages`)
	if err != nil {
		return st, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var stage string
		if err := rows.Scan(&stage); err != nil {
			return st, err
		}
		st.known[stage] = true
	}
	return st, rows.Err()
}

// overrideOff reports whether o keeps its component off on its machine, or holds it.
func overrideOff(o store.Override) bool {
	return o.Want == components.WantInstalled || o.Want == components.WantAbsent || o.Hold != nil
}

// stageWrites are the stage switches o makes for c (nil: c has no stage the registry
// knows): enabled = false for each while o keeps c off or holds it, no row otherwise.
func stageWrites(c *components.Component, o store.Override, st stageState) map[string]*bool {
	var out map[string]*bool
	off := overrideOff(o)
	for _, stage := range c.Stages {
		if !st.known[stage] {
			continue
		}
		if out == nil {
			out = map[string]*bool{}
		}
		if off {
			f := false
			out[stage] = &f
		} else {
			out[stage] = nil
		}
	}
	return out
}

// stagesMatch reports whether the machine's switches already are what w makes.
func stagesMatch(w map[string]*bool, st stageState) bool {
	for stage, on := range w {
		cur, has := st.switches[stage]
		if on == nil && has || on != nil && (!has || cur != *on) {
			return false
		}
	}
	return true
}

type overrideWrite struct {
	o      store.Override
	audit  store.Audit
	stages map[string]*bool
}

type machinePlan struct {
	writes   []overrideWrite
	ov       map[string]store.Override // the overrides after the plan
	preview  Preview
	goingOff []string
}

func cloneOverrides(ov map[string]store.Override) map[string]store.Override {
	out := make(map[string]store.Override, len(ov))
	for id, o := range ov {
		out[id] = o
	}
	return out
}

// makeMachinePlan is makePlan for machine node: each change is checked against that
// machine's graph (the home's rows merged with its overrides, as the earlier changes
// leave them).
func makeMachinePlan(cat *components.Catalog, licensed func(string) bool, tier string, now time.Time, home map[string]store.Desired,
	ov map[string]store.Override, st stageState, rev int64, node string, req Request, actor Actor, collect bool) (*machinePlan, error) {
	if licensed == nil {
		licensed = func(string) bool { return true }
	}
	p := &machinePlan{ov: cloneOverrides(ov), preview: Preview{Effects: []Effect{}, Conflicts: []string{}, Refused: []Refusal{}}}
	fail := func(r *Refusal) error {
		if !collect {
			return r
		}
		p.preview.Refused = append(p.preview.Refused, *r)
		return nil
	}
	if req.Generation != nil && *req.Generation != rev {
		r := refuse(http.StatusConflict, "", CodeStale, fmt.Sprintf("the components changed since revision %d (now %d)", *req.Generation, rev))
		r.Revision = &rev
		if err := fail(r); err != nil {
			return nil, err
		}
	}
	audit := store.Audit{By: actor.By, ActorUserID: actor.UserID, ActorSource: actor.Source, Action: overrideAction, Node: node}
	write := func(c *components.Component, next store.Override) {
		p.ov[c.ID] = next
		p.writes = append(p.writes, overrideWrite{o: next, audit: audit, stages: stageWrites(c, next, st)})
	}
	for _, ch := range req.Changes {
		c, ok := cat.Get(ch.ID)
		if !ok {
			if err := fail(refuse(http.StatusNotFound, ch.ID, CodeUnknown, "no component "+ch.ID)); err != nil {
				return nil, err
			}
			continue
		}
		if !c.Switchable {
			if err := fail(refuse(http.StatusConflict, ch.ID, CodeNotSwitchable, ch.ID+" is part of Maktaba's core and cannot be changed")); err != nil {
				return nil, err
			}
			continue
		}
		if (ch.Want == "" && ch.Hold == nil) || (ch.Want != "" && ch.Want != WantFollow && !validWant(ch.Want)) {
			if err := fail(refuse(http.StatusBadRequest, ch.ID, CodeBadWant, fmt.Sprintf("want %q: enabled, installed, absent or follow", ch.Want))); err != nil {
				return nil, err
			}
			continue
		}
		cur, has := p.ov[ch.ID]
		if ch.Generation != nil && *ch.Generation != cur.Generation {
			r := refuse(http.StatusConflict, ch.ID, CodeStale, fmt.Sprintf("%s on this machine changed since generation %d (now %d)", ch.ID, *ch.Generation, cur.Generation))
			if err := fail(r); err != nil {
				return nil, err
			}
			continue
		}
		next := store.Override{Node: node, ID: ch.ID, Want: cur.Want, Hold: cur.Hold, Generation: cur.Generation}
		switch ch.Want {
		case "":
		case WantFollow:
			next.Want = ""
		default:
			next.Want = ch.Want
		}
		if ch.Hold != nil {
			switch {
			case *ch.Hold && next.Hold == nil:
				next.Hold = &store.Hold{By: actor.By, At: now}
			case !*ch.Hold:
				next.Hold = nil
			}
		}
		if next.Want == components.WantEnabled && c.Placement == "home" {
			if err := fail(refuse(http.StatusConflict, ch.ID, CodeHomeOnly, ch.ID+" runs only on the home")); err != nil {
				return nil, err
			}
			continue
		}
		before := store.Merge(home, p.ov)
		trial := cloneOverrides(p.ov)
		trial[ch.ID] = next
		was, will := wantOf(before, ch.ID), wantOf(store.Merge(home, trial), ch.ID)
		turningOn := will == components.WantEnabled && was != components.WantEnabled
		if turningOn && c.Licence != "" && !licensed(c.Licence) {
			r := licenceRefusal(c, tier)
			if err := fail(r); err != nil {
				return nil, err
			}
			continue
		}
		var others []string
		if turningOn {
			for _, x := range conflictsOf(cat, ch.ID) {
				if wantOf(before, x) == components.WantEnabled {
					others = append(others, x)
				}
			}
		}
		if len(others) > 0 && !req.WithConflicts {
			r := refuse(http.StatusConflict, ch.ID, CodeConflicts, ch.ID+" cannot run with what is on on this machine")
			r.Conflicts = others
			p.preview.Conflicts = appendNew(p.preview.Conflicts, others...)
			if err := fail(r); err != nil {
				return nil, err
			}
			continue
		}
		if will == components.WantAbsent && was != components.WantAbsent {
			if by := requiredBy(cat, before, ch.ID); len(by) > 0 {
				r := refuse(http.StatusConflict, ch.ID, CodeRequiredBy, ch.ID+" is required by components that are on on this machine")
				r.RequiredBy = by
				if err := fail(r); err != nil {
					return nil, err
				}
				continue
			}
		}
		for _, x := range others {
			oc, _ := cat.Get(x)
			o := p.ov[x]
			write(oc, store.Override{Node: node, ID: x, Want: components.WantInstalled, Hold: o.Hold, Generation: o.Generation})
			p.preview.Conflicts = appendNew(p.preview.Conflicts, x)
		}
		same := next.Want == cur.Want && (next.Hold == nil) == (cur.Hold == nil) && (has || (next.Want == "" && next.Hold == nil))
		if same && stagesMatch(stageWrites(c, next, st), st) {
			continue // nothing changes
		}
		write(c, next)
	}
	before, after := gatesLicensed(cat, store.Merge(home, ov), licensed), gatesLicensed(cat, store.Merge(home, p.ov), licensed)
	p.preview.Effects = effects(cat, before, after)
	for _, id := range cat.Order() {
		if before[id].on && !after[id].on {
			p.goingOff = append(p.goingOff, id)
			c, _ := cat.Get(id)
			grace := c.StopGraceS
			if c.HasPart(components.KindProcess) {
				grace = max(grace, components.DefaultStopGraceS)
			}
			if c.HasPart(components.KindStages) {
				grace = max(grace, c.Drain.GraceS)
			}
			if grace > p.preview.DrainS {
				p.preview.DrainS = grace
			}
		}
	}
	return p, nil
}

func (s *Service) previewMachine(ctx context.Context, node string, req Request) (Preview, error) {
	if _, err := machine(ctx, s.Store.DB, node); err != nil {
		return Preview{}, err
	}
	home, rev, err := s.Store.Desired(ctx, store.Home)
	if err != nil {
		return Preview{}, err
	}
	ov, err := s.Store.Overrides(ctx, node)
	if err != nil {
		return Preview{}, err
	}
	st, err := readStages(ctx, s.Store.DB, node)
	if err != nil {
		return Preview{}, err
	}
	p, err := makeMachinePlan(s.cat(), s.licensed, s.tier(), s.now(), home, ov, st, rev, node, req, Actor{By: "preview"}, true)
	if err != nil {
		return Preview{}, err
	}
	return p.preview, nil
}

// applyMachine makes every change of req on machine node in one transaction, or none:
// the override rows, their audit rows and the stage switches together.
func (s *Service) applyMachine(ctx context.Context, node string, req Request, actor Actor) (Result, error) {
	var p *machinePlan
	err := s.machineWrite(ctx, node, func(w *store.MachineWrite, st stageState) ([]overrideWrite, error) {
		var err error
		p, err = makeMachinePlan(s.cat(), s.licensed, s.tier(), s.now(), w.Home, w.Overrides, st, w.Revision, node, req, actor, false)
		if err != nil {
			return nil, err
		}
		if len(p.writes) > 1 {
			batch := uuid.NewString()
			for i := range p.writes {
				p.writes[i].audit.BatchID = batch
			}
		}
		return p.writes, nil
	})
	if err != nil {
		return Result{}, err
	}
	res := Result{Changed: []store.Desired{}, Effect: p.preview, Overrides: []store.Override{}}
	for _, w := range p.writes {
		res.Overrides = append(res.Overrides, w.o)
	}
	if len(p.writes) > 1 {
		res.BatchID = p.writes[0].audit.BatchID
	}
	res.Revision, _ = s.Store.Revision(ctx)
	return res, nil
}

// machineWrite runs plan in node's write transaction and writes what it answers: each
// override (with its audit row) and its stage switches. A plan that writes nothing
// commits nothing.
func (s *Service) machineWrite(ctx context.Context, node string, plan func(*store.MachineWrite, stageState) ([]overrideWrite, error)) error {
	w, err := s.Store.BeginMachineWrite(ctx, node)
	if err != nil {
		return err
	}
	defer func() { _ = w.Tx.Rollback() }()
	if _, err := machine(ctx, w.Tx, node); err != nil {
		return err
	}
	st, err := readStages(ctx, w.Tx, node)
	if err != nil {
		return err
	}
	writes, err := plan(w, st)
	if err != nil {
		return err
	}
	if len(writes) == 0 {
		return nil
	}
	stages := false
	for i, ow := range writes {
		after, err := s.Store.SetOverrideTx(ctx, w.Tx, ow.o, ow.audit)
		if err != nil {
			return err
		}
		writes[i].o = after
		if len(ow.stages) > 0 {
			if err := cluster.SetStagesTx(ctx, w.Tx, node, ow.stages); err != nil {
				return fmt.Errorf("components: the stage switches of %s: %w", ow.o.ID, err)
			}
			stages = true
		}
	}
	if err := w.Tx.Commit(); err != nil {
		return err
	}
	if stages && s.OnMachineChanged != nil {
		s.OnMachineChanged(node)
	}
	return nil
}

// WorkerOffer says whether the approve form offers c to a worker machine: a pipeline
// component (stages or a process) of the Worker machine bundle that can run on a
// machine (placement any) and is on at home.
func WorkerOffer(c *components.Component, home map[string]store.Desired) bool {
	return c.Switchable && slices.Contains(c.Bundles, "worker") && c.Placement != "home" &&
		(len(c.Stages) > 0 || c.HasPart(components.KindProcess)) && wantOf(home, c.ID) == components.WantEnabled
}

// WorkerChoice is a machine's components chosen as it is approved (POST
// /api/cluster/nodes/{id}/approve with components): every component the form offers
// (WorkerOffer) and keep leaves out is off on that machine (an override, want
// installed, with its stage switches). An id the catalog does not have is refused.
func (s *Service) WorkerChoice(ctx context.Context, node string, keep []string, actor Actor) error {
	node = strings.ToLower(strings.TrimSpace(node))
	cat := s.cat()
	for _, id := range keep {
		if _, ok := cat.Get(id); !ok {
			return refuse(http.StatusBadRequest, id, CodeUnknown, "no component "+id)
		}
	}
	if strings.TrimSpace(actor.By) == "" {
		return errors.New("components: a change needs who makes it")
	}
	return s.machineWrite(ctx, node, func(w *store.MachineWrite, st stageState) ([]overrideWrite, error) {
		var out []overrideWrite
		audit := store.Audit{By: actor.By, ActorUserID: actor.UserID, ActorSource: actor.Source, Action: overrideAction,
			Node: node, BatchID: uuid.NewString()}
		for _, c := range cat.Components() {
			if !WorkerOffer(c, w.Home) || slices.Contains(keep, c.ID) {
				continue
			}
			cur := w.Overrides[c.ID]
			next := store.Override{Node: node, ID: c.ID, Want: components.WantInstalled, Hold: cur.Hold}
			out = append(out, overrideWrite{o: next, audit: audit, stages: stageWrites(c, next, st)})
		}
		return out, nil
	})
}

// StagesSwitched keeps the component switches in step with the Machines page's stage
// table (PUT /api/cluster/nodes/{id} with stages): a stage turned back on (true, or
// null: automatic) whose component is off or held on that machine by its override makes
// the override follow the home again, and the stages still switched off stay so. The
// component then reads "On, 1 stage off" there, naming the stage. Stages turned off
// change no override.
func (s *Service) StagesSwitched(ctx context.Context, node string, stages map[string]*bool, actor Actor) error {
	node = strings.ToLower(strings.TrimSpace(node))
	cat := s.cat()
	var ids []string
	for stage, on := range stages {
		if on != nil && !*on {
			continue
		}
		if c, ok := cat.ByStage(stage); ok && c.Switchable && !slices.Contains(ids, c.ID) {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if strings.TrimSpace(actor.By) == "" {
		actor.By = "admin"
	}
	slices.Sort(ids)
	return s.machineWrite(ctx, node, func(w *store.MachineWrite, _ stageState) ([]overrideWrite, error) {
		var out []overrideWrite
		audit := store.Audit{By: actor.By, ActorUserID: actor.UserID, ActorSource: actor.Source, Action: overrideAction, Node: node}
		for _, id := range ids {
			cur, has := w.Overrides[id]
			if !has || !overrideOff(cur) {
				continue
			}
			next := cur
			if next.Want != components.WantEnabled {
				next.Want = ""
			}
			next.Hold = nil
			out = append(out, overrideWrite{o: next, audit: audit}) // the stage table stays as the admin set it
		}
		return out, nil
	})
}

// ResolveMachine is the id of the machine named (or with the id) nameOrID: the CLI's
// --machine. Two machines with that name are refused: use the id.
func (s *Service) ResolveMachine(ctx context.Context, nameOrID string) (string, error) {
	want := strings.TrimSpace(nameOrID)
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT CAST(id AS TEXT), name FROM cluster_nodes
		WHERE state IN ('pending', 'approved') ORDER BY name`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var found []string
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return "", err
		}
		if strings.EqualFold(id, want) {
			return id, nil
		}
		if name == want {
			found = append(found, id)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", refuse(http.StatusNotFound, "", CodeUnknownMachine, "no machine "+want)
	}
	return "", refuse(http.StatusBadRequest, "", CodeUnknownMachine, fmt.Sprintf("%d machines are named %s: use the id", len(found), want))
}

// MachineState is one component on one machine.
type MachineState struct {
	// Want, Hold and Generation are the machine's effective wish (the home's row with
	// the override applied; component_desired_for); Override is the row itself (nil:
	// none, the machine follows the home).
	Want       string          `json:"want"`
	Hold       *store.Hold     `json:"hold"`
	Generation int64           `json:"generation"`
	Override   *store.Override `json:"override"`
	// Phase and Reason are the machine's status (the worst of its parts); with no
	// status row, what its effective wish makes of it (Expected).
	Phase    string `json:"phase"`
	Reason   string `json:"reason,omitempty"`
	Expected Effect `json:"expected"`
	Reported bool   `json:"reported"`
	// Stale: the machine has been silent for longer than StaleAfter; the status is its
	// last.
	Stale     bool       `json:"stale"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// StagesOff are the component's stages switched off on the machine's stage table
	// while the component itself is on there ("On, 1 stage off").
	StagesOff []string `json:"stages_off"`
	// WorkerOffer: the approve form offers it (WorkerOffer).
	WorkerOffer bool `json:"worker_offer"`
	// RAMMB and GPU are what it needs; CannotRun why this machine cannot run it
	// (not_enough_memory, platform_unsupported; "": it can, or nothing is known).
	RAMMB     int    `json:"ram_mb,omitempty"`
	GPU       string `json:"gpu,omitempty"`
	CannotRun string `json:"cannot_run,omitempty"`
}

// MachineComponent is a component as one machine's Components section shows it: the
// home's view, and Here.
type MachineComponent struct {
	View
	Here MachineState `json:"here"`
}

// MachineList is GET /api/components?machine=<node_id>.
type MachineList struct {
	Revision   int64              `json:"revision"`
	Machine    Machine            `json:"machine"`
	Components []MachineComponent `json:"components"`
	// WorkerOn of WorkerOffered: "Worker machine: 3 of 8".
	WorkerOn      int `json:"worker_on"`
	WorkerOffered int `json:"worker_offered"`
}

// ListMachine is every component with machine node's effective wish, override and
// status (a refusal 404 unknown_machine for a machine there is none of).
func (s *Service) ListMachine(ctx context.Context, node string) (MachineList, error) {
	node = strings.ToLower(strings.TrimSpace(node))
	m, err := machine(ctx, s.Store.DB, node)
	if err != nil {
		return MachineList{}, err
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		return MachineList{}, err
	}
	ov, err := s.Store.Overrides(ctx, node)
	if err != nil {
		return MachineList{}, err
	}
	st, err := readStages(ctx, s.Store.DB, node)
	if err != nil {
		return MachineList{}, err
	}
	merged := store.Merge(snap.rows, ov)
	mg := gatesLicensed(s.cat(), merged, s.licensed)
	for _, rows := range snap.statuses {
		for _, r := range rows {
			if r.Machine == node && !r.UpdatedAt.IsZero() && (m.HeardAt == nil || r.UpdatedAt.After(*m.HeardAt)) {
				t := r.UpdatedAt
				m.HeardAt = &t
			}
		}
	}
	m.Stale = m.State == cluster.StateApproved && (m.HeardAt == nil || s.now().Sub(*m.HeardAt) > StaleAfter)
	out := MachineList{Revision: snap.rev, Machine: m, Components: []MachineComponent{}}
	for _, c := range s.cat().Components() {
		mc := MachineComponent{View: s.view(snap, c, false)}
		h := &mc.Here
		h.Want, h.Generation = components.WantEnabled, 0
		if d, ok := merged[c.ID]; ok && c.Switchable {
			h.Want, h.Hold, h.Generation = d.Want, d.Hold, d.Generation
		}
		if o, ok := ov[c.ID]; ok {
			oc := o
			h.Override = &oc
		}
		g := mg[c.ID]
		h.Expected = Effect{ID: c.ID, To: g.phase, Reason: g.reason}
		h.Phase, h.Reason = g.phase, g.reason
		var mine []store.Status
		for _, r := range snap.statuses[c.ID] {
			if r.Machine == node {
				mine = append(mine, r)
			}
		}
		if len(mine) > 0 {
			roll := store.Rollup(mine)
			h.Phase, h.Reason, h.Reported = roll.Phase, roll.Reason(), true
			var at time.Time
			for _, r := range mine {
				if r.UpdatedAt.After(at) {
					at = r.UpdatedAt
				}
			}
			h.UpdatedAt = &at
			h.Stale = m.Stale
		}
		h.StagesOff = []string{}
		if g.on {
			for _, stage := range c.Stages {
				if on, has := st.switches[stage]; has && !on {
					h.StagesOff = append(h.StagesOff, stage)
				}
			}
		}
		h.WorkerOffer = WorkerOffer(c, snap.rows)
		if h.WorkerOffer {
			out.WorkerOffered++
			if g.on {
				out.WorkerOn++
			}
		}
		h.RAMMB, h.GPU = c.Resources.RAMMB, c.Resources.GPU
		switch {
		case m.Platform != "" && len(c.Platforms) > 0 && !slices.Contains(c.Platforms, m.Platform):
			h.CannotRun = "platform_unsupported"
		case m.MemoryMB > 0 && c.Resources.RAMMB > 0 && m.MemoryMB < int64(c.Resources.RAMMB):
			h.CannotRun = "not_enough_memory"
		}
		out.Components = append(out.Components, mc)
	}
	return out, nil
}
