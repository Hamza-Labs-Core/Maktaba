package main

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/clusterauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
)

// selfUpdateEvery is how long an update's word on /api/cluster/self is kept: the page
// asks every 15 s, the apps more often; the database and the supervisor are asked at
// most this often.
const selfUpdateEvery = 10 * time.Second

// cachedUpdate keeps read's answer for selfUpdateEvery (a failed read keeps the last).
func cachedUpdate(read func(context.Context) (*clusterauth.SelfUpdate, bool)) func(context.Context) *clusterauth.SelfUpdate {
	var mu sync.Mutex
	var at time.Time
	var last *clusterauth.SelfUpdate
	return func(ctx context.Context) *clusterauth.SelfUpdate {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && time.Since(at) < selfUpdateEvery {
			return last
		}
		if u, ok := read(ctx); ok {
			last = u
		}
		at = time.Now()
		return last
	}
}

// homeUpdateRun is the home's update run in flight (its release, state and step, since
// when), for its /api/cluster/self: a paused machine says what the home is doing.
func homeUpdateRun(db *sql.DB) func(context.Context) *clusterauth.SelfUpdate {
	return cachedUpdate(func(ctx context.Context) (*clusterauth.SelfUpdate, bool) {
		if db == nil {
			return nil, true
		}
		run, err := (&updates.Store{DB: db}).ActiveRun(ctx)
		if err != nil {
			return nil, false
		}
		if run == nil {
			return nil, true
		}
		since := run.StartedAt.UTC()
		return &clusterauth.SelfUpdate{Version: run.Version, State: run.State, Step: run.Step, Since: &since}, true
	})
}

// homeCommitted is the release the home committed last (the update watermark), for its
// /api/cluster/self: a joined machine's app and CLI install nothing newer (the owner's
// rule, 2026-10-28). Read at most every selfUpdateEvery; a failed read keeps the last.
func homeCommitted(db *sql.DB) func(context.Context) string {
	var mu sync.Mutex
	var at time.Time
	var last string
	return func(ctx context.Context) string {
		mu.Lock()
		defer mu.Unlock()
		if db == nil || (!at.IsZero() && time.Since(at) < selfUpdateEvery) {
			return last
		}
		if wm, err := (&updates.Store{DB: db}).Watermark(ctx); err == nil {
			last = wm
		}
		at = time.Now()
		return last
	}
}

// supervisorUpdate is this machine's own update in flight, as its supervisor's journal
// says it (nil when it has none, or the run is over).
func supervisorUpdate(sup updates.HomeSupervisor) func(context.Context) *clusterauth.SelfUpdate {
	return cachedUpdate(func(ctx context.Context) (*clusterauth.SelfUpdate, bool) {
		if sup == nil {
			return nil, true
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		st, err := sup.Do(cctx, "status", nil)
		if err != nil {
			return nil, false
		}
		return machineUpdate(st), true
	})
}

// machineUpdate is a journal's run while it is in flight (nil once it ended).
func machineUpdate(st *updates.MachineStatus) *clusterauth.SelfUpdate {
	if st == nil || st.Job == nil {
		return nil
	}
	switch st.State {
	case updates.MCommitted, updates.MRolledBack, updates.MAborted, updates.MFailed,
		updates.MPrepareFailed, updates.MApplyFailed, "":
		return nil
	}
	return &clusterauth.SelfUpdate{Version: updates.NormalizeVersion(st.Job.Version), State: st.State, Step: st.Step}
}
