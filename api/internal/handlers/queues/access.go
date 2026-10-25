package queues

// Library visibility for the control centre's reads. Every route of the
// package is admin-only (Mount); this filter stays as a second line for a
// principal that sees only some libraries: job rows of other libraries, or
// of no library (catalog-wide work), are left out, and failure samples
// (subject labels and error text) are blanked.

import (
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// fullView reports whether p sees every item (admin or all libraries).
func fullView(p *principal.Principal) bool {
	return p != nil && (p.IsAdmin || p.AccessAllLibraries)
}

// refVisible reports whether p may see the job row ref.
func refVisible(p *principal.Principal, ref JobRef) bool {
	if fullView(p) {
		return true
	}
	return p != nil && ref.LibraryID != nil && p.HasLibrary(*ref.LibraryID)
}

// filterRefs keeps the job rows p may see (refs unchanged for a full view).
func filterRefs(p *principal.Principal, refs []JobRef) []JobRef {
	if fullView(p) || refs == nil {
		return refs
	}
	out := make([]JobRef, 0, len(refs))
	for _, ref := range refs {
		if refVisible(p, ref) {
			out = append(out, ref)
		}
	}
	return out
}

// scopeSnapshot returns snap with the running job rows p may not see
// removed (a copy: the snapshot may be shared).
func scopeSnapshot(p *principal.Principal, snap Snapshot) Snapshot {
	if fullView(p) {
		return snap
	}
	qs := make([]QueueSummary, len(snap.Queues))
	for i, q := range snap.Queues {
		q.RunningJobs = filterRefs(p, q.RunningJobs)
		qs[i] = q
	}
	snap.Queues = qs
	return snap
}

// scopeFailures blanks the per-item detail of failure groups (sample
// subjects and message) for a caller without a full view: a group's
// samples carry no library, so they cannot be filtered per library.
func scopeFailures(p *principal.Principal, groups []FailureGroup) []FailureGroup {
	if fullView(p) {
		return groups
	}
	out := make([]FailureGroup, len(groups))
	for i, g := range groups {
		g.SampleSubjects = []SubjectSample{}
		g.SampleMessage = ""
		out[i] = g
	}
	return out
}

// canSeeLibraryOf reports whether p may see an item of library lib (nil:
// no library, so a full view only).
func canSeeLibraryOf(p *principal.Principal, lib *string) bool {
	if fullView(p) {
		return true
	}
	return p != nil && lib != nil && p.HasLibrary(*lib)
}
