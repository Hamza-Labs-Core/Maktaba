package queues

import (
	"database/sql/driver"
	"net/http"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// A library-scoped caller sees the control centre's items of its own
// libraries only; counts stay global.
func TestAccess_JobRowsAreLibraryScoped(t *testing.T) {
	other := "66666666-6666-4666-8666-666666666666"
	mine, theirs := libL, other
	refs := []JobRef{{ID: 1, LibraryID: &mine}, {ID: 2, LibraryID: &theirs}, {ID: 3}}
	got := filterRefs(libViewer(), refs)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("library viewer sees %+v", got)
	}
	if got := filterRefs(admin(), refs); len(got) != 3 {
		t.Fatalf("admin sees %d rows", len(got))
	}
	snap := Snapshot{Queues: []QueueSummary{{Stage: "probe", RunningJobs: refs}}}
	if s := scopeSnapshot(libViewer(), snap); len(s.Queues[0].RunningJobs) != 1 {
		t.Fatalf("snapshot running = %+v", s.Queues[0].RunningJobs)
	}
	if len(snap.Queues[0].RunningJobs) != 3 {
		t.Fatal("scopeSnapshot changed the shared snapshot")
	}
	groups := []FailureGroup{{Stage: "probe", Count: 2, SampleMessage: "/media/x.mkv: timeout",
		SampleSubjects: []SubjectSample{{Kind: "video", ID: "v", Label: "Hidden"}}}}
	if g := scopeFailures(libViewer(), groups); g[0].SampleMessage != "" || len(g[0].SampleSubjects) != 0 || g[0].Count != 2 {
		t.Fatalf("failure group for a library viewer = %+v", g[0])
	}
}

func TestAccess_FilterEvent(t *testing.T) {
	p := libViewer()
	jobEv := func(lib any) ws.Event {
		return ws.Event{Type: "job.started", Payload: map[string]any{"job": map[string]any{"id": 1.0, "library_id": lib}}}
	}
	if _, ok := FilterEvent(p, "jobs", jobEv(libL)); !ok {
		t.Error("own library's job event dropped")
	}
	if _, ok := FilterEvent(p, "jobs", jobEv("66666666-6666-4666-8666-666666666666")); ok {
		t.Error("another library's job event passed")
	}
	if _, ok := FilterEvent(p, "jobs", jobEv(nil)); ok {
		t.Error("a library-less job event passed")
	}
	if _, ok := FilterEvent(p, "jobs", ws.Event{Type: "queue.control", Payload: map[string]any{"stage": "probe"}}); !ok {
		t.Error("a global queue.control event dropped")
	}
	if _, ok := FilterEvent(admin(), "jobs", jobEv(nil)); !ok {
		t.Error("admin lost an event")
	}
	snap := ws.Event{Type: SnapshotEventType, Payload: map[string]any{"queues": []any{
		map[string]any{"stage": "probe", "running_jobs": []any{
			map[string]any{"id": 1.0, "library_id": libL},
			map[string]any{"id": 2.0, "library_id": "66666666-6666-4666-8666-666666666666"},
		}}}}}
	e, ok := FilterEvent(p, SnapshotChannel, snap)
	if !ok {
		t.Fatal("snapshot dropped")
	}
	run := e.Payload["queues"].([]any)[0].(map[string]any)["running_jobs"].([]any)
	if len(run) != 1 {
		t.Fatalf("snapshot running_jobs = %v", run)
	}
	orig := snap.Payload["queues"].([]any)[0].(map[string]any)["running_jobs"].([]any)
	if len(orig) != 2 {
		t.Fatal("FilterEvent changed the shared event")
	}
}

func TestAccess_ActivityAndHistoryHideOtherLibraries(t *testing.T) {
	f := newFixture(stagesRule())
	other := "66666666-6666-4666-8666-666666666666"
	if rec := f.do(http.MethodGet, "/api/activity?library_id="+other, "", libViewer()); rec.Code != http.StatusNotFound {
		t.Fatalf("activity of another library: %d", rec.Code)
	}
	g := newFixture(stagesRule(),
		videoSubjectRule(other))
	if rec := g.do(http.MethodGet, "/api/items/video/"+uidA+"/history", "", libViewer()); rec.Code != http.StatusNotFound {
		t.Fatalf("history of another library's video: %d %s", rec.Code, rec.Body.String())
	}
	if rec := g.do(http.MethodGet, "/api/items/catalog/"+CatalogID+"/history", "", libViewer()); rec.Code != http.StatusNotFound {
		t.Fatalf("catalog history for a library viewer: %d", rec.Code)
	}
	for _, path := range []string{"/api/workers", "/api/extensions"} {
		if rec := f.do(http.MethodGet, path, "", allViewer()); rec.Code != http.StatusForbidden {
			t.Errorf("%s for a non-admin: %d", path, rec.Code)
		}
	}
}

func TestAccess_LibrariesListIsScoped(t *testing.T) {
	rows := []LibraryQueueRow{{LibraryID: libL, Name: "Mine"}, {LibraryID: "66666666-6666-4666-8666-666666666666", Name: "Theirs"}}
	if got := visibleLibraryRows(libViewer(), rows); len(got) != 1 || got[0].Name != "Mine" {
		t.Fatalf("library viewer rows = %+v", got)
	}
	if got := visibleLibraryRows(&principal.Principal{UserID: "x"}, rows); len(got) != 0 {
		t.Fatalf("no-library viewer rows = %+v", got)
	}
	if got := visibleLibraryRows(admin(), rows); len(got) != 2 {
		t.Fatalf("admin rows = %+v", got)
	}
}

// videoSubjectRule answers the history subject lookup of uidA as a video
// of library lib.
func videoSubjectRule(lib string) *queuestest.Rule {
	return &queuestest.Rule{Match: "from videos v join libraries l", Cols: []string{"lib", "libname", "dur", "inseries", "isextra"},
		Rows: [][]driver.Value{{lib, "Theirs", 600.0, false, false}}}
}
