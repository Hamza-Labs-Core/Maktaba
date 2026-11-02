package releaseverify

import (
	"errors"
	"time"
)

// ErrOlderBuild: a release newer in version than the one installed but signed before
// it, on the same major.minor line: an rc of a version already released, tagged after it
// (v0.3.0-rc.7 after v0.3.0), so its "newer" release is the older build. It is not a
// candidate, never a downgrade or a replay alarm, and it blocks nothing: every updater
// passes it by quietly.
var ErrOlderBuild = errors.New("release built before the installed one")

// OlderBuild is Maktaba's one "never go back" rule for a release newer in version than
// the installed one (every updater applies it: the home's choice of a target, a plain or
// layout install, a runtime, an app bundle): such a candidate is an older build when it
// was signed (published) before the installed release on the same major.minor line. A
// higher major.minor is never one: a hotfix on an older minor signed after the next minor
// (0.2.5 after 0.3.0) still moves up to it (story 22.5 EC1). An unknown time decides
// nothing.
func OlderBuild(cand string, candSigned time.Time, installed string, installedSigned time.Time) bool {
	if candSigned.IsZero() || installedSigned.IsZero() {
		return false
	}
	if CompareVersions(cand, installed) <= 0 {
		return false // not newer: the downgrade and replay rules say what it is
	}
	return candSigned.Before(installedSigned) && SameLine(cand, installed)
}
