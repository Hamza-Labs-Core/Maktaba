package supervisor

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 38 story 38.1: on Windows the pipeline writes UTF-8 to its pipes and does
// not buffer them (Arabic titles in a log line raised UnicodeEncodeError under the
// ANSI code page); an operator's own setting is kept, and other OSes are left alone.
func TestPipelinePythonEnvOnWindows(t *testing.T) {
	prev := onWindows
	t.Cleanup(func() { onWindows = prev })
	for _, k := range []string{"PYTHONUTF8", "PYTHONIOENCODING", "PYTHONUNBUFFERED"} {
		t.Setenv(k, "")
	}
	cfg := config.Defaults()

	onWindows = func() bool { return true }
	m := envMap(childEnvOK(RolePipeline, cfg))
	if m["PYTHONUTF8"] != "1" || m["PYTHONIOENCODING"] != "utf-8" || m["PYTHONUNBUFFERED"] != "1" {
		t.Fatalf("pipeline env on Windows: %v", m)
	}
	if _, ok := envMap(childEnvOK(RoleAPI, cfg))["PYTHONUTF8"]; ok {
		t.Error("only the pipeline runs Python")
	}
	t.Setenv("PYTHONIOENCODING", "utf-8:replace")
	if _, ok := envMap(childEnvOK(RolePipeline, cfg))["PYTHONIOENCODING"]; ok {
		t.Error("the operator's own PYTHONIOENCODING must be kept (it is inherited, not overridden)")
	}

	onWindows = func() bool { return false }
	if _, ok := envMap(childEnvOK(RolePipeline, cfg))["PYTHONUTF8"]; ok {
		t.Error("PYTHONUTF8 is for Windows only")
	}
}
