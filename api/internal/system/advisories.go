// Security advisories for the running version (Story 23.8 AC4, TC2).
//
// Maktaba's advisories are the repository's published GitHub Security Advisories
// (SECURITY.md: every accepted report becomes a GHSA, published with its affected
// versions once fixed). The update check reads them beside the releases, from the same
// API with the same client and token, and GET /api/system/updates carries the ones whose
// affected range covers this build: `advisories`, with the release that fixes each, so a
// client can say "this version is affected; update to X". The list is fetched best
// effort: when it cannot be read, `advisories_checked` is false and a client still links
// to the full list (`advisories_url`), never saying "none".
//
// An advisory's ranges are in Maktaba's release versions (the GitHub form: "< 0.3.152",
// ">= 0.3.100, < 0.3.152", "= 0.3.140"); a range that does not parse is skipped, and a
// build whose version is not a release ("dev") matches none.
package system

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Advisory is one published security advisory that affects the running version.
type Advisory struct {
	ID       string `json:"id"`            // the GHSA id
	CVE      string `json:"cve,omitempty"` // its CVE, when one was assigned
	Severity string `json:"severity"`      // critical | high | medium | low | unknown
	Summary  string `json:"summary"`
	URL      string `json:"url"`
	// Affected is the range that matched this version, as published.
	Affected string `json:"affected"`
	// FixedIn is the release that fixes it ("" while no release does).
	FixedIn     string    `json:"fixed_in,omitempty"`
	PublishedAt time.Time `json:"published_at"`
}

// ghAdvisory is the subset of GitHub's repository security advisories API we read
// (GET /repos/{owner}/{repo}/security-advisories?state=published).
type ghAdvisory struct {
	GHSAID          string    `json:"ghsa_id"`
	CVEID           string    `json:"cve_id"`
	HTMLURL         string    `json:"html_url"`
	Summary         string    `json:"summary"`
	Severity        string    `json:"severity"`
	State           string    `json:"state"`
	PublishedAt     time.Time `json:"published_at"`
	WithdrawnAt     *string   `json:"withdrawn_at"`
	Vulnerabilities []struct {
		VulnerableVersionRange string `json:"vulnerable_version_range"`
		PatchedVersions        string `json:"patched_versions"`
	} `json:"vulnerabilities"`
}

// AdvisoriesURL is the repository's public list of advisories.
func (u *Updater) AdvisoriesURL() string {
	return "https://github.com/" + u.repo + "/security/advisories"
}

func (u *Updater) fetchAdvisories(ctx context.Context) ([]ghAdvisory, error) {
	url := u.baseURL + "/repos/" + u.repo + "/security-advisories?state=published&per_page=100&sort=published&direction=desc"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if u.token != "" {
		req.Header.Set("Authorization", "Bearer "+u.token)
	}
	resp, err := u.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch advisories: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github security advisories: HTTP %d", resp.StatusCode)
	}
	var out []ghAdvisory
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode advisories: %w", err)
	}
	return out, nil
}

// affecting returns the advisories whose affected ranges cover version, most severe
// first, then newest.
func affecting(list []ghAdvisory, version string) []Advisory {
	out := []Advisory{}
	if !validSemver(version) {
		return out
	}
	for _, a := range list {
		if a.WithdrawnAt != nil || (a.State != "" && a.State != "published") {
			continue
		}
		for _, v := range a.Vulnerabilities {
			if !inRange(version, v.VulnerableVersionRange) {
				continue
			}
			out = append(out, Advisory{
				ID:          a.GHSAID,
				CVE:         a.CVEID,
				Severity:    severityWord(a.Severity),
				Summary:     a.Summary,
				URL:         a.HTMLURL,
				Affected:    strings.TrimSpace(v.VulnerableVersionRange),
				FixedIn:     fixedAfter(v.PatchedVersions, version),
				PublishedAt: a.PublishedAt,
			})
			break // one entry per advisory
		}
	}
	rank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "unknown": 4}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		return out[i].PublishedAt.After(out[j].PublishedAt)
	})
	return out
}

func severityWord(s string) string {
	switch s = strings.ToLower(strings.TrimSpace(s)); s {
	case "critical", "high", "medium", "low":
		return s
	case "moderate":
		return "medium"
	}
	return "unknown"
}

// inRange reports whether version is in a GitHub vulnerable_version_range: comparators
// (`<`, `<=`, `>`, `>=`, `=`) joined by commas, all of which must hold. An empty or
// unreadable range matches nothing: a client is never told it is affected by a typo.
func inRange(version, rng string) bool {
	rng = strings.TrimSpace(rng)
	if rng == "" {
		return false
	}
	for _, part := range strings.Split(rng, ",") {
		part = strings.TrimSpace(part)
		op := ""
		for _, o := range []string{"<=", ">=", "<", ">", "="} {
			if strings.HasPrefix(part, o) {
				op = o
				break
			}
		}
		if op == "" {
			// a bare version means exactly that version
			op = "="
		}
		ver := strings.TrimSpace(strings.TrimPrefix(part, op))
		if !validSemver(ver) {
			return false
		}
		c := compareSemver(version, ver)
		ok := false
		switch op {
		case "<":
			ok = c < 0
		case "<=":
			ok = c <= 0
		case ">":
			ok = c > 0
		case ">=":
			ok = c >= 0
		case "=":
			ok = c == 0
		}
		if !ok {
			return false
		}
	}
	return true
}

// fixedAfter is the lowest of an advisory's patched versions ("0.3.152", or several
// comma-separated) that is newer than version: the release to update to.
func fixedAfter(patched, version string) string {
	best := ""
	for _, p := range strings.Split(patched, ",") {
		p = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(p), ">= "))
		if !validSemver(p) || compareSemver(p, version) <= 0 {
			continue
		}
		if best == "" || compareSemver(p, best) < 0 {
			best = p
		}
	}
	return best
}
