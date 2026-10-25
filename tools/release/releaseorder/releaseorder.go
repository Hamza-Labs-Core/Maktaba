// Package releaseorder decides a tag's place among the published releases, for the
// release workflows: whether the tag may be released at all (the tag-order guard),
// which release is its "previous" (release.json's breaks_previous_home and
// min_upgrade_from), and whether it becomes GitHub's latest.
//
// v0.3.0-rc.7 was tagged after v0.3.0 was published: no updater ever offered it (every
// one orders 0.3.0 above it) and a machine on it was offered the older 0.3.0. A tag is
// released only above every published release; a deliberate maintenance line (a hotfix
// of an older minor, story 22.5 EC1) is named explicitly and never becomes the latest.
package releaseorder

import (
	"fmt"
	"strings"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// versions are the valid tags of published, their v dropped, the tag itself left out.
func others(tag string, published []string) []string {
	var out []string
	for _, p := range published {
		p = strings.TrimSpace(p)
		if p == "" || p == tag || !rv.ValidTag(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func ver(tag string) string { return strings.TrimPrefix(tag, "v") }

// Guard reports whether tag may be released given the published releases' tags (drafts
// excluded). A tag already published is a re-run of its own release: allowed. Otherwise
// it must be above every published release (a pre-release of a version already out, or
// a version at or below the newest, is refused), unless allow names it: a deliberate
// maintenance release, which reports maintenance (it never becomes the latest).
func Guard(tag string, published []string, allow string) (maintenance bool, err error) {
	if !rv.ValidTag(tag) {
		return false, fmt.Errorf("%q is not a release tag (vX.Y.Z or vX.Y.Z-pre)", tag)
	}
	for _, p := range published {
		if strings.TrimSpace(p) == tag {
			return false, nil // its own release, run again
		}
	}
	var newest string
	for _, p := range others(tag, published) {
		if newest == "" || rv.CompareVersions(ver(p), ver(newest)) > 0 {
			newest = p
		}
	}
	if newest == "" || rv.CompareVersions(ver(tag), ver(newest)) > 0 {
		return false, nil
	}
	if strings.TrimSpace(allow) == tag {
		return true, nil
	}
	why := "is not above"
	if rv.IsPrerelease(ver(tag)) && rv.CompareVersions(ver(tag), ver(newest)) < 0 {
		why = "is a pre-release below"
	}
	return false, fmt.Errorf("%s %s the newest published release %s: no updater would offer it, and a machine on it "+
		"would be offered %s. Tag a version above %s, or, for a deliberate maintenance release of an older line, "+
		"set the repository variable MAKTABA_RELEASE_ALLOW_TAG=%s (it never becomes the latest)", tag, why, newest, newest,
		newest, tag)
}

// Place is a tag's place among the published releases, as the guard reports it.
type Place struct {
	// Maintenance: a deliberate maintenance release (MAKTABA_RELEASE_ALLOW_TAG).
	Maintenance bool
	// Published: the tag is a published release already (this run is a re-run of it: its
	// signed files are not built again, only a draft is).
	Published bool
	// Newest is the newest published release other than the tag ("" when there is none):
	// its commit must be in the tag's history (the guard checks it with git).
	Newest string
	// Latest: the tag would be GitHub's latest (the highest stable version): its images'
	// :latest moves and the relay deploys only then.
	Latest bool
}

// Locate is Guard's answer with the rest of the tag's place.
func Locate(tag string, published []string, allow string) (Place, error) {
	maint, err := Guard(tag, published, allow)
	if err != nil {
		return Place{}, err
	}
	p := Place{Maintenance: maint, Latest: !maint && Latest(tag, published)}
	for _, t := range published {
		if strings.TrimSpace(t) == tag {
			p.Published = true
		}
	}
	for _, t := range others(tag, published) {
		if p.Newest == "" || rv.CompareVersions(ver(t), ver(p.Newest)) > 0 {
			p.Newest = t
		}
	}
	return p, nil
}

// Previous is the release tag's release.json builds on: the highest published version
// below it; for a stable tag, the highest stable one (a release candidate of the same
// version is not what a stable home comes from). "" when there is none.
func Previous(tag string, published []string) string {
	stable := !rv.IsPrerelease(ver(tag))
	var best string
	for _, p := range others(tag, published) {
		if rv.CompareVersions(ver(p), ver(tag)) >= 0 || (stable && rv.IsPrerelease(ver(p))) {
			continue
		}
		if best == "" || rv.CompareVersions(ver(p), ver(best)) > 0 {
			best = p
		}
	}
	return best
}

// Latest reports whether tag becomes GitHub's latest release: a stable version above
// every other published stable release (a pre-release, or a hotfix of an older line,
// never is).
func Latest(tag string, published []string) bool {
	if !rv.ValidTag(tag) || rv.IsPrerelease(ver(tag)) {
		return false
	}
	for _, p := range others(tag, published) {
		if !rv.IsPrerelease(ver(p)) && rv.CompareVersions(ver(p), ver(tag)) >= 0 {
			return false
		}
	}
	return true
}
