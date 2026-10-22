package cluster

import (
	"context"
	"errors"
	"testing"
)

// Which slots of the home's database are not Maktaba's (guardSlots), with copies allowed
// and refused (a database that is not Maktaba's own Postgres, without the rule).
func TestForeignSlots(t *testing.T) {
	const machine = "0123456789abcdef0123456789abcdef"
	copying := map[string]bool{machine: true}
	logical := func(name, plugin string) slotRow {
		return slotRow{name: name, kind: "logical", plugin: plugin, here: true}
	}
	for _, c := range []struct {
		slot            slotRow
		bundled         bool
		refused         string
		foreign         bool
		refusedForeigns bool
	}{
		{slot: logical("maktaba_copy_"+machine, "pgoutput"), refusedForeigns: true},
		{slot: logical("maktaba_copy_ffffffffffffffffffffffffffffffff", "pgoutput"), foreign: true, refusedForeigns: true},
		{slot: logical("pg_16400_sync_16500_7301", "pgoutput"), refusedForeigns: true},
		// an emergency's fork is the home's own, copies or not
		{slot: logical("maktaba_fork_7", "pgoutput")},
		{slot: logical("maktaba_copy_"+machine, "test_decoding"), foreign: true, refusedForeigns: true},
		{slot: logical("someone_else", "pgoutput"), foreign: true, refusedForeigns: true},
		// another database's decodes nothing of this one
		{slot: slotRow{name: "someone_else", kind: "logical", plugin: "test_decoding"}},
		{slot: slotRow{name: "standby", kind: "physical"}},
		{slot: slotRow{name: "standby", kind: "physical"}, bundled: true, foreign: true, refusedForeigns: true},
		{slot: slotRow{name: "standby", kind: "physical", active: true}, bundled: true},
	} {
		if got := c.slot.foreign(copying, c.bundled, "") != ""; got != c.foreign {
			t.Errorf("%+v bundled %v: foreign %v, want %v", c.slot, c.bundled, got, c.foreign)
		}
		why := c.slot.foreign(copying, c.bundled, refusedOperatorPG)
		if got := why != ""; got != c.refusedForeigns {
			t.Errorf("%+v bundled %v, copies refused: foreign %v (%q), want %v", c.slot, c.bundled, got, why, c.refusedForeigns)
		}
	}
	if why := logical("maktaba_copy_"+machine, "pgoutput").foreign(copying, false, refusedOperatorPG); why != refusedOperatorPG {
		t.Fatalf("a machine's slot with copies refused: %q, want the refusal", why)
	}
}

// ruleOn is rules.cluster.copies_on_operator_postgres on.
func ruleOn(context.Context) (bool, error) { return true, nil }

// Copies are refused on a database that is not Maktaba's own Postgres unless the rule is
// on; Maktaba's own needs none; a rule that could not be read is not known (nothing is
// changed for it).
func TestCopiesRefusal(t *testing.T) {
	off := func(context.Context) (bool, error) { return false, nil }
	unread := func(context.Context) (bool, error) { return true, errors.New("the database did not answer") }
	for i, c := range []struct {
		copies         Copies
		refused, known bool
	}{
		{Copies{}, true, true},
		{Copies{OperatorPG: off}, true, true},
		{Copies{OperatorPG: ruleOn}, false, true},
		{Copies{OperatorPG: unread}, false, false},
		{Copies{Bundled: true}, false, true},
		{Copies{Bundled: true, OperatorPG: off}, false, true},
		{Copies{Bundled: true, OperatorPG: unread}, false, true},
	} {
		why, known := c.copies.refusal(context.Background())
		if (why != "") != c.refused || known != c.known {
			t.Errorf("case %d: refused %q known %v, want refused %v known %v", i, why, known, c.refused, c.known)
		}
	}
}
