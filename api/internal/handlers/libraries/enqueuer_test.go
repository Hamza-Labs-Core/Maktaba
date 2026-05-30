package libraries

import (
	"strings"
	"testing"
)

// These tests pin the API enqueuer's SQL to the slot-0058 contract and
// the pipeline `enqueue_scan` shape (pipeline/.../db/jobs.py
// _INSERT_SCAN_SQL / _FALLBACK_LIVE_SCAN_SQL). A scan job created via
// the API must be byte-indistinguishable from one the pipeline creates;
// if either side's SQL drifts, one of these fails (the W1-C3 lesson:
// no divergent scan-job insert).

func TestInsertScanSQL_MatchesSlot0058Contract(t *testing.T) {
	q := normalise(insertScanSQL)
	// Library-scoped, video_id explicitly NULL, stage 'scan', pending.
	for _, want := range []string{
		"insert into processing_jobs",
		"(library_id, video_id, stage, state, priority, payload, max_attempts)",
		"values ($1, null, 'scan', 'pending', $2, $3, $4)",
		// ON CONFLICT target + predicate must mirror the slot-0058
		// partial unique index processing_jobs_one_live_scan_per_library.
		"on conflict (library_id, stage)",
		"where stage = 'scan'",
		"and state in ('pending','claimed','running','resuming','paused')",
		"do nothing",
		"returning id",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("insertScanSQL missing %q\nfull: %s", want, q)
		}
	}
}

func TestFallbackLiveScanSQL_MatchesPipelineShape(t *testing.T) {
	q := normalise(fallbackLiveScanSQL)
	for _, want := range []string{
		"select id from processing_jobs",
		"where library_id = $1",
		"and stage = 'scan'",
		"and state in ('pending','claimed','running','resuming','paused')",
		"limit 1",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("fallbackLiveScanSQL missing %q\nfull: %s", want, q)
		}
	}
}

// The five live states must be identical between the INSERT's ON
// CONFLICT predicate and the fallback SELECT — they both ride the same
// slot-0058 index predicate. A single named constant guarantees this;
// the test fails loudly if someone inlines a divergent list.
func TestLiveScanStates_AreSharedConstant(t *testing.T) {
	if !strings.Contains(insertScanSQL, liveScanStatesSQL) {
		t.Error("insertScanSQL does not use the shared liveScanStatesSQL constant")
	}
	if !strings.Contains(fallbackLiveScanSQL, liveScanStatesSQL) {
		t.Error("fallbackLiveScanSQL does not use the shared liveScanStatesSQL constant")
	}
}

func normalise(s string) string {
	s = strings.ToLower(s)
	// Collapse runs of whitespace/newlines to single spaces so the
	// substring assertions are layout-insensitive (but still pin token
	// order and the parenthesised column/value lists).
	return strings.Join(strings.Fields(s), " ")
}
