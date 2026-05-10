// Package states is the canonical Go binding of the video FSM owned by
// story-01-06. The 12 video states, the 7 processing-stage names, and
// the transition graph are mirrored from shared/states/states.json
// and pinned by tests so they cannot drift from the Python binding in
// pipeline/src/maktaba_pipeline/domain/states.py or from the migration
// CHECK constraint in shared/db/migrations/0004_video_states_and_stages.sql.
//
// AdvanceAfterStage is the SOLE function callers should use to mutate
// videos.state. It validates the (from, trigger, outcome) triple against
// the runtime transition table and returns IllegalStateTransition for
// anything outside the allowed set. See plan-01-06 §6.4 for why the DB
// only enforces set membership and not transition validity.
package states

// State is the canonical video-state enum. The string values are the
// values that land in videos.state and that the slot 0004 CHECK
// constraint enumerates.
type State string

// The 12 canonical video states. Iteration order in AllStates matches
// the order in shared/states/states.json so tests can compare directly.
const (
	StateDiscovered     State = "discovered"
	StateProbed         State = "probed"
	StateAudioExtracted State = "audio_extracted"
	StateTranscribed    State = "transcribed"
	StateIndexed        State = "indexed"
	StateThumbnailed    State = "thumbnailed"
	StateReady          State = "ready"
	StateReadyNoAudio   State = "ready_no_audio"
	StateMissing        State = "missing"
	StateSuperseded     State = "superseded"
	StateCorrupted      State = "corrupted"
	StateFailed         State = "failed"
)

// AllStates is the full set in manifest order.
var AllStates = []State{
	StateDiscovered,
	StateProbed,
	StateAudioExtracted,
	StateTranscribed,
	StateIndexed,
	StateThumbnailed,
	StateReady,
	StateReadyNoAudio,
	StateMissing,
	StateSuperseded,
	StateCorrupted,
	StateFailed,
}

// Class returns the FSM class of a state — one of "open",
// "terminal-good", "terminal-soft", "terminal-bad", or "sink".
func (s State) Class() string { return classOf[s] }

// IsTerminalDrop reports whether a state should silently drop incoming
// stage finishes. SUPERSEDED, CORRUPTED, and FAILED rows are
// terminal-soft / terminal-bad: a worker that finishes after the row
// hit one of these states is racing with a side-channel transition;
// AdvanceAfterStage logs and no-ops instead of erroring.
func (s State) IsTerminalDrop() bool {
	switch s {
	case StateSuperseded, StateCorrupted, StateFailed:
		return true
	default:
		return false
	}
}

var classOf = map[State]string{
	StateDiscovered:     "open",
	StateProbed:         "open",
	StateAudioExtracted: "open",
	StateTranscribed:    "open",
	StateIndexed:        "open",
	StateThumbnailed:    "open",
	StateReady:          "terminal-good",
	StateReadyNoAudio:   "terminal-good",
	StateMissing:        "sink",
	StateSuperseded:     "terminal-soft",
	StateCorrupted:      "terminal-bad",
	StateFailed:         "terminal-bad",
}

// Stage is the canonical processing_jobs.stage enum.
type Stage string

const (
	StageScan         Stage = "scan"
	StageProbe        Stage = "probe"
	StageExtract      Stage = "extract"
	StageTranscribe   Stage = "transcribe"
	StageSubtitleGen  Stage = "subtitle_gen"
	StageIndex        Stage = "index"
	StageThumbnail    Stage = "thumbnail"
)

// AllStages is the full set in manifest order. The slot 0002 + slot
// 0004 CHECK constraints enumerate the same seven values.
var AllStages = []Stage{
	StageScan,
	StageProbe,
	StageExtract,
	StageTranscribe,
	StageSubtitleGen,
	StageIndex,
	StageThumbnail,
}

// Trigger is a superset of Stage that includes the three side-channel
// triggers (filesystem, library, integrity) carrying the "any-state"
// transitions to MISSING / SUPERSEDED / CORRUPTED. Triggers are an
// in-process concept; only the seven Stage values appear in the DB.
type Trigger string

const (
	TriggerScan         Trigger = "scan"
	TriggerProbe        Trigger = "probe"
	TriggerExtract      Trigger = "extract"
	TriggerTranscribe   Trigger = "transcribe"
	TriggerSubtitleGen  Trigger = "subtitle_gen"
	TriggerIndex        Trigger = "index"
	TriggerThumbnail    Trigger = "thumbnail"
	TriggerFilesystem   Trigger = "filesystem"
	TriggerLibrary      Trigger = "library"
	TriggerIntegrity    Trigger = "integrity"
)

// AllTriggers is the full ten-element superset.
var AllTriggers = []Trigger{
	TriggerScan,
	TriggerProbe,
	TriggerExtract,
	TriggerTranscribe,
	TriggerSubtitleGen,
	TriggerIndex,
	TriggerThumbnail,
	TriggerFilesystem,
	TriggerLibrary,
	TriggerIntegrity,
}

// IsStage reports whether a trigger corresponds to one of the seven
// canonical pipeline stages (i.e. not a side-channel trigger).
func (t Trigger) IsStage() bool {
	switch t {
	case TriggerScan, TriggerProbe, TriggerExtract, TriggerTranscribe,
		TriggerSubtitleGen, TriggerIndex, TriggerThumbnail:
		return true
	default:
		return false
	}
}

// Outcome is the result-token a caller passes alongside a trigger.
// Open at the type level; the runtime transition table enumerates the
// outcomes the FSM understands. Anything else is an
// IllegalStateTransition even from an otherwise-compatible source.
type Outcome string

// Recognized outcomes. Listed for documentation; new outcomes only
// matter when added to the transition table below.
const (
	OutcomeOK            Outcome = "ok"
	OutcomeNoAudio       Outcome = "no_audio"
	OutcomePartial       Outcome = "partial"
	OutcomeExhausted     Outcome = "exhausted"
	OutcomeRediscovered  Outcome = "rediscovered"
	OutcomeDeleted       Outcome = "deleted"
	OutcomeReplaced      Outcome = "replaced"
	OutcomeFail          Outcome = "fail"
	OutcomeAllGatesOK    Outcome = "all_gates_ok"
)

// transitionKey is the lookup key into the explicit transition table.
type transitionKey struct {
	From    State
	Trigger Trigger
	Outcome Outcome
}

// explicitTransitions are the 11 hand-named edges from plan-01-06 §4.
// Broadcast edges are evaluated by Lookup against state classes and
// trigger groups so we don't have to enumerate ~70 expanded rows here.
var explicitTransitions = map[transitionKey]State{
	{StateDiscovered, TriggerProbe, OutcomeOK}:                 StateProbed,
	{StateProbed, TriggerExtract, OutcomeOK}:                   StateAudioExtracted,
	{StateProbed, TriggerProbe, OutcomeNoAudio}:                StateReadyNoAudio,
	{StateAudioExtracted, TriggerTranscribe, OutcomeOK}:        StateTranscribed,
	{StateTranscribed, TriggerSubtitleGen, OutcomePartial}:     StateTranscribed,
	{StateTranscribed, TriggerIndex, OutcomePartial}:           StateTranscribed,
	{StateTranscribed, TriggerSubtitleGen, OutcomeOK}:          StateIndexed,
	{StateTranscribed, TriggerIndex, OutcomeOK}:                StateIndexed,
	{StateIndexed, TriggerThumbnail, OutcomeOK}:                StateThumbnailed,
	{StateThumbnailed, TriggerScan, OutcomeAllGatesOK}:         StateReady,
	{StateMissing, TriggerScan, OutcomeRediscovered}:           StateDiscovered,
}

// Lookup returns the target state for (from, trigger, outcome) and
// reports whether a matching edge exists. It first consults the
// explicit table, then the four broadcast rules. A miss on both is
// what AdvanceAfterStage turns into IllegalStateTransition.
func Lookup(from State, trigger Trigger, outcome Outcome) (State, bool) {
	if to, ok := explicitTransitions[transitionKey{from, trigger, outcome}]; ok {
		return to, true
	}
	return broadcastLookup(from, trigger, outcome)
}

// broadcastLookup expands the four "any-source" rows from the manifest:
//
//   - filesystem/deleted  → MISSING     (open ∪ terminal-good ∪ sink)
//   - <stage>/exhausted   → FAILED      (open ∪ terminal-good)
//   - integrity/fail      → CORRUPTED   (open ∪ terminal-good)
//   - library/replaced    → SUPERSEDED  (open ∪ terminal-good ∪ terminal-soft)
//
// The stage-wildcard exhausted edge matches any of the seven canonical
// stage triggers, never a side-channel trigger.
func broadcastLookup(from State, trigger Trigger, outcome Outcome) (State, bool) {
	cls := from.Class()

	switch {
	case trigger == TriggerFilesystem && outcome == OutcomeDeleted:
		if cls == "open" || cls == "terminal-good" || cls == "sink" {
			return StateMissing, true
		}

	case trigger.IsStage() && outcome == OutcomeExhausted:
		if cls == "open" || cls == "terminal-good" {
			return StateFailed, true
		}

	case trigger == TriggerIntegrity && outcome == OutcomeFail:
		if cls == "open" || cls == "terminal-good" {
			return StateCorrupted, true
		}

	case trigger == TriggerLibrary && outcome == OutcomeReplaced:
		if cls == "open" || cls == "terminal-good" || cls == "terminal-soft" {
			return StateSuperseded, true
		}
	}

	return "", false
}
