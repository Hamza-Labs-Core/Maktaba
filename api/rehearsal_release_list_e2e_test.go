//go:build unix

package main

// Scenario 20 of the update rehearsal (docs/update-rehearsal.md): GitHub's release list at its
// real size and shape, and past its cap.
//
// From 2026-11-23 11:02Z every home's update check failed: GitHub's list of the newest 30
// releases (some 190 assets each) had grown past the 8 MiB a home read it to, and the
// failure was only logged. The home went on choosing from a catalog that no longer knew the
// newest releases (the incident of 2026-11-24). Every rehearsal scenario served a short list
// of a few small releases, so no scenario ever read a list the size of GitHub's.
//
// Here the fake GitHub serves the list as GitHub does (useRealShape: its paging, each
// release in the recorded release's shape, tests/fixtures/github-releases), every release
// twice the recorded size (AssetGrowth in api/internal/updates). The previous release asks
// for 30 releases a page under 8 MiB, which GitHub's real list is over (the incident
// itself, until a release with the fix is the previous one), so this commit is installed
// by hand and the list is rehearsed on it:
//
//	(a) the home's check finds the release after this commit in a list of real size, pages
//	    twice the recorded page, and the update to it completes;
//	(b) the list then grows past the cap a home reads a page to: two checks fail, the
//	    Updates page's overview says so (too_large, two failures, since when), and the
//	    System health page's "Update check" item turns degraded; one check of a list back
//	    to its size clears it.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// listGrowth is how much larger than the recorded releases the rehearsal's are: the tests'
// AssetGrowth (api/internal/updates/catalog_list_test.go), the worst the caps allow for.
const listGrowth = 2.0

// overCap is a page of the list longer than the 16 MiB a home reads it to
// (api/internal/updates: listPageLimit).
const overCap = 17 << 20

// checkOverview is the update check's part of GET /api/cluster/updates.
type checkOverview struct {
	LastCheckOK       *time.Time `json:"last_check_ok"`
	LastCheckError    string     `json:"last_check_error"`
	LastCheckCode     string     `json:"last_check_code"`
	CheckFailures     int        `json:"check_failures"`
	CheckFailingSince *time.Time `json:"check_failing_since"`
}

func (a *ownerSession) checkOverview() checkOverview {
	t := a.m.r.t
	t.Helper()
	code, body := a.get("/api/cluster/updates")
	if code != http.StatusOK {
		t.Fatalf("GET /api/cluster/updates: %d %s", code, tail(string(body), 2000))
	}
	var ov checkOverview
	if err := json.Unmarshal(body, &ov); err != nil {
		t.Fatalf("GET /api/cluster/updates: %v", err)
	}
	return ov
}

// statusItem is one item of GET /api/admin/system/status (nil: not listed).
func (a *ownerSession) statusItem(id string) map[string]any {
	code, body := a.get("/api/admin/system/status")
	if code != http.StatusOK {
		return nil
	}
	var rep struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(body, &rep)
	for _, it := range rep.Items {
		if it["id"] == id {
			return it
		}
	}
	return nil
}

// checkNow is the Updates page's "Check now": the answer's status.
func (a *ownerSession) checkNow() int {
	resp, body := a.b.do("POST", a.m.webURL()+"/api/cluster/updates/check", map[string]any{}, a.csrf)
	if resp.StatusCode == http.StatusUnauthorized {
		a.login()
		resp, body = a.b.do("POST", a.m.webURL()+"/api/cluster/updates/check", map[string]any{}, a.csrf)
	}
	a.m.r.logf("check now: %d %s", resp.StatusCode, tail(string(body), 400))
	return resp.StatusCode
}

func TestUpdateRehearsal20RealSizeReleaseList(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	home, _ := r.homeOnPrev(profile("small"))
	home.installByHand(k.curSpec())
	home.waitServing(k.cur, 10*time.Minute)

	// (a) a list of real shape, twice the recorded size: the next release is found in it
	// and installed
	r.gh.useRealShape(t, listGrowth, 3)
	recorded := realListPageBytes(t)
	n1, b1 := additiveMigration(k.rev + 1)
	next := payloadSpec{variant: "next", version: k.next, extra: map[string]string{n1: b1}}
	run, mon, _ := r.update(home, next, 30*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s, found in a list of real size, ended %s: %s (steps %+v)", k.next, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.next, 5*time.Minute)
	r.budget(mon, "an update found in a release list of real size (twice the recorded)", 2*time.Minute)
	largest := r.gh.largestPage()
	r.logf("the largest page of the list served: %d bytes (the recorded page: %d)", largest, recorded)
	if float64(largest) < 0.9*listGrowth*float64(recorded) {
		t.Errorf("the largest page served was %d bytes: the list was not served at %.0fx the recorded %d",
			largest, listGrowth, recorded)
	}

	// (b) the list past its cap: not log-only
	a := home.signIn()
	r.gh.setPad(overCap)
	for i := range 2 {
		if code := a.checkNow(); code == http.StatusOK {
			t.Fatalf("check %d of a list over its cap answered 200", i+1)
		}
	}
	ov := a.checkOverview()
	if ov.LastCheckCode != "too_large" || ov.CheckFailures < 2 || ov.CheckFailingSince == nil || ov.LastCheckError == "" {
		t.Errorf("after two failed checks the Updates page reads %+v; want too_large, two failures, since when, the error", ov)
	}
	var item map[string]any
	waitFor(t, "the System health page's update check item to turn degraded", 3*time.Minute, func() bool {
		item = a.statusItem("update_check")
		return item != nil && item["status"] == "degraded" && item["code"] == "update_check_failing"
	})
	r.logf("the update check item: %v", item)

	// a list back to its size clears it
	r.gh.setPad(0)
	if code := a.checkNow(); code != http.StatusOK {
		t.Fatalf("a check of a list back to its size answered %d", code)
	}
	if ov = a.checkOverview(); ov.CheckFailures != 0 || ov.LastCheckCode != "" || ov.CheckFailingSince != nil || ov.LastCheckError != "" {
		t.Errorf("after a check that worked the Updates page reads %+v; want no failures", ov)
	}
	waitFor(t, "the update check item to leave degraded", 3*time.Minute, func() bool {
		it := a.statusItem("update_check")
		return it == nil || it["status"] != "degraded"
	})
}
