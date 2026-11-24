package updates

// The release feed's check (cmd/release-feed-check, the release-feed workflow hourly and
// every release workflow after it publishes): GitHub's feed read exactly as a home reads it,
// through the same code (listPage, candidate, the tag read), and held to FeedHeadroom of
// each cap. Since 2026-11-23 every home's check had failed on a list longer than its cap,
// and nothing outside a home read the feed the way a home does (the incident of 2026-11-24).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// FeedRead is one answer the probe read: its URL, length and cap.
type FeedRead struct {
	URL      string `json:"url"`
	Bytes    int64  `json:"bytes"`
	Cap      int64  `json:"cap"`
	Releases int    `json:"releases,omitempty"`
	// Err is why the answer could not be read ("" when it was).
	Err string `json:"error,omitempty"`
	// Code is Err's CheckFail code.
	Code string `json:"code,omitempty"`
}

// Share is the part of its cap the answer used.
func (r FeedRead) Share() float64 {
	if r.Cap <= 0 {
		return 0
	}
	return float64(r.Bytes) / float64(r.Cap)
}

// FeedReport is what ProbeFeed found.
type FeedReport struct {
	Repo string `json:"repo"`
	// Pages are the list's pages as a home reads them; Tag is the release-by-tag read of
	// Target (a home's Recheck before a run).
	Pages []FeedRead `json:"pages"`
	Tag   *FeedRead  `json:"tag,omitempty"`
	// Latest is GitHub's latest release (/releases/latest: the newest published stable
	// one); Target is the release the probe looks for in the home's list (Expect, else
	// Latest).
	Latest string `json:"latest,omitempty"`
	Target string `json:"target,omitempty"`
	// Selectable is the versions the home's catalog keeps from what it read (stable
	// channel; beta when Target is a prerelease), newest first; Newest the highest.
	Selectable []string `json:"selectable"`
	Newest     string   `json:"newest,omitempty"`
	// Drafts counts the drafts in the list: none unless the probe's token can push.
	Drafts int `json:"drafts,omitempty"`
	// RateLimited: GitHub refused a read for its rate limit (the answer is unknown, not bad).
	RateLimited bool `json:"rate_limited,omitempty"`
	// Problems are why a home could not read this feed, or soon could not; empty: it can.
	Problems []string `json:"problems"`
}

// OK reports whether a home reads the feed with room to spare.
func (r FeedReport) OK() bool { return len(r.Problems) == 0 }

func (r *FeedReport) problem(format string, args ...any) {
	r.Problems = append(r.Problems, fmt.Sprintf(format, args...))
}

// ProbeFeed reads the release feed as a home's check does and reports each answer's size
// against its cap, and whether the home's catalog can select expect (a tag; "" for GitHub's
// latest release). A problem is an answer over FeedHeadroom of its cap or not read, a target
// the catalog cannot select, or one GitHub does not list.
func (d *Discoverer) ProbeFeed(ctx context.Context, expect string) FeedReport {
	rep := FeedReport{Repo: d.repo(), Selectable: []string{}, Problems: []string{}}
	note := func(read *FeedRead, err error) {
		if err == nil {
			return
		}
		read.Err, read.Code = err.Error(), CheckErrorCode(err)
		if errors.Is(err, ErrRateLimited) {
			rep.RateLimited = true
		}
	}

	// GitHub's newest published stable release, by GitHub's own reckoning
	latestURL := d.base() + "/repos/" + d.repo() + "/releases/latest"
	var latest FeedRead
	latest.URL, latest.Cap = latestURL, tagReadLimit
	body, code, err := d.get(ctx, latestURL, true, tagReadLimit)
	switch {
	case code == http.StatusNotFound:
		// no stable release yet
	case err != nil:
		note(&latest, err)
		rep.problem("GitHub's latest release could not be read: %s", latest.Err)
	default:
		var g ghRelease
		if err := json.Unmarshal(body, &g); err != nil {
			rep.problem("GitHub's latest release is not a release: %v", err)
		} else {
			rep.Latest = g.TagName
		}
	}
	rep.Target = strings.TrimSpace(expect)
	if rep.Target == "" {
		rep.Target = rep.Latest
	}
	channel := ChannelStable
	if rep.Target != "" && IsPrerelease(rep.Target) {
		channel = ChannelBeta
	}

	// the list, page by page, as list reads it
	var rels []ghRelease
	for page := 1; page <= listPages; page++ {
		read := FeedRead{URL: fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", d.base(), d.repo(), listPerPage, page),
			Cap: listPageLimit}
		part, n, decodeErr, err := d.listPage(ctx, page)
		read.Bytes, read.Releases = n, len(part)
		note(&read, err)
		if err == nil && decodeErr != nil {
			read.Err, read.Code = "decode releases: "+decodeErr.Error(), CheckFailDecode
		}
		if errors.Is(err, errTooLarge) {
			read.Bytes = listPageLimit + 1 // at least
		}
		rep.Pages = append(rep.Pages, read)
		if read.Err != "" {
			rep.problem("page %d of the release list cannot be read as a home reads it: %s", page, read.Err)
			break
		}
		if read.Share() > FeedHeadroom {
			rep.problem("page %d of the release list is %d bytes, %.0f%% of the %d a home reads (over %.0f%%)",
				page, n, 100*read.Share(), int64(listPageLimit), 100*FeedHeadroom)
		}
		rels = append(rels, part...)
		if len(part) < listPerPage {
			break
		}
	}

	// what the home's catalog keeps of it
	var target *Release
	var targetRaw *ghRelease
	for i, g := range rels {
		if g.Draft {
			rep.Drafts++
		}
		if g.TagName == rep.Target {
			targetRaw = &rels[i]
		}
		r, ok := d.candidate(g, channel, "")
		if !ok {
			continue
		}
		rep.Selectable = append(rep.Selectable, r.Version)
		if rep.Newest == "" || CompareVersions(r.Version, rep.Newest) > 0 {
			rep.Newest = r.Version
		}
		if g.TagName == rep.Target {
			rr := r
			target = &rr
		}
	}
	sortVersionsDesc(rep.Selectable)
	listRead := len(rep.Pages) > 0 && rep.Pages[len(rep.Pages)-1].Err == ""
	switch {
	case rep.Target == "":
		// nothing published to look for
	case !listRead:
		// the list's own problem is already said
	case targetRaw == nil:
		rep.problem("%s is not in the %d releases a home reads (%d pages of %d): the home cannot see it",
			rep.Target, len(rels), listPages, listPerPage)
	case target == nil:
		why := "not a version"
		switch {
		case targetRaw.Draft:
			why = "a draft"
		case channel == ChannelStable && (targetRaw.Prerelease || IsPrerelease(targetRaw.TagName)):
			why = "a prerelease"
		}
		rep.problem("%s is in the list, but the home's catalog passes over it: %s", rep.Target, why)
	case !hasSignedFiles(*target):
		rep.problem("%s is in the list, but without its signed files (%s, %s, %s) from %s's own releases: the home cannot verify it",
			rep.Target, ChecksumsFile, BundleFile, ManifestFile, d.repo())
	}

	// the release-by-tag read of the target (a home's Recheck just before a run)
	if rep.Target != "" {
		tagURL := d.base() + "/repos/" + d.repo() + "/releases/tags/" + rep.Target
		tag := FeedRead{URL: tagURL, Cap: tagReadLimit}
		cr := int64(0)
		_, err := d.read(ctx, tagURL, true, tagReadLimit, func(r io.Reader) error {
			n, err := io.Copy(io.Discard, r)
			cr = n
			return err
		})
		tag.Bytes = cr
		note(&tag, err)
		if errors.Is(err, errTooLarge) {
			tag.Bytes = tagReadLimit + 1
		}
		rep.Tag = &tag
		switch {
		case tag.Err != "":
			rep.problem("the release %s cannot be read by its tag as a home reads it: %s", rep.Target, tag.Err)
		case tag.Share() > FeedHeadroom:
			rep.problem("the release %s is %d bytes read by its tag, %.0f%% of the %d a home reads (over %.0f%%)",
				rep.Target, tag.Bytes, 100*tag.Share(), int64(tagReadLimit), 100*FeedHeadroom)
		}
	}
	return rep
}

func sortVersionsDesc(vs []string) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && CompareVersions(vs[j], vs[j-1]) > 0; j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}

// FeedCaps are the caps a home holds GitHub's answers to (the tests and the feed check
// print them).
func FeedCaps() (listPage, tagRead, smallFile int64, perPage, pages int) {
	return listPageLimit, tagReadLimit, smallFileLimit, listPerPage, listPages
}
