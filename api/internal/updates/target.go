package updates

import (
	"sort"
	"time"
)

// Verify states (release_catalog.verify_state).
const (
	VerifyUnverified = "unverified"
	VerifyVerified   = "verified"
	VerifyFailed     = "failed"
)

// Release is one release_catalog row.
type Release struct {
	Version         string       `json:"version"`
	Tag             string       `json:"tag"`
	Channel         string       `json:"channel"`
	PublishedAt     *time.Time   `json:"published_at,omitempty"`
	Prerelease      bool         `json:"prerelease"`
	ReleaseURL      string       `json:"release_url,omitempty"`
	Notes           string       `json:"notes,omitempty"`
	Assets          []Asset      `json:"assets,omitempty"`
	Manifest        *Manifest    `json:"manifest,omitempty"`
	ChecksumsSHA256 string       `json:"checksums_sha256,omitempty"`
	VerifyState     string       `json:"verify_state"`
	VerifyError     *VerifyError `json:"verify_error,omitempty"`
	FirstSeenAt     time.Time    `json:"first_seen_at"`
	VerifiedAt      *time.Time   `json:"verified_at,omitempty"`
	WithdrawnAt     *time.Time   `json:"withdrawn_at,omitempty"`
	BlockedReason   string       `json:"blocked_reason,omitempty"`
	HeldAt          *time.Time   `json:"held_at,omitempty"`
	ApprovedAt      *time.Time   `json:"approved_at,omitempty"`
	ApprovedBy      string       `json:"approved_by,omitempty"`
	Attempts        int          `json:"attempts"`
}

// Asset is one GitHub release asset (name, download URL, size).
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// BreaksPreviousHome is the verified release.json's contract flag.
func (r Release) BreaksPreviousHome() bool { return r.Manifest != nil && r.Manifest.BreaksPreviousHome }

// MinUpgradeFrom is the verified release.json's min_upgrade_from ("" = any).
func (r Release) MinUpgradeFrom() string {
	if r.Manifest == nil {
		return ""
	}
	return r.Manifest.MinUpgradeFrom
}

// Published is when the release was published: release.json's time, else GitHub's,
// else when the home first saw it.
func (r Release) Published() time.Time {
	if r.Manifest != nil && !r.Manifest.Published.IsZero() {
		return r.Manifest.Published
	}
	if r.PublishedAt != nil && !r.PublishedAt.IsZero() {
		return *r.PublishedAt
	}
	return r.FirstSeenAt
}

// IsPrerelease is a beta-channel release (GitHub's flag or a suffix in the version).
func (r Release) IsPrerelease() bool {
	return r.Prerelease || r.Channel == ChannelBeta || IsPrerelease(r.Version)
}

// Reasons a target is not installed (Choice.Reason, the Updates page's "why not").
const (
	ReasonUpToDate     = "up_to_date"
	ReasonNoUpgradeFor = "no_upgrade_path" // every candidate needs a newer starting version
	ReasonHeld         = "needs_approval"  // breaks the previous home release (story 35.3 AC4)
)

// Choice is ChooseTarget's answer.
type Choice struct {
	// Target is the release to install next (nil: none).
	Target *Release `json:"target,omitempty"`
	// Held: the target breaks the previous home release and waits for an admin's
	// approval (Update now); it is never installed automatically.
	Held bool `json:"held"`
	// SteppingStone: the newest candidate cannot be installed from the running version
	// (min_upgrade_from), so the target is the newest one that can, and the next window
	// continues from there.
	SteppingStone bool `json:"stepping_stone"`
	// Newest is the newest candidate whatever its min_upgrade_from (for the page).
	Newest *Release `json:"newest,omitempty"`
	Reason string   `json:"reason,omitempty"`
}

// ChooseTarget picks the release to install (design §4; pure, table-tested):
//
//	candidates = verified, not withdrawn, not blocked, channel allowed,
//	             > watermark (and > current), not skipped, <= pinned_version when set
//	target     = the highest candidate (numeric semver) whose min_upgrade_from <= current
//	held       = target.breaks_previous_home and not approved by an admin
//
// Switching beta -> stable never selects a lower version: everything at or below the
// watermark is excluded whatever the channel.
func ChooseTarget(catalog []Release, p Policy, current, watermark string) Choice {
	floor := maxVersion(NormalizeVersion(current), NormalizeVersion(watermark))
	var cands []Release
	for _, r := range catalog {
		switch {
		case r.VerifyState != VerifyVerified || r.Manifest == nil:
			continue
		case r.WithdrawnAt != nil || r.BlockedReason != "":
			continue
		case p.Channel != ChannelBeta && r.IsPrerelease():
			continue
		case floor != "" && CompareVersions(r.Version, floor) <= 0:
			continue
		case p.Skipped(r.Version):
			continue
		case p.PinnedVersion != "" && CompareVersions(r.Version, p.PinnedVersion) > 0:
			continue
		}
		cands = append(cands, r)
	}
	if len(cands) == 0 {
		return Choice{Reason: ReasonUpToDate}
	}
	sort.SliceStable(cands, func(i, j int) bool { return CompareVersions(cands[i].Version, cands[j].Version) > 0 })
	newest := cands[0]
	out := Choice{Newest: &newest}
	for i := range cands {
		r := cands[i]
		if m := r.MinUpgradeFrom(); m != "" && current != "" && CompareVersions(m, current) > 0 {
			continue
		}
		out.Target = &r
		out.SteppingStone = i > 0
		break
	}
	if out.Target == nil {
		out.Reason = ReasonNoUpgradeFor
		return out
	}
	if out.Target.BreaksPreviousHome() && out.Target.ApprovedAt == nil {
		out.Held = true
		out.Reason = ReasonHeld
	}
	return out
}
