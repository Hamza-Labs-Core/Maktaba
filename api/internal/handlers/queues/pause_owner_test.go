package queues

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

func withPausedBy(by string) stageOpt {
	return func(r []driver.Value) { r[13] = true; r[14] = t0; r[15] = by }
}

// pausedRules answers GET /api/queues with probe's row as given.
func pausedRules(row []driver.Value, more ...*queuestest.Rule) []*queuestest.Rule {
	// probe's row before the snapshot's rules (the first rule that matches wins)
	rules := append([]*queuestest.Rule{stagesRule(row)}, snapshotRules()...)
	return append(rules, more...)
}

// probeInfo is probe's stage in GET /api/queues, and the response body.
func probeInfo(t *testing.T, f *fixture) (StageInfo, string) {
	t.Helper()
	rec := f.do("GET", "/api/queues", "", admin())
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var snap struct {
		Stages []StageInfo `json:"stages"`
	}
	decode(t, rec, &snap)
	for _, s := range snap.Stages {
		if s.Stage == "probe" {
			return s, rec.Body.String()
		}
	}
	t.Fatalf("no probe in %s", rec.Body.String())
	return StageInfo{}, ""
}

// TestStagePauseOwner: a paused stage says who paused it. An admin's pause
// (paused_by "user:<id>", what PauseStage writes) carries the admin's username;
// a pause by the system (the share-path conversion) carries its paused_by only.
// On 2026-10-22 a stage paused by nobody showed only "Paused" and was never resumed.
func TestStagePauseOwner(t *testing.T) {
	users := &queuestest.Rule{Match: "select username from users where id::text = $1",
		Cols: []string{"username"}, Rows: [][]driver.Value{{"mahmoud"}}}

	t.Run("an admin's pause names the admin", func(t *testing.T) {
		f := newFixture(pausedRules(stageRow("probe", "episodes", 1, "nas_read", withPausedBy("user:"+uidA)), users)...)
		info, body := probeInfo(t, f)
		if !info.Paused || info.PausedAt == nil || info.PausedBy == nil || *info.PausedBy != "user:"+uidA {
			t.Fatalf("info = %+v", info)
		}
		if info.PausedByName == nil || *info.PausedByName != "mahmoud" {
			t.Fatalf("paused_by_name = %v", info.PausedByName)
		}
		if !strings.Contains(body, `"paused_by_name":"mahmoud"`) {
			t.Errorf("body = %s", body)
		}
		var asked []driver.Value
		for _, c := range f.db.Calls() {
			if strings.Contains(c.SQL, "from users") {
				asked = c.Args
			}
		}
		if len(asked) != 1 || asked[0] != uidA {
			t.Errorf("user lookup args = %v", asked)
		}
	})

	t.Run("a system pause has no user to look up", func(t *testing.T) {
		f := newFixture(pausedRules(stageRow("probe", "episodes", 1, "nas_read", withPausedBy("storage_canonicalize")), users)...)
		info, _ := probeInfo(t, f)
		if !info.Paused || info.PausedByName != nil || info.PausedBy == nil || *info.PausedBy != "storage_canonicalize" {
			t.Fatalf("info = %+v", info)
		}
		for _, c := range f.db.Calls() {
			if strings.Contains(c.SQL, "from users") {
				t.Errorf("looked up a user for a system pause: %s", c.SQL)
			}
		}
	})

	t.Run("a stage that is not paused has no owner", func(t *testing.T) {
		f := newFixture(pausedRules(stageRow("probe", "episodes", 1, "nas_read"), users)...)
		info, body := probeInfo(t, f)
		if info.Paused || info.PausedByName != nil || !strings.Contains(body, `"paused_by_name":null`) {
			t.Fatalf("info = %+v", info)
		}
	})
}
