package switchover

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// What a person did after a switch stays done while the new home finishes it; a check
// that can tell (a model runtime started since) says so itself.
func TestTheChecklistKeepsWhatAPersonDid(t *testing.T) {
	o, _, _, b, _ := setup(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_terms (term, home_name, home_node, reason) VALUES (2, 'b', $1, 'planned')`, b); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_switchovers (id, term, kind, to_node, to_name, from_name, state,
	        integrations, frozen_at) VALUES ($1, 2, 'planned', $2, 'b', 'a', 'repointing',
	        '{"youtube": {"state": "needs_you", "detail": "firefox"}}'::jsonb, now())`, id, b); err != nil {
		t.Fatal(err)
	}
	checks := func(context.Context) map[string]Integration {
		return map[string]Integration{"youtube": {State: NeedsYou, Detail: "firefox"},
			"llm": {State: NeedsYou, Detail: "127.0.0.1:11434"}}
	}
	if err := o.MarkDone(ctx, id, "youtube", true); err != nil {
		t.Fatal(err)
	}
	if err := o.MarkDone(ctx, id, "no-such-item", true); err == nil {
		t.Fatal("an item the switch does not have was marked")
	}
	if err := o.finishOnce(ctx, checks); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	_ = o.DB.QueryRowContext(ctx, `SELECT integrations FROM cluster_switchovers WHERE id = $1`, id).Scan(&raw)
	cur := map[string]Integration{}
	_ = json.Unmarshal(raw, &cur)
	if !cur["youtube"].Done || cur["youtube"].State != NeedsYou || cur["llm"].Done {
		t.Fatalf("after the new home's look: %+v", cur)
	}
	// the model runtime answers now: the checklist says so, without a person
	o.checks = func(context.Context) map[string]Integration {
		return map[string]Integration{"llm": {State: Moved, Detail: "127.0.0.1:11434"}}
	}
	p := &Progress{Started: time.Now(), Integr: cur}
	o.recheck(ctx, p)
	if p.Integr["llm"].State != Moved || !p.Integr["youtube"].Done {
		t.Fatalf("rechecked: %+v", p.Integr)
	}
	// long after the switch the list is not checked again
	old := &Progress{Started: time.Now().Add(-(ChecklistDays + 1) * 24 * time.Hour),
		Integr: map[string]Integration{"llm": {State: NeedsYou}}}
	o.recheck(ctx, old)
	if old.Integr["llm"].State != NeedsYou {
		t.Fatal("a switch long past was checked again")
	}
}

// An emergency's fork is dropped once it is old and no report of its term waits; kept
// while one does, or while it is recent.
func TestAnOldForkIsDropped(t *testing.T) {
	o, _, _, _, _ := setup(t)
	ctx := context.Background()
	var level string
	if err := o.DB.QueryRowContext(ctx, `SHOW wal_level`).Scan(&level); err != nil || level != "logical" {
		t.Skip("the test database does not run with wal_level=logical")
	}
	for _, f := range []struct {
		term       int64
		age        string
		reportWait bool
	}{{5, "40 days", false}, {6, "3 days", false}, {7, "40 days", true}} {
		if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_terms (term, home_name, reason, started_at)
		    VALUES ($1, 'b', 'emergency', now() - $2::interval)`, f.term, f.age); err != nil {
			t.Fatal(err)
		}
		slot := fmt.Sprintf("maktaba_fork_%d", f.term)
		if _, err := o.DB.ExecContext(ctx, `SELECT pg_create_logical_replication_slot($1, 'pgoutput')`, slot); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = o.DB.ExecContext(context.Background(), `SELECT pg_drop_replication_slot(slot_name)
			    FROM pg_replication_slots WHERE slot_name = $1`, slot)
		})
		if f.reportWait {
			if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_kept_aside (id, term, from_node, from_name, report)
			    VALUES ($1, $2, $3, 'a', '{}'::jsonb)`, uuid.NewString(), f.term, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := o.dropOldForks(ctx); err != nil {
		t.Fatal(err)
	}
	have := func(term int64) bool {
		var n int
		_ = o.DB.QueryRowContext(ctx, `SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`,
			fmt.Sprintf("maktaba_fork_%d", term)).Scan(&n)
		return n == 1
	}
	if have(5) || !have(6) || !have(7) {
		t.Fatalf("forks kept: old %v, recent %v, a report waiting %v", have(5), have(6), have(7))
	}
}
