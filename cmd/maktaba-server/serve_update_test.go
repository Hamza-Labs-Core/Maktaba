package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// The watch checks that the pipeline runs the new release's pipeline (Epic 35 story 35.4
// AC7, review A12): a plain server's pipeline runs <release>/venv, and its /healthz says
// the version of the release's wheel, in PEP 440's spelling.
func TestMachineHealthChecksThePipelineVersion(t *testing.T) {
	t.Setenv("MAKTABA_PYTHON", "")
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	release := t.TempDir()
	py := filepath.Join(release, "venv", "bin", "python")
	if runtime.GOOS == "windows" {
		py = filepath.Join(release, "venv", "Scripts", "python.exe")
	}
	for _, f := range []string{py, filepath.Join(release, "pipeline", "maktaba_pipeline-1.5.0rc1-py3-none-any.whl")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	status, body := http.StatusOK, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	snap := supervisor.Snapshot{Processes: []supervisor.ChildStatus{{Role: supervisor.RolePipeline,
		State: supervisor.StateRunning, ProbeURL: srv.URL + "/healthz"}}}
	h := &machineHealth{releaseDir: release, snap: func() (supervisor.Snapshot, bool) { return snap, true }}

	for _, c := range []struct {
		name           string
		status         int
		body           string
		healthy, fatal bool
	}{
		{"the release's pipeline", 200, `{"status":"ok","version":"1.5.0rc1"}`, true, false},
		{"the old environment's", 200, `{"status":"ok","version":"1.4.0"}`, false, true},
		{"a pipeline from before it said its version", 200, `{"status":"ok","service":"pipeline"}`, true, false},
		{"a stale loop", 503, `{"status":"fail","version":"1.5.0rc1"}`, false, false},
	} {
		status, body = c.status, c.body
		v := h.Check(context.Background(), "v1.5.0-rc.1")
		if v.Healthy != c.healthy || v.Fatal != c.fatal {
			t.Errorf("%s: %+v, want healthy %v fatal %v", c.name, v, c.healthy, c.fatal)
		}
	}

	// a pipeline under MAKTABA_PYTHON runs another environment: its version is not the
	// release's to check
	t.Setenv("MAKTABA_PYTHON", py)
	status, body = 200, `{"status":"ok","version":"0.0.1"}`
	if v := h.Check(context.Background(), "v1.5.0-rc.1"); !v.Healthy {
		t.Fatalf("under MAKTABA_PYTHON: %+v", v)
	}
}
