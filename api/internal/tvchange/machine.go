package tvchange

import "sort"

// Actions an admin (or the controller, or a pipeline job) performs on a change.
const (
	ActionStage       = "stage"   // draft -> (tv_stage job) -> staged
	ActionVerify      = "verify"  // re-run the checks
	ActionApprove     = "approve" // ready -> approved (a draft: approve ahead)
	ActionSchedule    = "schedule"
	ActionPromote     = "promote" // swap now
	ActionCancel      = "cancel"
	ActionRollback    = "rollback"
	ActionStartVerify = "start_verify"   // controller: staged -> verifying
	ActionPass        = "pass"           // controller: verifying -> ready
	ActionFail        = "fail"           // controller: verifying -> failed
	ActionRecheckFail = "recheck_fail"   // controller: ready -> verifying
	ActionAutoApprove = "approved_ahead" // controller: ready -> approved (approved as a draft)
	ActionAutoPromote = "auto_promote"   // controller: ready -> swapping (a rule switched on)
	ActionSwapStart   = "swap_start"     // controller: approved -> swapping
	ActionSwap        = "swap"           // tv_swap job: swapping -> live
	ActionSwapFailed  = "swap_failed"    // tv_swap job: swapping -> ready
	ActionSupersede   = "supersede"
)

// table is design §11.2: state -> action -> next state. "Stay" actions (approve on a
// draft, schedule) keep the state.
var table = map[string]map[string]string{
	StateDraft: {
		ActionStage:     StateStaged,
		ActionApprove:   StateDraft,
		ActionSchedule:  StateDraft,
		ActionCancel:    StateCancelled,
		ActionSupersede: StateSuperseded,
	},
	StateStaged: {
		ActionStartVerify: StateVerifying,
		ActionSchedule:    StateStaged,
		ActionApprove:     StateStaged,
		ActionCancel:      StateCancelled,
		ActionFail:        StateFailed,
	},
	StateVerifying: {
		ActionPass:     StateReady,
		ActionFail:     StateFailed,
		ActionVerify:   StateVerifying,
		ActionSchedule: StateVerifying,
		ActionApprove:  StateVerifying,
		ActionCancel:   StateCancelled,
	},
	StateReady: {
		ActionApprove:     StateApproved,
		ActionVerify:      StateVerifying,
		ActionSchedule:    StateReady,
		ActionCancel:      StateCancelled,
		ActionRecheckFail: StateVerifying,
		ActionAutoApprove: StateApproved,
		ActionAutoPromote: StateSwapping,
		ActionPromote:     StateSwapping, // only when a rule allows it (Controller.Promote)
	},
	StateApproved: {
		ActionPromote:   StateSwapping,
		ActionSwapStart: StateSwapping,
		ActionSchedule:  StateApproved,
		ActionCancel:    StateCancelled,
	},
	StateSwapping: {
		ActionSwap:       StateLive,
		ActionSwapFailed: StateReady,
		ActionRollback:   StateRolledBack,
	},
	StateLive: {
		ActionSupersede: StateSuperseded,
		ActionRollback:  StateRolledBack,
	},
	StateFailed:     {},
	StateCancelled:  {},
	StateSuperseded: {},
	StateRolledBack: {},
}

// adminActions are what the Changes page offers (allowed_actions).
var adminActions = map[string]bool{
	ActionStage: true, ActionVerify: true, ActionApprove: true, ActionSchedule: true,
	ActionPromote: true, ActionCancel: true, ActionRollback: true,
}

// Next is the state action leads to from state, and whether it is allowed.
func Next(state, action string) (string, bool) {
	to, ok := table[state][action]
	return to, ok
}

// States lists every state (for tests).
func States() []string {
	out := make([]string, 0, len(table))
	for s := range table {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// AllowedActions are the admin actions of a state, sorted. A producer draft
// (write_channels in review) is approved by the queue's Apply, so it offers approve
// but not stage; a promote from ready needs an auto rule, so it is not offered there.
func AllowedActions(state, sourceKind string) []string {
	out := []string{}
	for a := range table[state] {
		if !adminActions[a] {
			continue
		}
		if state == StateReady && a == ActionPromote {
			continue
		}
		if state == StateDraft && a == ActionStage && sourceKind == "write_channels" {
			continue
		}
		out = append(out, a)
	}
	if state == StateLive || state == StateSwapping {
		// rollback is offered; it answers 501 until story 33.9 lands
		out = appendOnce(out, ActionRollback)
	}
	sort.Strings(out)
	return out
}

func appendOnce(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

// Terminal reports whether a state ends a change's life.
func Terminal(state string) bool {
	return len(table[state]) == 0
}
