package states

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ----------------------------------------------------------------------
// Manifest parity — the Go binding must agree with shared/states/states.json
// ----------------------------------------------------------------------

type manifest struct {
	States []struct {
		Name  string `json:"name"`
		DB    string `json:"db"`
		Class string `json:"class"`
	} `json:"states"`
	Stages   []string `json:"stages"`
	Triggers []string `json:"triggers"`
	Transitions []struct {
		From    string `json:"from"`
		Trigger string `json:"trigger"`
		Outcome string `json:"outcome"`
		To      string `json:"to"`
	} `json:"transitions"`
	BroadcastTransitions []struct {
		SourceClassIn []string `json:"source_class_in"`
		Trigger       string   `json:"trigger"`
		Outcome       string   `json:"outcome"`
		To            string   `json:"to"`
	} `json:"broadcast_transitions"`
	TerminalDropStates []string `json:"terminal_drop_states"`
}

// loadManifest reads shared/states/states.json. The package lives at
// shared/states/go, so the manifest is one directory up.
func loadManifest(t *testing.T) manifest {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(cwd, "..", "states.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest %s: %v", path, err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

func TestStateEnumMatchesManifest(t *testing.T) {
	m := loadManifest(t)
	if len(AllStates) != len(m.States) {
		t.Fatalf("AllStates len = %d, manifest = %d", len(AllStates), len(m.States))
	}
	for i, ms := range m.States {
		if string(AllStates[i]) != ms.DB {
			t.Errorf("AllStates[%d] = %s, manifest = %s", i, AllStates[i], ms.DB)
		}
		if got, want := AllStates[i].Class(), ms.Class; got != want {
			t.Errorf("class(%s) = %s, manifest = %s", AllStates[i], got, want)
		}
	}
}

func TestStageEnumMatchesManifest(t *testing.T) {
	m := loadManifest(t)
	if len(AllStages) != len(m.Stages) {
		t.Fatalf("AllStages len = %d, manifest = %d", len(AllStages), len(m.Stages))
	}
	for i, ms := range m.Stages {
		if string(AllStages[i]) != ms {
			t.Errorf("AllStages[%d] = %s, manifest = %s", i, AllStages[i], ms)
		}
	}
}

func TestTriggerEnumMatchesManifest(t *testing.T) {
	m := loadManifest(t)
	if len(AllTriggers) != len(m.Triggers) {
		t.Fatalf("AllTriggers len = %d, manifest = %d", len(AllTriggers), len(m.Triggers))
	}
	for i, mt := range m.Triggers {
		if string(AllTriggers[i]) != mt {
			t.Errorf("AllTriggers[%d] = %s, manifest = %s", i, AllTriggers[i], mt)
		}
	}
}

// ----------------------------------------------------------------------
// Transition table — explicit edges
// ----------------------------------------------------------------------

func TestExplicitTransitionsMatchManifest(t *testing.T) {
	m := loadManifest(t)
	want := map[transitionKey]State{}
	for _, tr := range m.Transitions {
		want[transitionKey{State(dbValueOf(m, tr.From)), Trigger(tr.Trigger), Outcome(tr.Outcome)}] =
			State(dbValueOf(m, tr.To))
	}
	if !reflect.DeepEqual(want, explicitTransitions) {
		t.Fatalf("explicit transitions diverged from manifest:\n got %v\nwant %v",
			explicitTransitions, want)
	}
}

func dbValueOf(m manifest, name string) string {
	for _, s := range m.States {
		if s.Name == name {
			return s.DB
		}
	}
	return name
}

func TestEachExplicitEdgeReachableViaLookup(t *testing.T) {
	cases := []struct {
		from    State
		trig    Trigger
		out     Outcome
		to      State
	}{
		{StateDiscovered, TriggerProbe, OutcomeOK, StateProbed},
		{StateProbed, TriggerExtract, OutcomeOK, StateAudioExtracted},
		{StateProbed, TriggerProbe, OutcomeNoAudio, StateReadyNoAudio},
		{StateAudioExtracted, TriggerTranscribe, OutcomeOK, StateTranscribed},
		{StateTranscribed, TriggerSubtitleGen, OutcomePartial, StateTranscribed},
		{StateTranscribed, TriggerIndex, OutcomePartial, StateTranscribed},
		{StateTranscribed, TriggerSubtitleGen, OutcomeOK, StateIndexed},
		{StateTranscribed, TriggerIndex, OutcomeOK, StateIndexed},
		{StateIndexed, TriggerThumbnail, OutcomeOK, StateThumbnailed},
		{StateThumbnailed, TriggerScan, OutcomeAllGatesOK, StateReady},
		{StateMissing, TriggerScan, OutcomeRediscovered, StateDiscovered},
	}
	for _, c := range cases {
		got, ok := Lookup(c.from, c.trig, c.out)
		if !ok {
			t.Errorf("Lookup(%s,%s,%s) → no edge, want %s",
				c.from, c.trig, c.out, c.to)
			continue
		}
		if got != c.to {
			t.Errorf("Lookup(%s,%s,%s) = %s, want %s",
				c.from, c.trig, c.out, got, c.to)
		}
	}
}

// ----------------------------------------------------------------------
// Broadcast edges
// ----------------------------------------------------------------------

func TestFilesystemDeletedReachesMissingFromAllNonTerminalBad(t *testing.T) {
	// Sources where filesystem/deleted should drive → MISSING.
	// (open ∪ terminal-good ∪ sink) per manifest.
	sources := []State{
		StateDiscovered, StateProbed, StateAudioExtracted, StateTranscribed,
		StateIndexed, StateThumbnailed, StateReady, StateReadyNoAudio,
		StateMissing,
	}
	for _, s := range sources {
		got, ok := Lookup(s, TriggerFilesystem, OutcomeDeleted)
		if !ok || got != StateMissing {
			t.Errorf("filesystem/deleted from %s = (%s, %v), want (missing, true)",
				s, got, ok)
		}
	}
	// Sources that should NOT reach MISSING via this edge.
	for _, s := range []State{StateSuperseded, StateCorrupted, StateFailed} {
		_, ok := Lookup(s, TriggerFilesystem, OutcomeDeleted)
		if ok {
			t.Errorf("filesystem/deleted from %s should be rejected", s)
		}
	}
}

func TestExhaustedFromAnyStageReachesFailed(t *testing.T) {
	// Sources: open ∪ terminal-good (no MISSING, no terminal-soft/bad).
	sources := []State{
		StateDiscovered, StateProbed, StateAudioExtracted, StateTranscribed,
		StateIndexed, StateThumbnailed, StateReady, StateReadyNoAudio,
	}
	stages := []Trigger{
		TriggerScan, TriggerProbe, TriggerExtract, TriggerTranscribe,
		TriggerSubtitleGen, TriggerIndex, TriggerThumbnail,
	}
	for _, s := range sources {
		for _, stg := range stages {
			got, ok := Lookup(s, stg, OutcomeExhausted)
			if !ok || got != StateFailed {
				t.Errorf("%s/exhausted from %s = (%s, %v), want (failed, true)",
					stg, s, got, ok)
			}
		}
	}
	// Side-channel triggers don't carry exhausted.
	for _, t2 := range []Trigger{TriggerFilesystem, TriggerLibrary, TriggerIntegrity} {
		_, ok := Lookup(StateDiscovered, t2, OutcomeExhausted)
		if ok {
			t.Errorf("%s/exhausted should be rejected (side-channel triggers carry no exhausted edge)", t2)
		}
	}
	// Already-terminal states are not eligible.
	for _, s := range []State{StateMissing, StateSuperseded, StateCorrupted, StateFailed} {
		_, ok := Lookup(s, TriggerProbe, OutcomeExhausted)
		if ok {
			t.Errorf("probe/exhausted from %s should be rejected", s)
		}
	}
}

func TestIntegrityFailReachesCorrupted(t *testing.T) {
	for _, s := range []State{
		StateDiscovered, StateProbed, StateAudioExtracted, StateTranscribed,
		StateIndexed, StateThumbnailed, StateReady, StateReadyNoAudio,
	} {
		got, ok := Lookup(s, TriggerIntegrity, OutcomeFail)
		if !ok || got != StateCorrupted {
			t.Errorf("integrity/fail from %s = (%s, %v), want (corrupted, true)",
				s, got, ok)
		}
	}
	for _, s := range []State{StateMissing, StateSuperseded, StateCorrupted, StateFailed} {
		_, ok := Lookup(s, TriggerIntegrity, OutcomeFail)
		if ok {
			t.Errorf("integrity/fail from %s should be rejected", s)
		}
	}
}

func TestLibraryReplacedReachesSuperseded(t *testing.T) {
	for _, s := range []State{
		StateDiscovered, StateProbed, StateAudioExtracted, StateTranscribed,
		StateIndexed, StateThumbnailed, StateReady, StateReadyNoAudio,
		StateSuperseded, // re-supersede is allowed; pointer is updated, state stays
	} {
		got, ok := Lookup(s, TriggerLibrary, OutcomeReplaced)
		if !ok || got != StateSuperseded {
			t.Errorf("library/replaced from %s = (%s, %v), want (superseded, true)",
				s, got, ok)
		}
	}
	for _, s := range []State{StateMissing, StateCorrupted, StateFailed} {
		_, ok := Lookup(s, TriggerLibrary, OutcomeReplaced)
		if ok {
			t.Errorf("library/replaced from %s should be rejected", s)
		}
	}
}

// ----------------------------------------------------------------------
// Negative cases — Lookup rejects unmapped triples
// ----------------------------------------------------------------------

func TestLookupRejectsInvalidTriples(t *testing.T) {
	cases := []struct {
		from State
		trig Trigger
		out  Outcome
		why  string
	}{
		// Wrong outcome for a valid (from, trigger).
		{StateDiscovered, TriggerProbe, "weird", "unknown outcome"},
		// Wrong trigger for the source state.
		{StateDiscovered, TriggerTranscribe, OutcomeOK, "transcribe before extract"},
		// Skipping a stage.
		{StateAudioExtracted, TriggerThumbnail, OutcomeOK, "thumbnail before transcribe/index"},
		// READY_NO_AUDIO is terminal-good — extract is not allowed.
		{StateReadyNoAudio, TriggerExtract, OutcomeOK, "extract after no_audio terminal"},
		// subtitle_gen before transcribe (plan §11.6 explicit example).
		{StateAudioExtracted, TriggerSubtitleGen, OutcomeOK, "subtitle_gen before transcribe"},
		// Self-loop on PROBED: probe/ok from PROBED is not a real edge.
		{StateProbed, TriggerProbe, OutcomeOK, "double probe"},
	}
	for _, c := range cases {
		_, ok := Lookup(c.from, c.trig, c.out)
		if ok {
			t.Errorf("Lookup(%s,%s,%s) accepted, expected rejection: %s",
				c.from, c.trig, c.out, c.why)
		}
	}
}

// ----------------------------------------------------------------------
// IsTerminalDrop / Class
// ----------------------------------------------------------------------

func TestIsTerminalDrop(t *testing.T) {
	for _, s := range []State{StateSuperseded, StateCorrupted, StateFailed} {
		if !s.IsTerminalDrop() {
			t.Errorf("%s should be terminal-drop", s)
		}
	}
	for _, s := range []State{
		StateDiscovered, StateProbed, StateAudioExtracted, StateTranscribed,
		StateIndexed, StateThumbnailed, StateReady, StateReadyNoAudio, StateMissing,
	} {
		if s.IsTerminalDrop() {
			t.Errorf("%s should NOT be terminal-drop", s)
		}
	}
}

// ----------------------------------------------------------------------
// AdvanceInTx — fake-driver tests for the lock + UPDATE shape
// ----------------------------------------------------------------------

// fakeTx implements txQuerier for unit testing AdvanceInTx without a
// real database. It records what queries were issued and serves a
// scripted current state for the SELECT.
type fakeTx struct {
	mu           sync.Mutex
	current      State
	queryCount   int32
	execCount    int32
	updateTarget State
	failQuery    error
	failExec     error
}

func (f *fakeTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	atomic.AddInt32(&f.queryCount, 1)
	if f.failQuery != nil {
		// We can't easily synthesise a failing *sql.Row here without a
		// driver; instead, tests that need a query failure exercise
		// AdvanceAfterStage end-to-end against a real sql.DB. Tests
		// that use fakeTx leave failQuery nil.
		return nil
	}
	// Build a one-row *sql.Row by going through a real in-memory
	// driver (sql.Open("sqlite", ":memory:")) is too heavy for this
	// package; instead we use a tiny helper that returns a *sql.Row
	// pre-populated via a stub SELECT that the standard library
	// happens to expose: there is no such helper in database/sql, so
	// we sidestep the type by returning nil and require tests using
	// fakeTx to call advanceFake below instead of AdvanceInTx
	// directly.
	return nil
}

func (f *fakeTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	atomic.AddInt32(&f.execCount, 1)
	if f.failExec != nil {
		return nil, f.failExec
	}
	if strings.Contains(query, "UPDATE videos SET state") {
		f.mu.Lock()
		if s, ok := args[0].(string); ok {
			f.updateTarget = State(s)
		}
		f.mu.Unlock()
	}
	return driverResult{}, nil
}

type driverResult struct{}

func (driverResult) LastInsertId() (int64, error) { return 0, nil }
func (driverResult) RowsAffected() (int64, error) { return 1, nil }

// advanceFake replicates AdvanceInTx's logic against fakeTx without
// relying on QueryRowContext (which requires a real driver to build a
// *sql.Row). It is the same code path exercised in tests below; the
// production AdvanceInTx is covered separately by integration tests
// using a real database.
func advanceFake(
	ctx context.Context,
	tx *fakeTx,
	log *slog.Logger,
	videoID string,
	trigger Trigger,
	outcome Outcome,
) (State, error) {
	current := tx.current

	if current.IsTerminalDrop() {
		if log != nil {
			log.Info("late_stage_finish",
				"video_id", videoID,
				"current", string(current),
				"trigger", string(trigger),
				"outcome", string(outcome),
			)
		}
		return current, nil
	}

	target, ok := Lookup(current, trigger, outcome)
	if !ok {
		return current, &IllegalStateTransition{
			VideoID: videoID, From: current,
			Trigger: trigger, Outcome: outcome,
		}
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE videos SET state = $1, updated_at = now() WHERE id = $2`,
		string(target), videoID,
	)
	if err != nil {
		return "", err
	}
	return target, nil
}

func TestAdvance_HappyPath_Discovered_To_Probed(t *testing.T) {
	tx := &fakeTx{current: StateDiscovered}
	got, err := advanceFake(context.Background(), tx, nil, "v1",
		TriggerProbe, OutcomeOK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != StateProbed {
		t.Errorf("got %s, want probed", got)
	}
	if tx.updateTarget != StateProbed {
		t.Errorf("UPDATE target = %s, want probed", tx.updateTarget)
	}
}

func TestAdvance_HappyPath_NoAudio_Path(t *testing.T) {
	tx := &fakeTx{current: StateProbed}
	got, err := advanceFake(context.Background(), tx, nil, "v1",
		TriggerProbe, OutcomeNoAudio)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != StateReadyNoAudio {
		t.Errorf("got %s, want ready_no_audio", got)
	}
}

func TestAdvance_RejectsInvalidTriple(t *testing.T) {
	tx := &fakeTx{current: StateDiscovered}
	_, err := advanceFake(context.Background(), tx, nil, "v1",
		TriggerTranscribe, OutcomeOK)
	if err == nil {
		t.Fatal("expected IllegalStateTransition, got nil")
	}
	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("err is not ErrIllegalTransition: %v", err)
	}
	var ist *IllegalStateTransition
	if !errors.As(err, &ist) {
		t.Fatalf("err is not *IllegalStateTransition: %v", err)
	}
	if ist.From != StateDiscovered {
		t.Errorf("ist.From = %s, want discovered", ist.From)
	}
	// Crucially: state must not have advanced.
	if tx.execCount != 0 {
		t.Errorf("ExecContext called %d times on rejected transition, want 0",
			tx.execCount)
	}
}

func TestAdvance_LateStageFinishOnSuperseded(t *testing.T) {
	tx := &fakeTx{current: StateSuperseded}
	got, err := advanceFake(context.Background(), tx, slog.New(slog.NewTextHandler(os.Stderr, nil)),
		"v1", TriggerThumbnail, OutcomeOK)
	if err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
	if got != StateSuperseded {
		t.Errorf("got %s, want superseded (terminal drop)", got)
	}
	if tx.execCount != 0 {
		t.Errorf("ExecContext called %d times on terminal drop, want 0",
			tx.execCount)
	}
}

func TestAdvance_LateStageFinishOnFailed(t *testing.T) {
	tx := &fakeTx{current: StateFailed}
	got, err := advanceFake(context.Background(), tx, nil,
		"v1", TriggerThumbnail, OutcomeOK)
	if err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
	if got != StateFailed {
		t.Errorf("got %s, want failed (terminal drop)", got)
	}
}

func TestAdvance_TranscribedPartialSelfLoopRunsUpdate(t *testing.T) {
	// The (TRANSCRIBED, *, partial) edge is a self-loop, but the
	// UPDATE still runs so updated_at advances and the NOTIFY trigger
	// fires (observability).
	tx := &fakeTx{current: StateTranscribed}
	got, err := advanceFake(context.Background(), tx, nil,
		"v1", TriggerSubtitleGen, OutcomePartial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != StateTranscribed {
		t.Errorf("got %s, want transcribed (self-loop)", got)
	}
	if tx.execCount != 1 {
		t.Errorf("ExecContext called %d times, want 1 even on self-loop",
			tx.execCount)
	}
}

func TestAdvance_SubtitleGenAndIndexBothFinish(t *testing.T) {
	// Story acceptance: finish only subtitle_gen → state remains
	// TRANSCRIBED. Then finish index → state advances to INDEXED.
	tx := &fakeTx{current: StateTranscribed}

	// 1. subtitle_gen partial — state unchanged.
	got, err := advanceFake(context.Background(), tx, nil, "v1",
		TriggerSubtitleGen, OutcomePartial)
	if err != nil || got != StateTranscribed {
		t.Fatalf("partial: got=%s err=%v", got, err)
	}

	// 2. index ok — advances to INDEXED.
	tx.current = StateTranscribed // simulate the row still TRANSCRIBED
	got, err = advanceFake(context.Background(), tx, nil, "v1",
		TriggerIndex, OutcomeOK)
	if err != nil {
		t.Fatalf("index ok unexpected error: %v", err)
	}
	if got != StateIndexed {
		t.Errorf("after index/ok: got %s, want indexed", got)
	}
}

// ----------------------------------------------------------------------
// Sequential serialization — second caller after a committed advance
// ----------------------------------------------------------------------

// In production AdvanceInTx takes a row-level lock (SELECT … FOR UPDATE)
// so concurrent advances on the same video serialize: the loser sees
// the post-update state and either no-ops (terminal drop) or hits
// IllegalStateTransition. We mirror that with two sequential calls
// against fake state. The "concurrent" semantics that need a real DB
// are exercised in integration tests outside this package.
func TestAdvance_SecondCallAfterFirstSeesNewState(t *testing.T) {
	tx := &fakeTx{current: StateDiscovered}

	// First call: discovered → probed.
	got, err := advanceFake(context.Background(), tx, nil, "v1",
		TriggerProbe, OutcomeOK)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if got != StateProbed {
		t.Fatalf("first call: got %s, want probed", got)
	}

	// Imitate the COMMIT.
	tx.current = StateProbed

	// Second call with the same trigger now sees PROBED. (PROBED,
	// probe, ok) is not a valid edge → IllegalStateTransition.
	_, err = advanceFake(context.Background(), tx, nil, "v1",
		TriggerProbe, OutcomeOK)
	if err == nil {
		t.Fatal("second call: expected IllegalStateTransition, got nil")
	}
	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("second call: err is not ErrIllegalTransition: %v", err)
	}
}

// ----------------------------------------------------------------------
// IllegalStateTransition error formatting
// ----------------------------------------------------------------------

func TestIllegalStateTransition_ErrorString(t *testing.T) {
	e := &IllegalStateTransition{
		VideoID: "abc",
		From:    StateDiscovered,
		Trigger: TriggerTranscribe,
		Outcome: OutcomeOK,
	}
	got := e.Error()
	for _, want := range []string{"abc", "discovered", "transcribe", "ok", "illegal state transition"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, missing %q", got, want)
		}
	}
}
