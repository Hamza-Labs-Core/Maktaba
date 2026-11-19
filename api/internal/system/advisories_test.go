package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Story 23.8 AC4 / TC2: the update status carries the published advisories that affect
// the running version, with the release that fixes each; an outdated build is told to
// update, a fixed one sees none, and an unreadable list is never "none".

const advisoriesJSON = `[
  {"ghsa_id": "GHSA-aaaa-aaaa-aaaa", "cve_id": "CVE-2026-1001", "html_url": "https://github.com/o/r/security/advisories/GHSA-aaaa-aaaa-aaaa",
   "summary": "Path traversal in subtitle ingest", "severity": "high", "state": "published", "published_at": "2026-11-01T10:00:00Z",
   "vulnerabilities": [{"package": {"ecosystem": "other", "name": "maktaba"}, "vulnerable_version_range": ">= 0.3.100, < 0.3.152", "patched_versions": "0.3.152"}]},
  {"ghsa_id": "GHSA-bbbb-bbbb-bbbb", "cve_id": null, "html_url": "https://github.com/o/r/security/advisories/GHSA-bbbb-bbbb-bbbb",
   "summary": "Session fixation", "severity": "critical", "state": "published", "published_at": "2026-10-01T10:00:00Z",
   "vulnerabilities": [{"vulnerable_version_range": "<= 0.3.150", "patched_versions": "0.3.151"}]},
  {"ghsa_id": "GHSA-cccc-cccc-cccc", "html_url": "u", "summary": "Old one", "severity": "moderate", "state": "published",
   "published_at": "2026-01-01T10:00:00Z",
   "vulnerabilities": [{"vulnerable_version_range": "< 0.2.0", "patched_versions": "0.2.0"}]},
  {"ghsa_id": "GHSA-dddd-dddd-dddd", "html_url": "u", "summary": "Withdrawn", "severity": "high", "state": "published",
   "withdrawn_at": "2026-11-02T00:00:00Z", "published_at": "2026-11-01T00:00:00Z",
   "vulnerabilities": [{"vulnerable_version_range": "< 9.0.0", "patched_versions": "9.0.0"}]}
]`

func advisoryStub(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/security-advisories") {
			if r.URL.Query().Get("state") != "published" {
				t.Errorf("advisories read without state=published: %s", r.URL.RawQuery)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(advisoriesJSON))
			return
		}
		_ = json.NewEncoder(w).Encode([]ghRelease{{TagName: "v0.3.152", HTMLURL: "rel"}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStatusCarriesTheAdvisoriesThatAffectThisVersion(t *testing.T) {
	srv := advisoryStub(t, http.StatusOK)
	u := NewUpdater(UpdaterConfig{Repo: "o/r", CurrentVersion: "v0.3.140", APIBaseURL: srv.URL})
	s := u.Status(context.Background(), true)
	if !s.AdvisoriesChecked {
		t.Fatalf("advisories not checked: %+v", s)
	}
	if s.AdvisoriesURL != "https://github.com/o/r/security/advisories" {
		t.Fatalf("advisories_url = %q", s.AdvisoriesURL)
	}
	// critical first; the 0.2 one and the withdrawn one do not apply
	if len(s.Advisories) != 2 || s.Advisories[0].ID != "GHSA-bbbb-bbbb-bbbb" || s.Advisories[1].ID != "GHSA-aaaa-aaaa-aaaa" {
		t.Fatalf("advisories = %+v", s.Advisories)
	}
	a := s.Advisories[1]
	if a.CVE != "CVE-2026-1001" || a.Severity != "high" || a.FixedIn != "0.3.152" || a.Affected != ">= 0.3.100, < 0.3.152" ||
		!strings.HasSuffix(a.URL, "GHSA-aaaa-aaaa-aaaa") || a.Summary == "" {
		t.Fatalf("advisory = %+v", a)
	}
	// 0.3.151 fixes B but A still covers it: 0.3.152 fixes both
	if s.AdvisoriesFixedIn != "0.3.152" {
		t.Fatalf("advisories_fixed_in = %q, want 0.3.152", s.AdvisoriesFixedIn)
	}
	// the update that fixes it is there too: the client's upgrade prompt
	if !s.Available || s.LatestVersion != "v0.3.152" {
		t.Fatalf("update = %+v", s)
	}
}

func TestAFixedVersionHasNoAdvisories(t *testing.T) {
	srv := advisoryStub(t, http.StatusOK)
	u := NewUpdater(UpdaterConfig{Repo: "o/r", CurrentVersion: "v0.3.152", APIBaseURL: srv.URL})
	s := u.Status(context.Background(), true)
	if !s.AdvisoriesChecked || len(s.Advisories) != 0 {
		t.Fatalf("a fixed version: checked=%v advisories=%+v", s.AdvisoriesChecked, s.Advisories)
	}
	// "none" is an empty list, not null, in the JSON
	rec := httptest.NewRecorder()
	u.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system/updates", nil))
	if !strings.Contains(rec.Body.String(), `"advisories":[]`) || !strings.Contains(rec.Body.String(), `"advisories_checked":true`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestAnUnreadableAdvisoryListIsNotNone(t *testing.T) {
	srv := advisoryStub(t, http.StatusForbidden)
	u := NewUpdater(UpdaterConfig{Repo: "o/r", CurrentVersion: "v0.3.140", APIBaseURL: srv.URL})
	s := u.Status(context.Background(), true)
	if s.AdvisoriesChecked || len(s.Advisories) != 0 || s.AdvisoriesURL == "" {
		t.Fatalf("unreadable list: %+v", s)
	}
	// the release check itself still answered
	if s.LatestVersion != "v0.3.152" {
		t.Fatalf("the update check failed with the advisories: %+v", s)
	}
}

func TestADisabledCheckReadsNoAdvisoriesButLinksThem(t *testing.T) {
	u := NewUpdater(UpdaterConfig{Repo: "o/r", CurrentVersion: "v0.3.140", Disabled: true})
	s := u.Status(context.Background(), true)
	if s.AdvisoriesChecked || s.AdvisoriesURL != "https://github.com/o/r/security/advisories" || s.Advisories == nil {
		t.Fatalf("disabled: %+v", s)
	}
}

func TestInRange(t *testing.T) {
	cases := []struct {
		v, rng string
		want   bool
	}{
		{"v0.3.140", "< 0.3.152", true},
		{"0.3.152", "< 0.3.152", false},
		{"0.3.152", "<= 0.3.152", true},
		{"0.3.99", ">= 0.3.100, < 0.3.152", false},
		{"0.3.100", ">= 0.3.100, < 0.3.152", true},
		{"0.3.140", "= 0.3.140", true},
		{"0.3.140", "0.3.140", true},
		{"0.3.141", "= 0.3.140", false},
		{"0.3.141", "> 0.3.140", true},
		{"dev", "< 9.0.0", false},
		{"0.3.140", "", false},
		{"0.3.140", "< banana", false},
		// a checkout's build after its tag is still that release's line
		{"v0.3.140-12-gabc1234", "< 0.3.141", true},
	}
	for _, c := range cases {
		v := c.v
		got := validSemver(v) && inRange(v, c.rng)
		if got != c.want {
			t.Errorf("inRange(%q, %q) = %v, want %v", c.v, c.rng, got, c.want)
		}
	}
}

func TestFixedAfter(t *testing.T) {
	if got := fixedAfter("0.3.150, 0.3.152", "0.3.151"); got != "0.3.152" {
		t.Fatalf("fixedAfter = %q", got)
	}
	if got := fixedAfter(">= 0.3.152", "0.3.140"); got != "0.3.152" {
		t.Fatalf("fixedAfter(>=) = %q", got)
	}
	if got := fixedAfter("", "0.3.140"); got != "" {
		t.Fatalf("no fix yet = %q", got)
	}
}

func TestSeverityWords(t *testing.T) {
	for in, want := range map[string]string{"CRITICAL": "critical", "moderate": "medium", "low": "low", "": "unknown", "weird": "unknown"} {
		if got := severityWord(in); got != want {
			t.Errorf("severityWord(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTheAdvisoriesAreReadPageByPage: an advisory past the first page (100 a page) is
// found; a list that never ends is an error (unknown), never "none".
func TestTheAdvisoriesAreReadPageByPage(t *testing.T) {
	var srvURL string
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/security-advisories") {
			_ = json.NewEncoder(w).Encode([]ghRelease{{TagName: "v0.3.152", HTMLURL: "rel"}})
			return
		}
		pages++
		switch r.URL.Query().Get("page") {
		case "":
			w.Header().Set("Link", `<`+srvURL+`/repos/o/r/security-advisories?page=2>; rel="next", <`+srvURL+`/repos/o/r/security-advisories?page=2>; rel="last"`)
			_, _ = w.Write([]byte(`[{"ghsa_id": "GHSA-new", "summary": "n", "severity": "low", "state": "published",
				"vulnerabilities": [{"vulnerable_version_range": ">= 9.0.0", "patched_versions": "9.1.0"}]}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"ghsa_id": "GHSA-old", "summary": "o", "severity": "high", "state": "published",
				"vulnerabilities": [{"vulnerable_version_range": "< 0.3.150", "patched_versions": "0.3.150"}]}]`))
		}
	}))
	t.Cleanup(srv.Close)
	srvURL = srv.URL
	u := NewUpdater(UpdaterConfig{Repo: "o/r", CurrentVersion: "v0.3.140", APIBaseURL: srv.URL})
	s := u.Status(context.Background(), true)
	if !s.AdvisoriesChecked || len(s.Advisories) != 1 || s.Advisories[0].ID != "GHSA-old" || pages != 2 {
		t.Fatalf("pages %d, advisories %+v (checked %v)", pages, s.Advisories, s.AdvisoriesChecked)
	}

	endless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/security-advisories") {
			_ = json.NewEncoder(w).Encode([]ghRelease{{TagName: "v0.3.152", HTMLURL: "rel"}})
			return
		}
		w.Header().Set("Link", `<http://`+r.Host+r.URL.Path+`?page=x>; rel="next"`)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(endless.Close)
	u = NewUpdater(UpdaterConfig{Repo: "o/r", CurrentVersion: "v0.3.140", APIBaseURL: endless.URL})
	if s := u.Status(context.Background(), true); s.AdvisoriesChecked || !s.Available {
		t.Fatalf("an endless list: checked %v (want false), update still answered %v", s.AdvisoriesChecked, s.Available)
	}
}

func TestNextLink(t *testing.T) {
	for h, want := range map[string]string{
		``: "",
		`<https://api/x?page=2>; rel="next", <https://api/x?page=5>; rel="last"`: "https://api/x?page=2",
		`<https://api/x?page=1>; rel="prev", <https://api/x?page=3>; rel="next"`: "https://api/x?page=3",
		`<https://api/x?page=1>; rel="first"`:                                    "",
	} {
		if got := nextLink(h); got != want {
			t.Errorf("nextLink(%q) = %q, want %q", h, got, want)
		}
	}
}

// TestFixedForAll: the release to update to must fix every advisory, on every branch its
// ranges name, and a prerelease is below its release.
func TestFixedForAll(t *testing.T) {
	// adv is one advisory of (range, patched) pairs, one per branch
	adv := func(pairs ...string) ghAdvisory {
		a := ghAdvisory{State: "published"}
		for i := 0; i+1 < len(pairs); i += 2 {
			a.Vulnerabilities = append(a.Vulnerabilities, struct {
				VulnerableVersionRange string `json:"vulnerable_version_range"`
				PatchedVersions        string `json:"patched_versions"`
			}{pairs[i], pairs[i+1]})
		}
		return a
	}
	cases := []struct {
		name    string
		list    []ghAdvisory
		version string
		want    string
	}{
		{"one fix", []ghAdvisory{adv("< 1.0.1", "1.0.1")}, "1.0.0", "1.0.1"},
		// A: 1.0.x fixed in 1.0.1, 2.0.0 still affected (fixed in 2.0.1); B fixed in 2.0.0
		{"branches", []ghAdvisory{adv("< 1.0.1", "1.0.1", ">= 2.0.0, < 2.0.1", "2.0.1"), adv("< 2.0.0", "2.0.0")}, "1.0.0", "2.0.1"},
		{"no fix yet", []ghAdvisory{adv("< 1.0.1", "1.0.1"), adv("<= 9.9.9", "")}, "1.0.0", ""},
		{"a prerelease is below its release", []ghAdvisory{adv("< 1.6.0-rc.1", "1.6.0-rc.1"), adv("< 1.6.0", "1.6.0")}, "1.5.0", "1.6.0"},
		{"none affect", []ghAdvisory{adv("< 1.0.0", "1.0.0")}, "1.2.0", ""},
		{"not a release", []ghAdvisory{adv("< 1.0.0", "1.0.0")}, "dev", ""},
	}
	for _, c := range cases {
		if got := fixedForAll(c.list, c.version); got != c.want {
			t.Errorf("%s: fixedForAll = %q, want %q", c.name, got, c.want)
		}
	}
}
