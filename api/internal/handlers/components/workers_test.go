package components

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/serversettings"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// The catalog's worker components: every one with a "pipeline/..." process, the core
// pipeline (the main worker) among them.
func TestWorkerComponents(t *testing.T) {
	ids := WorkerComponents(components.Default())
	for _, want := range []string{"core.pipeline", "transcription", "search.semantic"} {
		if !slices.Contains(ids, want) {
			t.Errorf("%s is not a worker component: %v", want, ids)
		}
	}
	cat := components.Default()
	for _, id := range ids {
		c, _ := cat.Get(id)
		if !slices.ContainsFunc(c.Processes, func(p string) bool { return strings.HasPrefix(p, "pipeline/") }) {
			t.Errorf("%s has no pipeline process: %v", id, c.Processes)
		}
	}
}

// workersSupervisor answers POST /components/{id}/restart with one restarted process
// each, behind the per-boot token; refuse names an id it answers 500 for.
func workersSupervisor(t *testing.T, refuse string) (*Packs, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if refuse != "" && strings.Contains(r.URL.Path, "/"+refuse+"/") {
			http.Error(w, `{"error":"components_unavailable"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"restarted": 1})
	}))
	t.Cleanup(srv.Close)
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Packs{Sup: &serversettings.Supervisor{URL: srv.URL, TokenFile: tok}}, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

// RestartWorkers asks the supervisor's own component restart, once for each worker
// component, and answers the processes restarted in all.
func TestRestartWorkers_AsksTheSupervisorForEachWorkerComponent(t *testing.T) {
	pk, calls := workersSupervisor(t, "")
	n, err := pk.RestartWorkers(context.Background(), nil)
	ids := WorkerComponents(components.Default())
	if err != nil || n != len(ids) {
		t.Fatalf("restarted %d, %v; want %d", n, err, len(ids))
	}
	got := calls()
	for _, id := range ids {
		if !slices.Contains(got, "POST /components/"+id+"/restart") {
			t.Errorf("no restart of %s: %v", id, got)
		}
	}
	if len(got) != len(ids) {
		t.Errorf("calls %v", got)
	}
}

func TestRestartWorkers_ASupervisorFailureIsAnError(t *testing.T) {
	pk, _ := workersSupervisor(t, "core.pipeline")
	if _, err := pk.RestartWorkers(context.Background(), nil); err == nil {
		t.Fatal("a failed restart answered no error")
	}
}

// Named components only: the transcription workers restart, the main worker is left alone.
func TestRestartWorkers_OnlyTheComponentsNamed(t *testing.T) {
	pk, calls := workersSupervisor(t, "")
	n, err := pk.RestartWorkers(context.Background(), []string{"transcription", "not.a.worker"})
	if err != nil || n != 1 {
		t.Fatalf("restarted %d, %v", n, err)
	}
	if got := calls(); !slices.Equal(got, []string{"POST /components/transcription/restart"}) {
		t.Fatalf("calls %v", got)
	}
}
