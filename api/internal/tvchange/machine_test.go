package tvchange

import (
	"testing"
	"time"
)

// TC1: every (state, action) pair follows design §11.2.
func TestTransitionsTable(t *testing.T) {
	want := map[string]map[string]string{
		StateDraft:      {ActionStage: StateStaged, ActionApprove: StateDraft, ActionSchedule: StateDraft, ActionCancel: StateCancelled, ActionSupersede: StateSuperseded},
		StateStaged:     {ActionStartVerify: StateVerifying, ActionSchedule: StateStaged, ActionApprove: StateStaged, ActionCancel: StateCancelled, ActionFail: StateFailed},
		StateVerifying:  {ActionPass: StateReady, ActionFail: StateFailed, ActionVerify: StateVerifying, ActionSchedule: StateVerifying, ActionApprove: StateVerifying, ActionCancel: StateCancelled},
		StateReady:      {ActionApprove: StateApproved, ActionVerify: StateVerifying, ActionSchedule: StateReady, ActionCancel: StateCancelled, ActionRecheckFail: StateVerifying, ActionAutoApprove: StateApproved, ActionAutoPromote: StateSwapping, ActionPromote: StateSwapping},
		StateApproved:   {ActionPromote: StateSwapping, ActionSwapStart: StateSwapping, ActionSchedule: StateApproved, ActionCancel: StateCancelled},
		StateSwapping:   {ActionSwap: StateLive, ActionSwapFailed: StateReady, ActionRollback: StateRolledBack},
		StateLive:       {ActionSupersede: StateSuperseded, ActionRollback: StateRolledBack},
		StateFailed:     {},
		StateCancelled:  {},
		StateSuperseded: {},
		StateRolledBack: {},
	}
	actions := []string{ActionStage, ActionVerify, ActionApprove, ActionSchedule, ActionPromote, ActionCancel,
		ActionRollback, ActionStartVerify, ActionPass, ActionFail, ActionRecheckFail, ActionAutoApprove,
		ActionAutoPromote, ActionSwapStart, ActionSwap, ActionSwapFailed, ActionSupersede}
	if len(States()) != len(want) {
		t.Fatalf("states %v", States())
	}
	for _, st := range States() {
		for _, a := range actions {
			to, ok := Next(st, a)
			exp, expOK := want[st][a]
			if ok != expOK || to != exp {
				t.Errorf("%s --%s--> %q %v, want %q %v", st, a, to, ok, exp, expOK)
			}
		}
	}
	// never cancelled once it is being (or has been) put on the air
	for _, st := range []string{StateSwapping, StateLive} {
		if _, ok := Next(st, ActionCancel); ok {
			t.Errorf("%s can be cancelled", st)
		}
	}
	if !Terminal(StateFailed) || Terminal(StateLive) {
		t.Error("terminal states")
	}
}

func TestAllowedActions(t *testing.T) {
	eq := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if got := AllowedActions(StateDraft, SourceEditor); !eq(got, "approve", "cancel", "schedule", "stage") {
		t.Errorf("draft: %v", got)
	}
	// a producer's reviewed draft is approved (the queue's Apply), never staged by hand
	if got := AllowedActions(StateDraft, SourceWriteChannels); !eq(got, "approve", "cancel", "schedule") {
		t.Errorf("write_channels draft: %v", got)
	}
	// a ready change offers approve, not "swap now" (approval is required)
	if got := AllowedActions(StateReady, SourceEditor); !eq(got, "approve", "cancel", "schedule", "verify") {
		t.Errorf("ready: %v", got)
	}
	if got := AllowedActions(StateApproved, SourceEditor); !eq(got, "cancel", "promote", "schedule") {
		t.Errorf("approved: %v", got)
	}
	if got := AllowedActions(StateLive, SourceEditor); !eq(got, "rollback") {
		t.Errorf("live: %v", got)
	}
}

func defaultConfig() Config {
	c := Config{Enabled: true, AutoPromote: map[string]string{}, QuietWindow: "03:00-05:00"}
	for _, k := range Kinds {
		c.AutoPromote[k] = RuleApproval
	}
	return c
}

// TC3: kinds x rules x window x viewers x warnings; the shipped defaults never promote.
func TestAutoPromoteRules(t *testing.T) {
	night := time.Date(2026, 6, 15, 3, 30, 0, 0, time.UTC)
	day := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)
	c := defaultConfig()
	for _, at := range []time.Time{night, day} {
		if AutoPromote(c, []string{"lineup"}, at, 0, false, nil) {
			t.Fatal("approval promoted by itself")
		}
	}
	c.AutoPromote["schedule"] = RuleImmediate
	if !AutoPromote(c, []string{"schedule"}, day, 5, false, nil) {
		t.Error("immediate")
	}
	if AutoPromote(c, []string{"schedule"}, day, 5, true, nil) {
		t.Error("warnings block an auto rule")
	}
	if AutoPromote(c, []string{"schedule", "lineup"}, day, 0, false, nil) {
		t.Error("the strictest rule (approval) wins")
	}
	later := day.Add(time.Hour)
	if AutoPromote(c, []string{"schedule"}, day, 0, false, &later) {
		t.Error("an explicit swap_at waits")
	}
	c.AutoPromote["art"] = RuleQuietWindow
	if !AutoPromote(c, []string{"art"}, night, 0, false, nil) {
		t.Error("quiet window at night, no viewers")
	}
	if AutoPromote(c, []string{"art"}, night, 1, false, nil) {
		t.Error("a viewer: not quiet")
	}
	if AutoPromote(c, []string{"art"}, day, 0, false, nil) {
		t.Error("outside the window")
	}
	if StrictestRule(c, []string{"art", "schedule"}) != RuleQuietWindow {
		t.Error("strictest of quiet_window and immediate")
	}
	if StrictestRule(c, nil) != RuleApproval {
		t.Error("no kinds: approval")
	}
}

// TestDefaultsAllApproval: the shipped rules (schema.json, from the pipeline's
// MaktabaTvStagingConfig) put every kind behind an admin's approval (owner decision).
func TestDefaultsAllApproval(t *testing.T) {
	c, err := LoadConfig(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || c.SwapWait != "boundary" || !c.HotStandby || c.KeepWarmMinutes != 15 || c.RetentionHours != 24 {
		t.Fatalf("defaults %+v", c)
	}
	for _, k := range Kinds {
		if c.AutoPromote[k] != RuleApproval {
			t.Errorf("%s: %s", k, c.AutoPromote[k])
		}
	}
}

func TestQuietWindowOverMidnight(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.UTC) }
	if !InQuietWindow("23:00-02:00", at(1, 0)) || !InQuietWindow("23:00-02:00", at(23, 30)) || InQuietWindow("23:00-02:00", at(12, 0)) {
		t.Error("window over midnight")
	}
	if InQuietWindow("nonsense", at(3, 0)) {
		t.Error("a malformed window is never quiet")
	}
}

func TestPendingHelpers(t *testing.T) {
	p := &Pending{}
	p.SetChannel("a", map[string]any{"number": 1.0})
	p.RemoveChannel("b")
	p.AddSched("x", "a", "x")
	p.SetArt("a", "logo", map[string]any{"path": "/l.png"})
	p.SetArt("a", "watermark", nil)
	if d, removed, ok := p.Channel("a"); !ok || removed || d["number"] != 1.0 {
		t.Error("channel a")
	}
	if _, removed, ok := p.Channel("b"); !ok || !removed {
		t.Error("channel b removed")
	}
	if got := p.Sched(); len(got) != 2 || got[0] != "a" {
		t.Errorf("sched %v", got)
	}
	art := p.Art("a")
	if art["logo"] == nil || art["watermark"] != nil {
		t.Errorf("art %v", art)
	}
	p.DropChannel("a")
	if _, _, ok := p.Channel("a"); ok {
		t.Error("dropped")
	}
	s := Summarize(p)
	if s["channels_removed"] != 1 {
		t.Errorf("summary %v", s)
	}
}
