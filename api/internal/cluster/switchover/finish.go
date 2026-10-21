package switchover

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// FollowWithin is how long the new home waits for the machines to follow it before it
// calls the switch done anyway (a machine that was off follows when it is back).
const FollowWithin = time.Hour

// Integrations reports the outside services after a switch, on the new home: each
// moved and checked, or what it needs (Integration.State).
type Integrations func(ctx context.Context) map[string]Integration

// Finish finishes on this home a switch that made it the home (it runs on every home,
// every 15 s, and does nothing otherwise): it marks the machines that follow it, moves
// and checks the integrations, and once every machine follows (or FollowWithin has
// passed) gives the machines their jobs back and calls the switch done.
func (o *Orchestrator) Finish(ctx context.Context, integrations Integrations) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		if err := o.finishOnce(ctx, integrations); err != nil && ctx.Err() == nil {
			o.log().Debug("switchover: finishing", "err", err, "event", "switchover_finish_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (o *Orchestrator) finishOnce(ctx context.Context, integrations Integrations) error {
	var (
		id, toNode, state string
		term              int64
		machinesRaw       json.RawMessage
		frozen            sql.NullTime
		integr            json.RawMessage
	)
	err := o.DB.QueryRowContext(ctx, `SELECT s.id::text, s.term, s.to_node::text, s.state, s.machines, s.frozen_at,
	        s.integrations
	    FROM cluster_switchovers s JOIN cluster_terms t ON t.term = s.term AND t.home_node = s.to_node
	    WHERE s.state IN ('frozen', 'handing_over', 'promoting', 'repointing')
	    ORDER BY s.requested_at DESC LIMIT 1`).Scan(&id, &term, &toNode, &state, &machinesRaw, &frozen, &integr)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	machines := map[string]Machine{}
	_ = json.Unmarshal(machinesRaw, &machines)
	delete(machines, toNode) // this home
	// the old home joined as a machine at the promotion
	rows, err := o.DB.QueryContext(ctx, `SELECT id::text, name FROM cluster_nodes WHERE approved_by = 'switchover'
	    AND approved_at >= $1`, frozen.Time)
	if err == nil {
		for rows.Next() {
			var nid, name string
			if rows.Scan(&nid, &name) == nil {
				if _, ok := machines[nid]; !ok {
					machines[nid] = Machine{Name: name, State: "waiting"}
				}
			}
		}
		_ = rows.Close()
	}
	all := true
	for nid, m := range machines {
		var rstate string
		var reported sql.NullTime
		var full bool
		err := o.DB.QueryRowContext(ctx, `SELECT COALESCE(replica->>'state', ''), replica_reported_at, full_node
		    FROM cluster_nodes WHERE id = $1 AND state = 'approved'`, nid).Scan(&rstate, &reported, &full)
		switch {
		case err != nil || !full:
			// gone, or a machine that keeps no copy: nothing to wait for
		case rstate == "following" && reported.Valid && reported.Time.After(frozen.Time):
			m.State = "following"
		case reported.Valid && reported.Time.After(frozen.Time):
			m.State, all = "copying_anew", false
		default:
			all = false
		}
		machines[nid] = m
	}
	cur := map[string]Integration{}
	_ = json.Unmarshal(integr, &cur)
	if integrations != nil {
		for k, v := range integrations(ctx) {
			cur[k] = v
		}
	}
	mj, _ := json.Marshal(machines)
	ij, _ := json.Marshal(cur)
	done := all || (frozen.Valid && time.Since(frozen.Time) > FollowWithin)
	newState := "repointing"
	if done {
		newState = "done"
		// the machines' jobs back, as each had them before
		for nid, m := range machines {
			_, _ = o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = $2, updated_at = now() WHERE id = $1`, nid, m.PriorDrain)
		}
		o.log().Info("switchover: done on the new home", "id", id, "term", term, "event", "switchover_done")
	}
	_, err = o.DB.ExecContext(ctx, `UPDATE cluster_switchovers SET state = $2, machines = $3::jsonb,
	    integrations = $4::jsonb, updated_at = now(), done_at = CASE WHEN $2 = 'done' THEN now() ELSE done_at END
	    WHERE id = $1`, id, newState, string(mj), string(ij))
	return err
}

// Recorded is a switch as the database has it (this home's latest one, the new home's
// view of a switch the old home ran).
func (o *Orchestrator) Recorded(ctx context.Context, limit int) ([]Progress, error) {
	rows, err := o.DB.QueryContext(ctx, `SELECT id::text, term, kind, to_node::text, to_name, from_name, state,
	        COALESCE(detail, ''), machines, integrations, requested_at
	    FROM cluster_switchovers ORDER BY requested_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Progress
	for rows.Next() {
		var p Progress
		var mj, ij json.RawMessage
		if err := rows.Scan(&p.ID, &p.Term, &p.Kind, &p.ToNode, &p.ToName, &p.FromName, &p.State, &p.Detail,
			&mj, &ij, &p.Started); err != nil {
			return nil, err
		}
		p.Machines = map[string]Machine{}
		_ = json.Unmarshal(mj, &p.Machines)
		_ = json.Unmarshal(ij, &p.Integr)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Term is the cluster's current term and its home.
type Term struct {
	Term      int64     `json:"term"`
	HomeName  string    `json:"home_name"`
	Reason    string    `json:"reason"`
	StartedAt time.Time `json:"started_at"`
}

// CurrentTerm is the cluster's current term.
func (o *Orchestrator) CurrentTerm(ctx context.Context) (Term, error) {
	var t Term
	err := o.DB.QueryRowContext(ctx, `SELECT term, home_name, reason, started_at FROM cluster_terms
	    ORDER BY term DESC LIMIT 1`).Scan(&t.Term, &t.HomeName, &t.Reason, &t.StartedAt)
	return t, err
}
