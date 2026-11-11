package videos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// fakeJobs records each enqueue; refuse, when set, is the outcome the
// pipeline answers instead of a job.
type fakeJobs struct {
	refuse string
	calls  []string
}

func (f *fakeJobs) BumpOrEnqueue(_ context.Context, _, stage string, _ int) (int64, string, error) {
	f.calls = append(f.calls, "process:"+stage)
	if f.refuse != "" {
		return 0, "", &NotQueuedError{Stage: stage, Outcome: f.refuse}
	}
	return 7, "pending", nil
}

func (f *fakeJobs) RollbackToStage(_ context.Context, _, stage string, _ int) ([]int64, error) {
	f.calls = append(f.calls, "reprocess:"+stage)
	if f.refuse != "" {
		return nil, &NotQueuedError{Stage: stage, Outcome: f.refuse}
	}
	return []int64{8}, nil
}

func processEnv(t *testing.T, jobs JobControl) http.Handler {
	t.Helper()
	db, _, _ := newDisplayEnv(t)
	h := &Handler{DB: db, JobQ: jobs}
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func post(t *testing.T, srv http.Handler, p *principal.Principal, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/videos/"+vid+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

var adminP = &principal.Principal{UserID: "a", IsAdmin: true, AccessAllLibraries: true}

// A server with no job queue answers 503 to process and reprocess: never a
// success with no job (before, both answered 200 with an empty job id).
func TestProcessWithoutAQueueIsAnHonestError(t *testing.T) {
	srv := processEnv(t, nil)
	for _, c := range []struct{ path, body string }{
		{"/process", `{"stage":"transcribe"}`},
		{"/reprocess", `{"from_stage":"transcribe"}`},
	} {
		rec := post(t, srv, adminP, c.path, c.body)
		if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "job_id") {
			t.Errorf("%s: %d %s, want 503", c.path, rec.Code, rec.Body)
		}
	}
}

func TestProcessQueuesAJob(t *testing.T) {
	jobs := &fakeJobs{}
	srv := processEnv(t, jobs)
	rec := post(t, srv, adminP, "/process", `{"stage":"subtitle_gen","priority":10}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"job_id":7`) ||
		!strings.Contains(rec.Body.String(), `"state":"pending"`) {
		t.Fatalf("process: %d %s", rec.Code, rec.Body)
	}
	rec = post(t, srv, adminP, "/reprocess", `{"from_stage":"transcribe"}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"job_ids":[8]`) ||
		!strings.Contains(rec.Body.String(), `"predecessor_state":"ready"`) {
		t.Fatalf("reprocess: %d %s", rec.Code, rec.Body)
	}
	if strings.Join(jobs.calls, ",") != "process:subtitle_gen,reprocess:transcribe" {
		t.Fatalf("calls %v", jobs.calls)
	}
}

// A stage the pipeline will not queue is an error, never a success.
func TestProcessRefusedStages(t *testing.T) {
	for outcome, want := range map[string]int{
		"unknown_stage":         http.StatusBadRequest,
		"invalid_subject":       http.StatusBadRequest,
		"disabled":              http.StatusConflict,
		"skipped_component_off": http.StatusConflict,
	} {
		srv := processEnv(t, &fakeJobs{refuse: outcome})
		if rec := post(t, srv, adminP, "/process", `{"stage":"x"}`); rec.Code != want {
			t.Errorf("process %s: %d %s, want %d", outcome, rec.Code, rec.Body, want)
		}
		if rec := post(t, srv, adminP, "/reprocess", `{"from_stage":"x"}`); rec.Code != want {
			t.Errorf("reprocess %s: %d %s, want %d", outcome, rec.Code, rec.Body, want)
		}
	}
	jobs := &fakeJobs{}
	srv := processEnv(t, jobs)
	for _, c := range []struct{ path, body string }{
		{"/process", `{"stage":"scan"}`},
		{"/process", `{"stage":"transcribe","priority":5000}`},
		{"/reprocess", `{"from_stage":"scan"}`},
		{"/reprocess", `{}`},
	} {
		if rec := post(t, srv, adminP, c.path, c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: %d %s, want 400", c.path, c.body, rec.Code, rec.Body)
		}
	}
	if len(jobs.calls) != 0 {
		t.Fatalf("a refused request queued %v", jobs.calls)
	}
}

// Process and reprocess are an admin's: anonymous callers and members (even of
// every library) get 403 and nothing is queued.
func TestProcessRefusesNonAdmins(t *testing.T) {
	jobs := &fakeJobs{}
	srv := processEnv(t, jobs)
	member := &principal.Principal{UserID: "m", AccessAllLibraries: true}
	for _, p := range []*principal.Principal{nil, member} {
		for _, c := range []struct{ path, body string }{
			{"/process", `{"stage":"transcribe"}`},
			{"/reprocess", `{"from_stage":"transcribe"}`},
		} {
			if rec := post(t, srv, p, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s as %v: %d, want 403", c.path, p, rec.Code)
			}
		}
	}
	if len(jobs.calls) != 0 {
		t.Fatalf("a refused caller queued %v", jobs.calls)
	}
}
