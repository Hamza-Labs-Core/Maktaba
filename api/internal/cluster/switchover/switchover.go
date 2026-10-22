// Package switchover runs a switch of the home from the home's API (specs/epics/
// 37-node-replica, "Switching the home"): another machine of the cluster, whose copy
// follows this home, becomes the home, and this one one of its machines. Planned, in
// order: the checks; every machine's jobs drained; the database frozen with the
// switch's marker as its last write (this machine's supervisor); the marker on the
// machine taking over; the kit (what replication leaves out, sealed for that machine)
// sent over the cluster channel; its promotion; every other machine told where its home
// is now; this machine made one of them. The new home finishes it (Finish): the machines
// followed, their jobs back, the integrations moved and checked. Nothing is a manual
// step; what only a person can do (a sign-in in a browser) is shown as such.
package switchover

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
)

// Hub is the cluster channel as a switch uses it (agent.Hub).
type Hub interface {
	MachineAgent(nodeID string) string
	// MachineAgents are the connected machines' agent sessions, by machine.
	MachineAgents() map[string]string
	HomeProcesses() []string
	CommandWithin(ctx context.Context, sessionID, name string, args json.RawMessage, by string, wait time.Duration) (agent.CmdResult, error)
}

// Supervisor is this machine's supervisor's switchover endpoints.
type Supervisor interface {
	Call(ctx context.Context, step string, req, out any) error
}

// Steps of a planned switch, in order.
const (
	StepCheck   = "check"
	StepDrain   = "drain"
	StepFreeze  = "freeze"
	StepMarker  = "marker"
	StepKit     = "kit"
	StepPromote = "promote"
	StepRepoint = "repoint"
	StepDemote  = "demote"
)

// Step is one step's state.
type Step struct {
	Name   string    `json:"name"`
	State  string    `json:"state"` // running | done | failed
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// Progress is a switch as this home runs it.
type Progress struct {
	ID       string                 `json:"id"`
	Term     int64                  `json:"term"`
	Kind     string                 `json:"kind"`
	ToNode   string                 `json:"to_node"`
	ToName   string                 `json:"to_name"`
	FromName string                 `json:"from_name"`
	State    string                 `json:"state"`
	Detail   string                 `json:"detail,omitempty"`
	Steps    []Step                 `json:"steps"`
	Machines map[string]Machine     `json:"machines"`
	Started  time.Time              `json:"started"`
	NewHome  *NewHome               `json:"new_home,omitempty"`
	Integr   map[string]Integration `json:"integrations,omitempty"`
	// PromoteSent: the promotion was asked (from then this home is never made writable
	// again but on the machine's word); ToURLs: where that machine answers as the home
	PromoteSent bool     `json:"promote_sent,omitempty"`
	ToURLs      []string `json:"to_urls,omitempty"`
	// ThawPending: the switch was undone but this home could not be made writable again
	// yet; it is asked again (retryThaw), after a restart too
	ThawPending bool `json:"thaw_pending,omitempty"`
}

// Machine is a machine's part in a switch.
type Machine struct {
	Name       string `json:"name"`
	State      string `json:"state"` // waiting | following | copying_anew | offline | failed
	Detail     string `json:"detail,omitempty"`
	PriorDrain bool   `json:"prior_drain"`
}

// Integration is an outside service's state after a switch. What needs a person is a
// checklist on the new home: Done once they say they did it (a check that can tell, the
// model runtime, says so itself: Moved).
type Integration struct {
	State  string `json:"state"` // moved | pending | failed | needs_you | not_used
	Detail string `json:"detail,omitempty"`
	Done   bool   `json:"done,omitempty"`
}

// NewHome is where the machines find the new home (the machine taking over's answer).
type NewHome struct {
	APIURL     string `json:"api_url"`
	WebURL     string `json:"web_url"`
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	SystemID   string `json:"system_id"`
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`
	// CA is the new home's database CA (PEM), pinned by the machines that follow it;
	// TLSOff: its database has no TLS (its own [postgres] tls = "off").
	CA     string `json:"ca_pem,omitempty"`
	TLSOff bool   `json:"tls_off,omitempty"`
}

// Orchestrator runs switches on the home.
type Orchestrator struct {
	DB         *sql.DB
	Hub        Hub
	Supervisor Supervisor
	Logger     *slog.Logger
	// HomeName is this home's name (the term's), SchemaRev its schema.
	HomeName  string
	SchemaRev int
	// JobsWait is how long the machines' jobs may take to end before they are paused
	// (0: 2 minutes).
	JobsWait time.Duration
	// HomeWait is how long the new home may take to answer as the home after its
	// promotion (it starts again: 0, 5 minutes) before the machines are told anyway.
	HomeWait time.Duration
	// ClusterKeyFile is the home's cluster key: an old home's report of what it kept aside
	// is sealed to it.
	ClusterKeyFile string
	// RetryEvery: retryEvery (0: 10 s).
	RetryEvery time.Duration

	// startMu makes a start's check and its row one step
	startMu     sync.Mutex
	mu          sync.Mutex
	progress    map[string]*Progress
	standbyWake chan struct{}
	// cancels are the switches an admin called off (Cancel); promoteWaits end the wait for
	// a promotion's answer once one is
	cancels      map[string]*cancelReq
	promoteWaits map[string]context.CancelFunc
	wakes        map[string]chan struct{}
	// checks are the outside services' checks (Finish's), run again for the checklist
	checks Integrations
}

func (o *Orchestrator) log() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}

// Reasons a machine cannot take over (the web shows them, translated).
const (
	ReasonNotApproved  = "not_approved"
	ReasonNotFull      = "not_full_node"
	ReasonNoCopy       = "copy_not_following"
	ReasonCopyStale    = "copy_not_reporting"
	ReasonSchema       = "schema_differs"
	ReasonAgentOffline = "agent_offline"
	ReasonBusy         = "switch_under_way"
)

// Candidate is a machine that could take over, and why it cannot now.
type Candidate struct {
	NodeID  string   `json:"node_id"`
	Name    string   `json:"name"`
	Ready   bool     `json:"ready"`
	Reasons []string `json:"reasons,omitempty"`
	LagMS   *int64   `json:"lag_ms,omitempty"`
}

type nodeRow struct {
	id, name, state, pubkey string
	full, drain             bool
	replica                 json.RawMessage
	reported                sql.NullTime
}

func (o *Orchestrator) nodes(ctx context.Context) ([]nodeRow, error) {
	rows, err := o.DB.QueryContext(ctx, `SELECT id::text, name, state, node_pubkey, full_node, drain,
	    COALESCE(replica, '{}'::jsonb), replica_reported_at FROM cluster_nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []nodeRow
	for rows.Next() {
		var n nodeRow
		if err := rows.Scan(&n.id, &n.name, &n.state, &n.pubkey, &n.full, &n.drain, &n.replica, &n.reported); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Candidates are the machines that could take over, with why not.
func (o *Orchestrator) Candidates(ctx context.Context) ([]Candidate, error) {
	ns, err := o.nodes(ctx)
	if err != nil {
		return nil, err
	}
	busy := o.active() != nil
	if !busy {
		// one under way here or on the new home (the database's row)
		_ = o.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cluster_switchovers
		    WHERE state NOT IN ('done', 'failed', 'cancelled'))`).Scan(&busy)
	}
	var out []Candidate
	for _, n := range ns {
		if n.state == "revoked" || n.state == "rejected" {
			continue
		}
		c := Candidate{NodeID: n.id, Name: n.name}
		var r struct {
			State       string `json:"state"`
			LagMS       *int64 `json:"lag_ms"`
			LocalSchema int64  `json:"local_schema"`
		}
		_ = json.Unmarshal(n.replica, &r)
		c.LagMS = r.LagMS
		switch {
		case n.state != "approved":
			c.Reasons = append(c.Reasons, ReasonNotApproved)
		case !n.full:
			c.Reasons = append(c.Reasons, ReasonNotFull)
		default:
			if r.State != "following" {
				c.Reasons = append(c.Reasons, ReasonNoCopy)
			} else if !n.reported.Valid || time.Since(n.reported.Time) > 2*time.Minute {
				c.Reasons = append(c.Reasons, ReasonCopyStale)
			}
			if o.SchemaRev > 0 && r.LocalSchema != int64(o.SchemaRev) {
				c.Reasons = append(c.Reasons, ReasonSchema)
			}
			if o.Hub == nil || o.Hub.MachineAgent(n.id) == "" {
				c.Reasons = append(c.Reasons, ReasonAgentOffline)
			}
		}
		if busy {
			c.Reasons = append(c.Reasons, ReasonBusy)
		}
		c.Ready = len(c.Reasons) == 0
		out = append(out, c)
	}
	return out, nil
}

func (o *Orchestrator) active() *Progress {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, p := range o.progress {
		if p.State != "done" && p.State != "failed" && p.State != "handed_over" {
			cp := *p
			return &cp
		}
	}
	return nil
}

// Progress is a switch this home ran (nil: none known here).
func (o *Orchestrator) Progress(id string) *Progress {
	o.mu.Lock()
	defer o.mu.Unlock()
	if p, ok := o.progress[id]; ok {
		cp := *p
		cp.Steps = append([]Step(nil), p.Steps...)
		cp.Machines = map[string]Machine{}
		for k, v := range p.Machines {
			cp.Machines[k] = v
		}
		return &cp
	}
	return nil
}

// ErrNotReady: the machine cannot take over now (Candidate.Reasons say why).
var ErrNotReady = errors.New("switchover: that machine cannot take over now")

// Start starts a planned switch to node (by: who asked); its id. One switch at a time:
// the check and the switch's row are one step here, and the database refuses a second
// row under way (cluster_switchovers_one_open) when two start at once.
func (o *Orchestrator) Start(ctx context.Context, node, by string) (string, error) {
	o.startMu.Lock()
	defer o.startMu.Unlock()
	cs, err := o.Candidates(ctx)
	if err != nil {
		return "", err
	}
	var target *Candidate
	for i := range cs {
		if cs[i].NodeID == node {
			target = &cs[i]
		}
	}
	if target == nil {
		return "", fmt.Errorf("%w: no such machine", ErrNotReady)
	}
	if !target.Ready {
		return "", fmt.Errorf("%w: %s", ErrNotReady, strings.Join(target.Reasons, ", "))
	}
	var term int64
	if err := o.DB.QueryRowContext(ctx, `SELECT COALESCE(max(term), 1) FROM cluster_terms`).Scan(&term); err != nil {
		return "", err
	}
	p := &Progress{ID: uuid.NewString(), Term: term + 1, Kind: "planned", ToNode: node, ToName: target.Name,
		FromName: o.HomeName, State: "checking", Machines: map[string]Machine{}, Started: time.Now().UTC()}
	if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_switchovers (id, term, kind, to_node, to_name, from_name,
	        state, requested_by) VALUES ($1, $2, 'planned', $3, $4, $5, 'checking', NULLIF($6, '')::uuid)`,
		p.ID, p.Term, p.ToNode, p.ToName, p.FromName, userOf(by)); err != nil {
		var pe *pq.Error
		if errors.As(err, &pe) && pe.Code == "23505" {
			return "", fmt.Errorf("%w: %s", ErrNotReady, ReasonBusy)
		}
		return "", err
	}
	o.mu.Lock()
	if o.progress == nil {
		o.progress = map[string]*Progress{}
	}
	o.progress[p.ID] = p
	o.mu.Unlock()
	o.persist(ctx, p.ID)
	o.log().Info("switchover: starting", "id", p.ID, "to", target.Name, "term", p.Term, "by", by,
		"event", "switchover_started")
	go o.run(p.ID)
	return p.ID, nil
}

func (o *Orchestrator) update(id string, fn func(p *Progress)) {
	o.mu.Lock()
	p, ok := o.progress[id]
	if ok {
		fn(p)
	}
	o.mu.Unlock()
	if ok {
		o.persist(context.Background(), id)
	}
}

func (o *Orchestrator) step(id, name, state, detail string) {
	o.update(id, func(p *Progress) {
		for i := range p.Steps {
			if p.Steps[i].Name == name {
				p.Steps[i].State, p.Steps[i].Detail, p.Steps[i].At = state, detail, time.Now().UTC()
				return
			}
		}
		p.Steps = append(p.Steps, Step{Name: name, State: state, Detail: detail, At: time.Now().UTC()})
	})
}

// persist keeps a switch where this home finds it after a restart (the supervisor's
// record of it), and in its row while this home is the one that writes it: until the
// promotion is asked (from then the row is the new home's), or once the switch is undone
// (this home writable again).
func (o *Orchestrator) persist(ctx context.Context, id string) {
	p := o.Progress(id)
	if p == nil {
		return
	}
	if o.Supervisor != nil {
		if err := o.record(ctx, id); err != nil {
			o.log().Warn("switchover: the switch's record could not be kept", "id", id, "err", err,
				"event", "switchover_record_failed")
		}
	}
	if p.PromoteSent && p.State != "failed" && p.State != "cancelled" {
		return
	}
	steps, _ := json.Marshal(p.Steps)
	machines, _ := json.Marshal(p.Machines)
	// the database may be frozen already (its sessions read-only): this one writes
	tx, err := o.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET TRANSACTION READ WRITE`); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cluster_switchovers SET state = $2, detail = NULLIF($3, ''),
	    steps = $4::jsonb, machines = $5::jsonb, updated_at = now() WHERE id = $1`,
		id, p.State, p.Detail, string(steps), string(machines)); err != nil {
		return
	}
	_ = tx.Commit()
}

// record keeps switch id where this home finds it after a restart: the supervisor's
// record, on disk and synced once it answers.
func (o *Orchestrator) record(ctx context.Context, id string) error {
	p := o.Progress(id)
	if p == nil {
		return nil
	}
	if o.Supervisor == nil {
		return errors.New("no supervisor keeps the switch's record")
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return o.Supervisor.Call(ctx, "record", map[string]any{"id": id, "progress": json.RawMessage(b)}, nil)
}

// command sends a command to a machine's agent and returns its detail.
func (o *Orchestrator) command(ctx context.Context, node, name string, args any, wait time.Duration) (json.RawMessage, error) {
	sid := o.Hub.MachineAgent(node)
	if sid == "" {
		return nil, errors.New("the machine's agent is not connected")
	}
	b, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	res, err := o.Hub.CommandWithin(ctx, sid, name, b, "switchover", wait)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		var detail string
		if json.Unmarshal(res.Detail, &detail) != nil {
			detail = string(res.Detail)
		}
		return nil, errors.New(detail)
	}
	return res.Detail, nil
}

// fail ends a switch that did not go through at step: with undo everything as it was
// (the database writable, the machines' jobs back), which only happens before the
// promotion is asked or once the machine taking over said it called the switch off.
func (o *Orchestrator) fail(ctx context.Context, id, step string, err error, undo bool) {
	o.end(ctx, id, step, err, undo, "failed")
}

// end ends a switch at step in state: "failed", or "cancelled" (an admin called it off).
func (o *Orchestrator) end(ctx context.Context, id, step string, err error, undo bool, state string) {
	o.step(id, step, "failed", err.Error())
	if state == "cancelled" {
		o.log().Warn("switchover: called off", "id", id, "step", step, "err", err, "event", "switchover_cancelled")
	} else {
		o.log().Warn("switchover: failed", "id", id, "step", step, "err", err, "event", "switchover_failed")
	}
	if undo {
		o.undo(ctx, id)
	}
	o.update(id, func(p *Progress) { p.State, p.Detail = state, step+": "+err.Error() })
}

// run runs a planned switch: each step, and on a failure before the promotion
// everything as it was (the database writable, the machines' jobs back). Once the
// promotion is asked this home is never made writable again unless the machine taking
// over says it called the switch off: its answer lost, this home stays frozen and asks it
// (and reads the notice it seals to this home once it is the home) until it knows.
func (o *Orchestrator) run(id string) {
	ctx := context.Background()
	p := o.Progress(id)
	// ---- the checks ----
	o.step(id, StepCheck, "running", "")
	if err := o.Supervisor.Call(ctx, "check", map[string]any{"id": id}, nil); err != nil {
		o.fail(ctx, id, StepCheck, fmt.Errorf("this home cannot be switched: %w", err), false)
		return
	}
	ns, err := o.nodes(ctx)
	if err != nil {
		o.fail(ctx, id, StepCheck, err, false)
		return
	}
	var pubkey string
	machines := map[string]Machine{}
	for _, n := range ns {
		if n.state != "approved" {
			continue
		}
		machines[n.id] = Machine{Name: n.name, State: "waiting", PriorDrain: n.drain}
		if n.id == p.ToNode {
			pubkey = n.pubkey
		}
	}
	// where the machine taking over answers once it is the home
	var urls []string
	var web, api sql.NullString
	_ = o.DB.QueryRowContext(ctx, `SELECT web_url, api_url FROM cluster_nodes WHERE id = $1`, p.ToNode).Scan(&web, &api)
	for _, u := range []string{web.String, api.String} {
		if u != "" {
			urls = append(urls, u)
		}
	}
	o.update(id, func(p *Progress) { p.Machines, p.ToURLs = machines, urls })
	o.step(id, StepCheck, "done", "")
	if o.cancelled(ctx, id, StepDrain) {
		return
	}

	// ---- every machine's jobs drained ----
	o.update(id, func(p *Progress) { p.State = "freezing" })
	o.step(id, StepDrain, "running", "")
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = true, updated_at = now() WHERE state = 'approved'`); err != nil {
		o.fail(ctx, id, StepDrain, err, true)
		return
	}
	for _, sid := range o.Hub.HomeProcesses() {
		_, _ = o.Hub.CommandWithin(ctx, sid, "drain", json.RawMessage("{}"), "switchover", 10*time.Second)
	}
	wait := o.JobsWait
	if wait <= 0 {
		wait = 2 * time.Minute
	}
	end := time.Now().Add(wait)
	for {
		var running int
		_ = o.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs WHERE state IN ('claimed', 'running', 'resuming')`).
			Scan(&running)
		if running == 0 || o.cancelOf(id) != nil {
			break
		}
		if time.Now().After(end) {
			// what did not end in time is paused: it runs again on the new home
			_, _ = o.DB.ExecContext(ctx, `UPDATE processing_jobs SET state = 'paused', paused_reason = 'switchover',
			    paused_at = now(), claimed_by = NULL WHERE state IN ('claimed', 'running', 'resuming')`)
			break
		}
		time.Sleep(2 * time.Second)
	}
	o.step(id, StepDrain, "done", "")
	if o.cancelled(ctx, id, StepFreeze) {
		return
	}

	// ---- the database frozen; the marker on the machine taking over ----
	o.step(id, StepFreeze, "running", "")
	if err := o.Supervisor.Call(ctx, "freeze", map[string]any{"id": id}, nil); err != nil {
		o.fail(ctx, id, StepFreeze, err, true)
		return
	}
	o.update(id, func(p *Progress) { p.State = "frozen" })
	o.step(id, StepFreeze, "done", "")
	if o.cancelled(ctx, id, StepMarker) {
		return
	}
	o.step(id, StepMarker, "running", "")
	raw, err := o.command(ctx, p.ToNode, "switch.prepare", map[string]any{"id": id}, 90*time.Second)
	var prep struct {
		Marker bool `json:"marker"`
	}
	if err == nil {
		_ = json.Unmarshal(raw, &prep)
	}
	if err != nil || !prep.Marker {
		if err == nil {
			err = errors.New("its copy does not have everything this home wrote")
		}
		o.fail(ctx, id, StepMarker, err, true)
		return
	}
	o.step(id, StepMarker, "done", "")
	if o.cancelled(ctx, id, StepKit) {
		return
	}

	// ---- the kit, sealed for the machine taking over, sent over the channel ----
	o.update(id, func(p *Progress) { p.State = "handing_over" })
	o.step(id, StepKit, "running", "")
	var kit struct {
		Path   string `json:"path"`
		Chunks int    `json:"chunks"`
	}
	if err := o.Supervisor.Call(ctx, "kit", map[string]any{"id": id, "term": p.Term, "to_node": p.ToNode,
		"to_pubkey": pubkey}, &kit); err != nil {
		o.fail(ctx, id, StepKit, err, true)
		return
	}
	if err := o.sendKit(ctx, id, p.ToNode, kit.Path); err != nil {
		o.fail(ctx, id, StepKit, err, true)
		return
	}
	o.step(id, StepKit, "done", fmt.Sprintf("%d chunks", kit.Chunks))
	if o.cancelled(ctx, id, StepPromote) {
		return
	}

	// ---- the promotion: from here the machine taking over is the home ----
	o.update(id, func(p *Progress) { p.State = "promoting" })
	o.step(id, StepPromote, "running", "")
	// this home's cloud relay agent stops first: the new home starts its own with the
	// credentials in the kit, and two agents of one server take the tunnel from each other
	if err := o.Supervisor.Call(ctx, "relay", map[string]any{"id": id, "on": false}, nil); err != nil {
		o.fail(ctx, id, StepPromote, fmt.Errorf("stopping this home's cloud relay agent: %w", err), true)
		return
	}
	// asked: this home is not made writable again but on the machine's word. That is
	// recorded (written and synced) before the promotion leaves, or it does not leave: a
	// restart without the record would undo a switch the machine may have gone through
	// with. An admin's call to stop it comes before, or after (then the machine is asked).
	pctx, stop := context.WithCancel(ctx)
	defer stop()
	sent := false
	o.mu.Lock()
	if cur := o.progress[id]; cur != nil && o.cancels[id] == nil {
		cur.PromoteSent, sent = true, true
		if o.promoteWaits == nil {
			o.promoteWaits = map[string]context.CancelFunc{}
		}
		o.promoteWaits[id] = stop
	}
	o.mu.Unlock()
	if !sent {
		o.end(ctx, id, StepPromote, errors.New("called off by an admin: everything is as it was"), true, "cancelled")
		return
	}
	if err := o.record(ctx, id); err != nil {
		o.mu.Lock()
		if cur := o.progress[id]; cur != nil {
			cur.PromoteSent = false
		}
		delete(o.promoteWaits, id)
		o.mu.Unlock()
		o.fail(ctx, id, StepPromote, fmt.Errorf("the switch could not be recorded, so the promotion was not asked: %w", err), true)
		return
	}
	raw, err = o.command(pctx, p.ToNode, "switch.promote", map[string]any{"id": id, "term": p.Term}, 10*time.Minute)
	o.mu.Lock()
	delete(o.promoteWaits, id)
	o.mu.Unlock()
	var home NewHome
	if err == nil {
		err = json.Unmarshal(raw, &home)
	}
	if err == nil && home.APIURL == "" {
		err = errors.New("the promotion answered no address")
	}
	if err != nil {
		var ok bool
		if home, ok = o.waitNewHome(ctx, id, err); !ok {
			return
		}
	}
	o.afterPromote(ctx, id, home)
}

// cancelReq is an admin's call to stop a switch under way: run sees it between its
// steps, and once the promotion was asked waitNewHome does. force: undone although the
// machine taking over did not answer.
type cancelReq struct {
	force bool
}

// Cancel's refusals.
var (
	ErrNoSwitch    = errors.New("no such switch under way")
	ErrTakingOver  = errors.New("the machine taking over is the home, or is becoming it: the switch goes on")
	ErrNoAnswer    = errors.New("the machine taking over does not answer")
	ErrConfirmName = errors.New("the name typed is not the name of the machine taking over")
)

// Cancel calls a switch under way off (POST /api/cluster/switchover/{id}/cancel). Before
// the promotion is asked it is undone at its next step. After, only the machine taking
// over can say it will not be the home: it is asked, and calls the switch off where it has
// not promoted. When it does not answer, the switch is undone only with force and that
// machine's name typed: should it be the home after all, this home learns its newer term
// when it is back and stops writing (what it wrote meanwhile is kept aside, as after an
// emergency). The answer is "calling_off" (at the next step) or "called_off".
func (o *Orchestrator) Cancel(ctx context.Context, id string, force bool, confirm string) (string, error) {
	o.mu.Lock()
	p, ok := o.progress[id]
	if !ok || p.State == "done" || p.State == "failed" || p.State == "cancelled" {
		o.mu.Unlock()
		return "", ErrNoSwitch
	}
	if p.NewHome != nil {
		o.mu.Unlock()
		return "", ErrTakingOver
	}
	if !p.PromoteSent {
		o.cancelLocked(id, false)
		o.mu.Unlock()
		return "calling_off", nil
	}
	toNode, toName, term := p.ToNode, p.ToName, p.Term
	o.mu.Unlock()
	raw, err := o.command(ctx, toNode, "switch.status", map[string]any{"id": id, "term": term, "abort": true}, 30*time.Second)
	if err == nil {
		var st struct {
			State string `json:"state"`
		}
		if json.Unmarshal(raw, &st) != nil || st.State != "called_off" {
			return "", ErrTakingOver
		}
		o.mu.Lock()
		o.cancelLocked(id, false)
		o.mu.Unlock()
		return "called_off", nil
	}
	if !force {
		return "", fmt.Errorf("%w: %v", ErrNoAnswer, err)
	}
	if !strings.EqualFold(strings.TrimSpace(confirm), strings.TrimSpace(toName)) {
		return "", ErrConfirmName
	}
	o.log().Warn("switchover: called off by an admin although the machine taking over does not answer",
		"id", id, "to", toName, "err", err, "event", "switchover_forced_cancel")
	o.mu.Lock()
	o.cancelLocked(id, true)
	o.mu.Unlock()
	return "called_off", nil
}

// cancelLocked marks switch id called off (o.mu held) and ends its waits.
func (o *Orchestrator) cancelLocked(id string, force bool) {
	if o.cancels == nil {
		o.cancels = map[string]*cancelReq{}
	}
	c := o.cancels[id]
	if c == nil {
		c = &cancelReq{}
		o.cancels[id] = c
	}
	c.force = c.force || force
	if w := o.wakes[id]; w != nil {
		close(w)
		delete(o.wakes, id)
	}
	if stop := o.promoteWaits[id]; stop != nil {
		stop()
	}
}

// cancelOf is switch id's cancel, nil when none was asked.
func (o *Orchestrator) cancelOf(id string) *cancelReq {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cancels[id]
}

// cancelled ends a switch an admin called off before its promotion was asked (true):
// everything as it was, its state "cancelled".
func (o *Orchestrator) cancelled(ctx context.Context, id, step string) bool {
	if o.cancelOf(id) == nil {
		return false
	}
	o.end(ctx, id, step, errors.New("called off by an admin: everything is as it was"), true, "cancelled")
	return true
}

// RetryEvery is how often a switch whose promotion's answer did not come asks the
// machine again (and looks for its notice), and how often a demotion under way is asked
// after (0: 10 s).
func (o *Orchestrator) retryEvery() time.Duration {
	if o.RetryEvery > 0 {
		return o.RetryEvery
	}
	return 10 * time.Second
}

// waitNewHome finds out where a promotion whose answer did not come (lost: why) went:
// the machine says it is the home (or this home reads, at its address, the notice it
// sealed to this machine once it is), or that it called the switch off (then everything
// is as it was here: false). Until then this home stays frozen and asks again.
func (o *Orchestrator) waitNewHome(ctx context.Context, id string, why error) (NewHome, bool) {
	o.update(id, func(p *Progress) {
		p.State, p.Detail = "waiting_new_home", "the promotion's answer did not come ("+why.Error()+")"
	})
	o.step(id, StepPromote, "running", "this home stays read-only until the machine taking over says where the "+
		"switch is: "+why.Error())
	o.log().Warn("switchover: the promotion's answer did not come; this home stays frozen until the machine says where the switch is",
		"id", id, "err", why, "event", "switchover_promote_unknown")
	for {
		p := o.Progress(id)
		if raw, err := o.command(ctx, p.ToNode, "switch.status", map[string]any{"id": id, "term": p.Term, "abort": true},
			30*time.Second); err == nil {
			var st struct {
				State string   `json:"state"`
				Home  *NewHome `json:"home"`
			}
			if json.Unmarshal(raw, &st) == nil {
				switch {
				case st.State == "home" && st.Home != nil && st.Home.APIURL != "":
					return *st.Home, true
				case st.State == "called_off":
					state, why := "failed", "the machine taking over called the switch off: everything is as it was"
					if o.cancelOf(id) != nil {
						state, why = "cancelled", "called off by an admin, and by the machine taking over: everything is as it was"
					}
					o.end(ctx, id, StepPromote, errors.New(why), true, state)
					return NewHome{}, false
				}
			}
		}
		var seen struct {
			Home *NewHome `json:"home"`
		}
		if err := o.Supervisor.Call(ctx, "new-home", map[string]any{"id": id, "term": p.Term, "urls": p.ToURLs},
			&seen); err == nil && seen.Home != nil && seen.Home.APIURL != "" {
			return *seen.Home, true
		}
		// called off by an admin without the machine's word (it did not answer): undone
		if c := o.cancelOf(id); c != nil && c.force {
			o.end(ctx, id, StepPromote, errors.New("called off by an admin although the machine taking over did not "+
				"answer: should it be the home after all, this home stops writing once it hears of it"), true, "cancelled")
			return NewHome{}, false
		}
		select {
		case <-ctx.Done():
			return NewHome{}, false
		case <-o.wakeOf(id):
		case <-time.After(o.retryEvery()):
		}
	}
}

// wakeOf is a channel an admin's call to stop switch id closes (Cancel).
func (o *Orchestrator) wakeOf(id string) <-chan struct{} {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.wakes == nil {
		o.wakes = map[string]chan struct{}{}
	}
	w := o.wakes[id]
	if w == nil {
		w = make(chan struct{})
		o.wakes[id] = w
	}
	return w
}

// afterPromote finishes a switch on this home once the machine taking over is the home:
// every other machine told where its home is, then this machine made one of them (its
// supervisor's demotion, asked again until it went through or this process ends with it).
func (o *Orchestrator) afterPromote(ctx context.Context, id string, home NewHome) {
	o.update(id, func(p *Progress) { p.NewHome, p.Detail = &home, "" })
	// it starts again as the home: the machines follow once it answers (they keep trying
	// on their own when it is slower than that)
	if err := o.waitHome(ctx, home); err != nil {
		o.log().Warn("switchover: the new home does not answer yet; the machines keep trying", "id", id,
			"home", home.APIURL, "err", err, "event", "switchover_home_slow")
	}
	o.step(id, StepPromote, "done", home.APIURL)

	// ---- every other machine told where its home is ----
	p := o.Progress(id)
	o.update(id, func(p *Progress) { p.State = "repointing" })
	o.step(id, StepRepoint, "running", "")
	for node := range p.Machines {
		if node == p.ToNode {
			continue
		}
		raw, err := o.command(ctx, node, "home.moved", map[string]any{"id": id, "term": p.Term, "home": home}, 2*time.Minute)
		var r struct {
			CopyKept bool `json:"copy_kept"`
		}
		state, detail := "following", ""
		switch {
		case err != nil:
			state, detail = "offline", err.Error()
		case json.Unmarshal(raw, &r) == nil && !r.CopyKept:
			state = "copying_anew"
		}
		o.update(id, func(p *Progress) {
			m := p.Machines[node]
			m.State, m.Detail = state, detail
			p.Machines[node] = m
		})
	}
	o.step(id, StepRepoint, "done", "")

	// ---- this machine one of the new home's ----
	o.step(id, StepDemote, "running", "")
	for {
		var out struct {
			Running   bool   `json:"running"`
			Done      bool   `json:"done"`
			LastError string `json:"last_error"`
		}
		err := o.Supervisor.Call(ctx, "demote", map[string]any{"id": id, "term": p.Term, "home": home}, &out)
		switch {
		case err == nil && out.Done:
			o.step(id, StepDemote, "done", "")
			o.update(id, func(p *Progress) { p.State = "handed_over" })
			o.log().Info("switchover: handed over; this machine starts again as one of the new home's", "id", id,
				"home", home.APIURL, "event", "switchover_handed_over")
			return
		case err != nil:
			o.step(id, StepDemote, "running", err.Error())
		case out.LastError != "":
			o.step(id, StepDemote, "running", "tried again: "+out.LastError)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(o.retryEvery()):
		}
	}
}

// Resume takes up, at this home's start, a switch its API was running when it stopped
// (the supervisor's record of it): one whose promotion was not asked yet is undone; one
// whose promotion was asked goes on from where it was (the machine taking over asked
// where it went, if its answer was not in).
func (o *Orchestrator) Resume(ctx context.Context) {
	if o.Supervisor == nil {
		return
	}
	var out struct {
		Switches []json.RawMessage `json:"switches"`
	}
	if err := o.Supervisor.Call(ctx, "recorded", map[string]any{}, &out); err != nil {
		o.log().Warn("switchover: the switches under way could not be read", "err", err, "event", "switchover_resume_failed")
		return
	}
	for _, raw := range out.Switches {
		var p Progress
		if json.Unmarshal(raw, &p) != nil || p.ID == "" {
			continue
		}
		o.mu.Lock()
		if o.progress == nil {
			o.progress = map[string]*Progress{}
		}
		if _, ok := o.progress[p.ID]; ok {
			o.mu.Unlock()
			continue
		}
		o.progress[p.ID] = &p
		o.mu.Unlock()
		o.log().Warn("switchover: this home started again during a switch: taking it up", "id", p.ID, "state", p.State,
			"event", "switchover_resumed")
		go o.resume(p.ID)
	}
}

func (o *Orchestrator) resume(id string) {
	ctx := context.Background()
	p := o.Progress(id)
	if p.ThawPending && (p.State == "failed" || p.State == "cancelled") {
		// over, but this home not writable yet: only the thaw is asked again
		o.retryThaw(ctx, id)
		return
	}
	if !p.PromoteSent {
		last := StepCheck
		if n := len(p.Steps); n > 0 {
			last = p.Steps[n-1].Name
		}
		o.fail(ctx, id, last, errors.New("this home started again during the switch: it was called off, "+
			"everything is as it was"), true)
		return
	}
	if p.NewHome != nil && p.NewHome.APIURL != "" {
		o.afterPromote(ctx, id, *p.NewHome)
		return
	}
	if home, ok := o.waitNewHome(ctx, id, errors.New("this home started again before it was answered")); ok {
		o.afterPromote(ctx, id, home)
	}
}

// waitHome waits for the new home to answer as the home (GET /api/cluster/self).
func (o *Orchestrator) waitHome(ctx context.Context, home NewHome) error {
	wait := o.HomeWait
	if wait <= 0 {
		wait = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	c := &http.Client{Timeout: 5 * time.Second}
	var last error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(home.APIURL, "/")+"/api/cluster/self", nil)
		if err != nil {
			return err
		}
		if resp, err := c.Do(req); err != nil {
			last = err
		} else {
			var self struct {
				Role string `json:"role"`
			}
			err := json.NewDecoder(resp.Body).Decode(&self)
			_ = resp.Body.Close()
			if err == nil && self.Role == "home" {
				return nil
			}
			last = fmt.Errorf("%s: %d, role %q", home.APIURL, resp.StatusCode, self.Role)
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(time.Second):
		}
	}
}

// undo puts things back after a switch that did not go through: the database writable,
// every machine's drain as it was, the home's own jobs back.
func (o *Orchestrator) undo(ctx context.Context, id string) {
	if err := o.Supervisor.Call(ctx, "thaw", map[string]any{"id": id}, nil); err != nil {
		// still frozen: said, the switch ended all the same, and the thaw asked again until it
		// goes through (also after a restart: the record says so)
		o.log().Error("switchover: this home could not be made writable again; asking again", "id", id, "err", err,
			"event", "switchover_thaw_failed")
		o.update(id, func(p *Progress) { p.Detail, p.ThawPending = "this home stays read-only: "+err.Error(), true })
		go o.retryThaw(context.Background(), id)
	}
	_ = o.Supervisor.Call(ctx, "relay", map[string]any{"id": id, "on": true}, nil)
	p := o.Progress(id)
	if p == nil {
		return
	}
	// the machine that was to take over calls the switch off too, and drops the kit it
	// was sent (the home's keys stay on no machine but a standby home's); one that does
	// not answer now drops it when it is asked again (Cancel), or calls the switch off
	// itself when its promotion fails
	if p.ToNode != "" {
		_, _ = o.command(ctx, p.ToNode, "switch.status", map[string]any{"id": id, "term": p.Term, "abort": true}, 30*time.Second)
	}
	for node, m := range p.Machines {
		_, _ = o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = $2, updated_at = now() WHERE id = $1`, node, m.PriorDrain)
	}
	for _, sid := range o.Hub.HomeProcesses() {
		_, _ = o.Hub.CommandWithin(ctx, sid, "undrain", json.RawMessage("{}"), "switchover", 10*time.Second)
	}
}

// retryThaw asks this home's supervisor again, every retryEvery, to make it writable
// after a switch that was undone (a thaw that failed) until it does.
func (o *Orchestrator) retryThaw(ctx context.Context, id string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(o.retryEvery()):
		}
		if err := o.Supervisor.Call(ctx, "thaw", map[string]any{"id": id}, nil); err == nil {
			o.update(id, func(p *Progress) { p.ThawPending = false })
			o.log().Info("switchover: this home is writable again", "id", id, "event", "switchover_thawed")
			return
		}
	}
}

// sendKit sends the sealed kit's chunks to the machine taking over.
func (o *Orchestrator) sendKit(ctx context.Context, id, node, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return readChunks(f, func(i uint64, last bool, sealed []byte) error {
		_, err := o.command(ctx, node, "switch.kit", map[string]any{"id": id, "i": i, "last": last,
			"data": base64.StdEncoding.EncodeToString(sealed)}, time.Minute)
		return err
	})
}

func userOf(by string) string {
	if u, ok := strings.CutPrefix(by, "user:"); ok {
		return u
	}
	return ""
}
