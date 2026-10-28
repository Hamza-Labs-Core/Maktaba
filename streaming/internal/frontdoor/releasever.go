package frontdoor

import (
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

// A release records its streaming software's version as "v0.3.40" (a binary change: the
// binary's own spelling) or "0.3.40" (a release the pipeline builds for a schedule or
// settings change: the pipeline package's), and "0.0.1" when the pipeline runs from a
// source checkout (its package's placeholder, which names no binary). The supervisor
// finds a release's binary by the bare version (its bareVersion, the shared
// layout.NormalizeVersion, which this module does not import). TV change #51
// (2026-10-28) compared the spellings as text and failed slot_not_healthy three times.

// sourceCheckoutVersion is the version a pipeline run from a source checkout records.
const sourceCheckoutVersion = "0.0.1"

// bareVersion is a version without its "v" (layout.NormalizeVersion).
func bareVersion(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && v[1] >= '0' && v[1] <= '9' {
		return v[1:]
	}
	return v
}

// namesNoBinary reports a release version that names no streaming binary: none, or a
// source checkout's placeholder.
func namesNoBinary(v string) bool {
	b := bareVersion(v)
	return b == "" || b == sourceCheckoutVersion
}

// releaseBinary is the streaming binary (version, sha256) a slot bound to a release
// recording version rv and checksum rs runs: none named ("", "") when the release names
// none, so the supervisor runs what an unbound slot runs (the binary the live TV is held
// on while it is held, else its own: a release naming no binary never moves a held TV);
// this binary's own (no checksum) when the release names this very version however it
// spells it; else the release's.
func releaseBinary(rv, rs string) (string, string) {
	switch {
	case namesNoBinary(rv):
		return "", ""
	case bareVersion(rv) == bareVersion(version.Version):
		return version.Version, ""
	}
	return rv, rs
}
