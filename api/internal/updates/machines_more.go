package updates

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// NoticeNodePausedSchema: a machine behind a release whose database schema it cannot
// run against (min_node_schema): its local routes answer 503 until it runs the release,
// so it is caught up at once.
const NoticeNodePausedSchema = "update.node_paused_schema"

// clusterRelease is the release the cluster runs: the watermark, raised to the home's
// running release when that is newer, one the catalog verified, and one the home runs for
// good (homeSettledOn: a home installed by hand: its machines are behind it, and caught up
// to it; never a release the home still watches).
func (d *Driver) clusterRelease(ctx context.Context) (string, error) {
	wm, err := d.Store.Watermark(ctx)
	if err != nil {
		return "", err
	}
	cur := ""
	if d.Svc != nil {
		cur = NormalizeVersion(d.Svc.Current)
	}
	if cur == "" || !ValidVersion(cur) || (wm != "" && CompareVersions(cur, wm) <= 0) {
		return wm, nil
	}
	rel, err := d.Store.Get(ctx, cur)
	if err != nil || rel.VerifyState != VerifyVerified || rel.WithdrawnAt != nil {
		return wm, nil
	}
	if !d.homeSettledOn(ctx, cur) {
		// the home runs it but has not committed it (its watch goes on after its run gave
		// up on it, home_timeout): the machines follow once it commits, never before (the
		// owner's decision, 2026-10-28)
		return wm, nil
	}
	if err := d.Store.RaiseWatermark(ctx, cur); err != nil {
		return wm, err
	}
	d.Store.Audit(ctx, "", "system", "watermark_raised", cur, map[string]any{"from": wm, "why": "home_runs_it"})
	return cur, nil
}

// homeSettledOn reports whether the home runs version for good: its supervisor has no
// update of it in flight (none at all, one of another release that is over, or this one
// committed). A supervisor that does not answer is not settled (asked again next time);
// a home without one (a plain container) is.
func (d *Driver) homeSettledOn(ctx context.Context, version string) bool {
	if d.Home == nil {
		return true
	}
	st, err := d.Home.Do(ctx, "status", nil)
	if err != nil {
		return false
	}
	if st == nil || st.Job == nil {
		return true
	}
	switch st.State {
	case MApplying, MRestarting, MWatching, MRollingBack:
		return false // an update in flight, whichever release: it ends first
	}
	if NormalizeVersion(st.Job.Version) == NormalizeVersion(version) {
		return st.State == MCommitted
	}
	return true
}

// heldBack reports whether a machine's last part in a run of version keeps it from being
// caught up to it: the release failed there (rolled back, or failed at or after its
// apply) or an admin skipped it, until a newer release or an admin's Retry. A part that
// failed for a person to mend (needs_person) or before its switch (failed_prepare: a
// download, a refusal) is tried again once the machine changed (another release running,
// another install kind), else catchUpRetry after it (catchUpPersonRetry, a day, for one a
// person must mend: its reason has not changed).
func (d *Driver) heldBack(ctx context.Context, nodeID, version, curVersion, curKind string, curAttempts []Attempt) bool {
	held, _ := d.heldBackUntil(ctx, nodeID, version, curVersion, curKind, curAttempts)
	return held
}

// heldBackUntil is heldBack with when the hold ends by itself: zero when it does not (a
// newer release or an admin's Retry ends it), else the end of its retry wait.
func (d *Driver) heldBackUntil(ctx context.Context, nodeID, version, curVersion, curKind string, curAttempts []Attempt) (bool, time.Time) {
	t, ok, err := d.Store.LastTargetOf(ctx, nodeID, version)
	if err != nil || !ok {
		return false, time.Time{}
	}
	switch t.State {
	case TargetRolledBack, TargetFailed:
		return true, time.Time{}
	case TargetSkipped:
		_, why, _ := d.Store.LastTargetState(ctx, nodeID, version)
		return why == "skipped", time.Time{}
	case TargetNeedsPerson, TargetFailedPrepare:
		if curKind != "" && t.Kind != "" && curKind != t.Kind {
			return false, time.Time{} // its install changed (converted to a self-managed one, say)
		}
		if curVersion != "" && t.FromVersion != "" && CompareVersions(curVersion, t.FromVersion) != 0 {
			return false, time.Time{}
		}
		if reasonChanged(t, curAttempts) {
			return false, time.Time{} // the machine says something else since (its own newer attempt)
		}
		wait := catchUpRetry
		if t.State == TargetNeedsPerson {
			wait = catchUpPersonRetry
		}
		if t.UpdatedAt != nil && d.now().Sub(*t.UpdatedAt) < wait {
			return true, t.UpdatedAt.Add(wait)
		}
	}
	return false, time.Time{}
}

// reasonChanged reports whether a machine's own newest attempt, made after its held part
// (t), gives another reason than that part's: what held it may be mended (a permission
// given, a folder unlocked), so it is caught up at once rather than after the wait.
func reasonChanged(t Target, attempts []Attempt) bool {
	if len(attempts) == 0 || t.UpdatedAt == nil {
		return false
	}
	newest := attempts[0]
	for _, a := range attempts[1:] {
		if a.At.After(newest.At) {
			newest = a
		}
	}
	return newest.At.After(*t.UpdatedAt) && newest.Code != readErr(t.Error).Code
}

// schemaPaused reports whether rel's schema pauses a machine on version: rel names a
// min_node_schema above version's schema (both from the catalog's verified manifests;
// false when version's is not known).
func (d *Driver) schemaPaused(ctx context.Context, rel Release, version string) bool {
	if rel.Manifest == nil || rel.Manifest.MinNodeSchema <= 0 || version == "" {
		return false
	}
	have, err := d.Store.Get(ctx, NormalizeVersion(version))
	if err != nil || have.Manifest == nil || have.Manifest.SchemaRev <= 0 {
		return false
	}
	return have.Manifest.SchemaRev < rel.Manifest.MinNodeSchema
}

// notePausedMachines tells, once per machine and release, of the machines rel's schema
// pauses until they run it.
func (d *Driver) notePausedMachines(ctx context.Context, run Run, rel Release) {
	nodes, err := d.nodes(ctx)
	if err != nil {
		return
	}
	_, states := d.agentStates()
	for _, n := range nodes {
		ver := n.version
		if s, ok := states[n.id]; ok && s.Version != "" {
			ver = s.Version
		}
		if ver == "" || CompareVersions(ver, rel.Version) >= 0 || !d.schemaPaused(ctx, rel, ver) {
			continue
		}
		d.Svc.notify(ctx, NoticeNodePausedSchema, notify.Payload{Severity: notify.SeverityWarn,
			Params: map[string]any{"machine": n.name, "version": NormalizeVersion(rel.Version),
				"from": NormalizeVersion(ver), "min_node_schema": rel.Manifest.MinNodeSchema},
			Link: "/admin/updates", Subject: "machine:" + n.id + ":paused:" + NormalizeVersion(rel.Version), Every: notifyOnce})
		d.audit(ctx, run, "node_paused_schema", map[string]any{"node_id": n.id, "from": ver})
	}
}

// nodeDrained drains a machine for its own apply (no new jobs; its running ones finish,
// or checkpoint at the restart once drain_timeout_minutes passed): true once it may
// restart. Its drain flag before is kept in the run's detail and put back once it is
// healthy; a machine whose part fails stays drained (a person looks at it first), and
// one the run never applied is put back when the run ends (releaseNodeDrains).
func (d *Driver) nodeDrained(ctx context.Context, run Run, det *RunDetail, pol Policy, t Target) bool {
	if det.NodeDrains == nil {
		det.NodeDrains = map[string]NodeDrain{}
	}
	nd, ok := det.NodeDrains[t.NodeID]
	if !ok {
		var prior boolScan
		noteStore(d.log(), "read_drain", d.Store.DB.QueryRowContext(ctx, `SELECT drain FROM cluster_nodes WHERE id = $1`, t.NodeID).Scan(&prior))
		nd = NodeDrain{Prior: prior.v, Since: d.now()}
		det.NodeDrains[t.NodeID] = nd
		// what to put back is saved before the flag changes
		noteStore(d.log(), "SetRunDetail", d.Store.SetRunDetail(ctx, run.ID, "", *det), "run_id", run.ID)
		storeExec(d.log(), "drain")(d.Store.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = true WHERE id = $1`, t.NodeID))
		d.audit(ctx, run, "node_drain", map[string]any{"node_id": t.NodeID})
	}
	return d.nodeJobs(ctx, t.NodeID, t.Name) == 0 || d.now().Sub(nd.Since) >= time.Duration(pol.DrainTimeoutMinutes)*time.Minute
}

// nodeHoster is a Cluster that knows the host names a machine's sessions report.
type nodeHoster interface {
	NodeHosts() map[string][]string
}

// sameHost reports whether two host names name one machine: equal (case aside), or one
// a bare name and the other that name with a domain (a Mac's "Studio.local" and "Studio").
// Two names that both have a domain must be equal ("localhost.lan" is not
// "localhost.localdomain"), and an address is never shortened ("10.0.0.5" is not
// "10.0.0.7").
func sameHost(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if strings.EqualFold(a, b) {
		return true
	}
	if net.ParseIP(a) != nil || net.ParseIP(b) != nil {
		return false
	}
	da, db := strings.Contains(a, "."), strings.Contains(b, ".")
	if da == db {
		return false // both bare (and not equal), or both with a domain (and not equal)
	}
	sa, _, _ := strings.Cut(a, ".")
	sb, _, _ := strings.Cut(b, ".")
	return strings.EqualFold(sa, sb)
}

// nodeJobs is how many jobs a machine's workers run now. A job's claimer is "host/pid/id"
// with its worker's host name: the hosts the machine's sessions report to the hub (its
// machine agent and its workers), else its cluster name (a name that is its host's).
func (d *Driver) nodeJobs(ctx context.Context, nodeID, name string) int {
	hosts := []string{name}
	if c, ok := d.Cluster.(nodeHoster); ok {
		hosts = append(hosts, c.NodeHosts()[nodeID]...)
	}
	rows, err := d.Store.DB.QueryContext(ctx, `SELECT claimed_by FROM processing_jobs
		WHERE state IN ('claimed', 'running', 'resuming') AND claimed_by IS NOT NULL`)
	if err != nil {
		return 0
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var by string
		if rows.Scan(&by) != nil {
			continue
		}
		host, _, _ := strings.Cut(by, "/")
		for _, h := range hosts {
			if sameHost(host, h) {
				n++
				break
			}
		}
	}
	return n
}

// nodeUndrain puts back a healthy machine's drain flag as it was before its apply.
func (d *Driver) nodeUndrain(ctx context.Context, run Run, nodeID string) {
	_, det, err := d.Store.RunDetailOf(ctx, run.ID)
	if err != nil {
		return
	}
	nd, ok := det.NodeDrains[nodeID]
	if !ok {
		return
	}
	if !nd.Prior {
		storeExec(d.log(), "undrain")(d.Store.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = false WHERE id = $1`, nodeID))
	}
	delete(det.NodeDrains, nodeID)
	noteStore(d.log(), "SetRunDetail", d.Store.SetRunDetail(ctx, run.ID, "", det), "run_id", run.ID)
}

// releaseNodeDrains puts back, at the run's end, the drain of the machines it drained
// for an apply it never gave (a cancelled run, a window over); a machine whose part
// failed stays drained.
func (d *Driver) releaseNodeDrains(ctx context.Context, run Run, det *RunDetail) {
	if len(det.NodeDrains) == 0 {
		return
	}
	states := map[string]string{}
	if targets, err := d.Store.Targets(ctx, run.ID); err == nil {
		for _, t := range targets {
			states[t.NodeID] = t.State
		}
	}
	for id, nd := range det.NodeDrains {
		switch states[id] {
		case TargetFailed, TargetRolledBack:
			d.audit(ctx, run, "node_kept_drained", map[string]any{"node_id": id})
			continue
		}
		if !nd.Prior {
			storeExec(d.log(), "undrain")(d.Store.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = false WHERE id = $1`, id))
		}
		delete(det.NodeDrains, id)
	}
	noteStore(d.log(), "SetRunDetail", d.Store.SetRunDetail(ctx, run.ID, "", *det), "run_id", run.ID)
}
