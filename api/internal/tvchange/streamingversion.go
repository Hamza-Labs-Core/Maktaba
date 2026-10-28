package tvchange

import (
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// A release records its streaming software's version in two spellings: a binary change
// (the updater's TV change) the binary's own, "v0.3.40", and a release the pipeline builds
// for a schedule or settings change the pipeline package's, "0.3.40". A slot reports
// the binary's own. TV change #51 (2026-10-28) compared them as text: its staging slot
// ran the right binary and failed slot_not_healthy three times. Versions compare as the
// release directories are named (layout.NormalizeVersion: the supervisor's bareVersion).

// sourceCheckoutVersion is what a pipeline run from a source checkout records: its
// package's placeholder version, not any streaming binary's (the owner's live release
// said it from 2026-10-22). It names no binary, so it is held to none, like a release
// that records no version at all.
const sourceCheckoutVersion = "0.0.1"

// streamingUnversioned reports a release version that names no streaming binary.
func streamingUnversioned(v string) bool {
	n := layout.NormalizeVersion(v)
	return n == "" || n == sourceCheckoutVersion
}

// sameVersion reports two spellings of one version ("v0.3.40" and "0.3.40").
func sameVersion(a, b string) bool {
	return layout.NormalizeVersion(a) == layout.NormalizeVersion(b)
}

// runsReleaseVersion reports whether a slot reporting running runs the streaming
// software a release records as release (any, when the release names none).
func runsReleaseVersion(release, running string) bool {
	return streamingUnversioned(release) || (strings.TrimSpace(running) != "" && sameVersion(release, running))
}
