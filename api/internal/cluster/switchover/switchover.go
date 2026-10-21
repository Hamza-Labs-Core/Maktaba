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
}

// Machine is a machine's part in a switch.
type Machine struct {
	Name       string `json:"name"`
	State      string `json:"state"` // waiting | following | copying_anew | offline | failed
	Detail     string `json:"detail,omitempty"`
	PriorDrain bool   `json:"prior_drain"`
}

// Integration is an outside service's state after a switch.
type Integration struct {
	State  string `json:"state"` // moved | failed | needs_you | not_used
	Detail string `json:"detail,omitempty"`
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

	mu          sync.Mutex
	progress    map[string]*Progress
	standbyWake chan struct{}
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

// Start starts a planned switch to node (by: who asked); its id.
func (o *Orchestrator) Start(ctx context.Context, node, by string) (string, error) {
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
	o.mu.Lock()
	if o.progress == nil {
		o.progress = map[string]*Progress{}
	}
	o.progress[p.ID] = p
	o.mu.Unlock()
	o.log().Info("switchover: starting", "id", p.ID, "to", target.Name, "term", p.Term, "by", by,
		"event", "switchover_started")
	go o.run(p.ID, by)
	return p.ID, nil
}

func (o *Orchestrator) update(id string, fn func(p *Progress)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if p, ok := o.progress[id]; ok {
		fn(p)
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

// run runs a planned switch: each step, and on a failure before the promotion
// everything as it was (the database writable, the machines' jobs back).
func (o *Orchestrator) run(id, by string) {
	ctx := context.Background()
	p := o.Progress(id)
	fail := func(step string, err error, undo bool) {
		o.step(id, step, "failed", err.Error())
		o.log().Warn("switchover: failed", "id", id, "step", step, "err", err, "event", "switchover_failed")
		if undo {
			o.undo(ctx, id)
		}
		o.update(id, func(p *Progress) { p.State, p.Detail = "failed", step+": "+err.Error() })
		_, _ = o.DB.ExecContext(ctx, `UPDATE cluster_switchovers SET state = 'failed', detail = $2, updated_at = now()
		    WHERE id = $1`, id, step+": "+err.Error())
	}
	// ---- the checks ----
	o.step(id, StepCheck, "running", "")
	if err := o.Supervisor.Call(ctx, "check", map[string]any{"id": id}, nil); err != nil {
		fail(StepCheck, fmt.Errorf("this home cannot be switched: %w", err), false)
		return
	}
	ns, err := o.nodes(ctx)
	if err != nil {
		fail(StepCheck, err, false)
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
	o.update(id, func(p *Progress) { p.Machines = machines })
	mj, _ := json.Marshal(machines)
	if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_switchovers (id, term, kind, to_node, to_name, from_name,
	        state, machines, requested_by) VALUES ($1, $2, 'planned', $3, $4, $5, 'freezing', $6::jsonb, NULLIF($7, '')::uuid)`,
		id, p.Term, p.ToNode, p.ToName, p.FromName, string(mj), userOf(by)); err != nil {
		fail(StepCheck, err, false)
		return
	}
	o.step(id, StepCheck, "done", "")

	// ---- every machine's jobs drained ----
	o.update(id, func(p *Progress) { p.State = "freezing" })
	o.step(id, StepDrain, "running", "")
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = true, updated_at = now() WHERE state = 'approved'`); err != nil {
		fail(StepDrain, err, true)
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
		if running == 0 {
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

	// ---- the database frozen; the marker on the machine taking over ----
	o.step(id, StepFreeze, "running", "")
	if err := o.Supervisor.Call(ctx, "freeze", map[string]any{"id": id}, nil); err != nil {
		fail(StepFreeze, err, true)
		return
	}
	o.update(id, func(p *Progress) { p.State = "frozen" })
	o.step(id, StepFreeze, "done", "")
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
		fail(StepMarker, err, true)
		return
	}
	o.step(id, StepMarker, "done", "")

	// ---- the kit, sealed for the machine taking over, sent over the channel ----
	o.update(id, func(p *Progress) { p.State = "handing_over" })
	o.step(id, StepKit, "running", "")
	var kit struct {
		Path   string `json:"path"`
		Chunks int    `json:"chunks"`
	}
	if err := o.Supervisor.Call(ctx, "kit", map[string]any{"id": id, "term": p.Term, "to_node": p.ToNode,
		"to_pubkey": pubkey}, &kit); err != nil {
		fail(StepKit, err, true)
		return
	}
	if err := o.sendKit(ctx, id, p.ToNode, kit.Path); err != nil {
		fail(StepKit, err, true)
		return
	}
	o.step(id, StepKit, "done", fmt.Sprintf("%d chunks", kit.Chunks))

	// ---- the promotion: from here the machine taking over is the home ----
	o.update(id, func(p *Progress) { p.State = "promoting" })
	o.step(id, StepPromote, "running", "")
	// this home's cloud relay agent stops first: the new home starts its own with the
	// credentials in the kit, and two agents of one server take the tunnel from each other
	if err := o.Supervisor.Call(ctx, "relay", map[string]any{"id": id, "on": false}, nil); err != nil {
		fail(StepPromote, fmt.Errorf("stopping this home's cloud relay agent: %w", err), true)
		return
	}
	raw, err = o.command(ctx, p.ToNode, "switch.promote", map[string]any{"id": id, "term": p.Term}, 10*time.Minute)
	var home NewHome
	if err == nil {
		err = json.Unmarshal(raw, &home)
	}
	if err != nil {
		fail(StepPromote, err, true)
		return
	}
	o.update(id, func(p *Progress) { p.NewHome = &home })
	// it starts again as the home: the machines follow once it answers (they keep trying
	// on their own when it is slower than that)
	if err := o.waitHome(ctx, home); err != nil {
		o.log().Warn("switchover: the new home does not answer yet; the machines keep trying", "id", id,
			"home", home.APIURL, "err", err, "event", "switchover_home_slow")
	}
	o.step(id, StepPromote, "done", home.APIURL)

	// ---- every other machine told where its home is ----
	o.update(id, func(p *Progress) { p.State = "repointing" })
	o.step(id, StepRepoint, "running", "")
	for node := range machines {
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
	if err := o.Supervisor.Call(ctx, "demote", map[string]any{"id": id, "term": p.Term, "home": home}, nil); err != nil {
		o.step(id, StepDemote, "failed", err.Error())
		o.update(id, func(p *Progress) { p.State, p.Detail = "failed", "demote: "+err.Error() })
		return
	}
	o.step(id, StepDemote, "done", "")
	o.update(id, func(p *Progress) { p.State = "handed_over" })
	o.log().Info("switchover: handed over; this machine starts again as one of the new home's", "id", id,
		"home", home.APIURL, "event", "switchover_handed_over")
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

// undo puts things back after a switch that did not reach the promotion: the database
// writable, every machine's drain as it was, the home's own jobs back.
func (o *Orchestrator) undo(ctx context.Context, id string) {
	_ = o.Supervisor.Call(ctx, "thaw", map[string]any{"id": id}, nil)
	_ = o.Supervisor.Call(ctx, "relay", map[string]any{"id": id, "on": true}, nil)
	p := o.Progress(id)
	if p == nil {
		return
	}
	for node, m := range p.Machines {
		_, _ = o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = $2, updated_at = now() WHERE id = $1`, node, m.PriorDrain)
	}
	for _, sid := range o.Hub.HomeProcesses() {
		_, _ = o.Hub.CommandWithin(ctx, sid, "undrain", json.RawMessage("{}"), "switchover", 10*time.Second)
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
