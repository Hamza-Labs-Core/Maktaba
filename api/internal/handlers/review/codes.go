// Package review serves word review (Epic 52): the suspect words word_check saved, the
// audio around each, the fixes a reviewer makes (transcript edits, undo), what the fixes
// teach (the vocabulary), each library's word review settings and the language packs'
// report. Every route is for home admins only (README §8, decision 1): a library's write
// or admin role grants nothing here, and canReview never reads library_acl.
package review

// Reasons, Statuses and Sources are the codes the pipeline's writer
// (word_review/settings.py) and the web (web/src/lib/reviewApi.ts) name too; a test in
// each fails on a code the others do not know.
var (
	Reasons = []string{
		"low_confidence", "not_in_dictionary", "listeners_disagree", "quran_mismatch",
		"named_entity", "near_vocabulary", "manual", "previous_fix", "over_budget",
	}
	Statuses = []string{"open", "accepted", "corrected", "name", "dictionary", "skipped", "stale"}
	Sources  = []string{"whisper", "second_listen", "dictionary", "vocabulary", "quran", "gazetteer", "previous_fix"}
	// Actions a reviewer takes on a flag.
	Actions = []string{"accept", "correct", "name", "dictionary", "skip"}
	// Switchable are the reasons a library may turn off (word_review.reasons).
	Switchable = []string{
		"low_confidence", "not_in_dictionary", "listeners_disagree", "quran_mismatch",
		"named_entity", "near_vocabulary",
	}
)

func in(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// statusOf is the flag status an action leaves.
func statusOf(action string) string {
	switch action {
	case "accept":
		return "accepted"
	case "correct":
		return "corrected"
	case "skip":
		return "skipped"
	}
	return action // name, dictionary
}
