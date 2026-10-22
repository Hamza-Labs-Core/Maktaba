package switchover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// An admin calls a switch off before the promotion is asked: it is undone at its next
// step, and the promotion never leaves.
func TestAnAdminCallsASwitchOffBeforeThePromotion(t *testing.T) {
	o, h, s, b, _ := setup(t)
	ctx := context.Background()
	held, release := make(chan struct{}), make(chan struct{})
	h.answers["switch.prepare"] = func(json.RawMessage) (bool, any) {
		close(held)
		<-release
		return true, map[string]any{"marker": true}
	}
	id, err := o.Start(ctx, b, "system")
	if err != nil {
		t.Fatal(err)
	}
	<-held
	if state, err := o.Cancel(ctx, id, false, ""); err != nil || state != "calling_off" {
		t.Fatalf("cancel: %q %v", state, err)
	}
	close(release)
	p := waitState(t, o, id, "cancelled", "failed", "handed_over")
	if p.State != "cancelled" || p.PromoteSent {
		t.Fatalf("the switch: %+v", p)
	}
	if got := strings.Join(h.called(), " "); strings.Contains(got, "switch.promote") || strings.Contains(got, "switch.kit") {
		t.Fatalf("the machine was asked on: %v", got)
	}
	if got := strings.Join(s.called(), " "); !strings.Contains(got, "thaw") {
		t.Fatalf("the supervisor's steps: %v (not thawed)", got)
	}
	var state string
	_ = o.DB.QueryRowContext(ctx, `SELECT state FROM cluster_switchovers WHERE id = $1`, id).Scan(&state)
	if state != "cancelled" {
		t.Fatalf("the row: %q", state)
	}
	if _, err := o.Cancel(ctx, id, false, ""); !errors.Is(err, ErrNoSwitch) {
		t.Fatalf("a switch over called off again: %v", err)
	}
}

// After the promotion was asked, a call to stop goes to the machine taking over: one
// that does not answer is called off only with force and its name typed, and the old home
// is made writable again then (it never was while the machine could be the home).
func TestAnAdminCallsOffASwitchWhoseMachineDoesNotAnswer(t *testing.T) {
	o, h, s, b, _ := setup(t)
	ctx := context.Background()
	o.RetryEvery = time.Hour // only the cancel wakes the wait
	h.answers["switch.promote"] = func(json.RawMessage) (bool, any) { return false, "the channel closed" }
	h.answers["switch.status"] = func(json.RawMessage) (bool, any) { return false, "the machine's agent is not connected" }
	id, err := o.Start(ctx, b, "system")
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, o, id, "waiting_new_home")
	if _, err := o.Cancel(ctx, id, false, ""); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("without force: %v", err)
	}
	if _, err := o.Cancel(ctx, id, true, "c"); !errors.Is(err, ErrConfirmName) {
		t.Fatalf("another machine's name: %v", err)
	}
	if got := strings.Join(s.called(), " "); strings.Contains(got, "thaw") {
		t.Fatalf("thawed before the admin forced it: %v", got)
	}
	if state, err := o.Cancel(ctx, id, true, " B "); err != nil || state != "called_off" {
		t.Fatalf("forced: %q %v", state, err)
	}
	p := waitState(t, o, id, "cancelled", "failed", "handed_over")
	if p.State != "cancelled" || !strings.Contains(p.Detail, "did not answer") {
		t.Fatalf("the switch: %+v", p)
	}
	if got := strings.Join(s.called(), " "); !strings.HasSuffix(got, "thaw relay:true") {
		t.Fatalf("the supervisor's steps: %v", got)
	}
}

// A machine that is the home, or becoming it, is not called off; one that has not
// promoted calls the switch off when asked, and the old home is made writable again.
func TestACallToStopAsksTheMachineTakingOver(t *testing.T) {
	o, h, s, b, _ := setup(t)
	ctx := context.Background()
	o.RetryEvery = time.Hour
	var promoted atomic.Bool
	promoted.Store(true)
	h.answers["switch.promote"] = func(json.RawMessage) (bool, any) { return false, "timed out" }
	h.answers["switch.status"] = func(json.RawMessage) (bool, any) {
		if promoted.Load() {
			return true, map[string]any{"state": "promoting"}
		}
		return true, map[string]any{"state": "called_off"}
	}
	id, err := o.Start(ctx, b, "system")
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, o, id, "waiting_new_home")
	if _, err := o.Cancel(ctx, id, true, "b"); !errors.Is(err, ErrTakingOver) {
		t.Fatalf("a machine promoting: %v", err)
	}
	promoted.Store(false)
	if state, err := o.Cancel(ctx, id, false, ""); err != nil || state != "called_off" {
		t.Fatalf("a machine that has not promoted: %q %v", state, err)
	}
	p := waitState(t, o, id, "cancelled", "failed", "handed_over")
	if p.State != "cancelled" {
		t.Fatalf("the switch: %+v", p)
	}
	if got := strings.Join(s.called(), " "); !strings.Contains(got, "thaw") {
		t.Fatalf("the supervisor's steps: %v", got)
	}
}

// The record that the promotion was asked cannot be kept: the promotion does not leave,
// and everything is as it was (a restart would otherwise undo a switch the machine may
// have gone through with).
func TestAPromotionIsNotAskedWithoutItsRecord(t *testing.T) {
	o, h, s, b, _ := setup(t)
	ctx := context.Background()
	s.mu.Lock()
	s.failPromoted = true
	s.mu.Unlock()
	id, err := o.Start(ctx, b, "system")
	if err != nil {
		t.Fatal(err)
	}
	p := waitState(t, o, id, "failed", "handed_over")
	if p.State != "failed" || p.PromoteSent || !strings.Contains(p.Detail, "not asked") {
		t.Fatalf("the switch: %+v", p)
	}
	if got := strings.Join(h.called(), " "); strings.Contains(got, "switch.promote") {
		t.Fatalf("the promotion left: %v", got)
	}
	if got := strings.Join(s.called(), " "); !strings.Contains(got, "thaw") {
		t.Fatalf("the supervisor's steps: %v", got)
	}
}
