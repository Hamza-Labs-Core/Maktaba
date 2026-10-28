package frontdoor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

// A slot bound to a release runs the binary the release names, however the release
// spells it (the binary's "v0.3.40" or the pipeline's "0.3.40"), and this binary when the
// release names none (no version, or a source checkout's 0.0.1, for which the supervisor
// has no binary).
func TestReleaseBinarySpellings(t *testing.T) {
	was := version.Version
	version.Version = "v0.3.40"
	t.Cleanup(func() { version.Version = was })
	for _, c := range []struct {
		rv, rs, wantV, wantS string
	}{
		{"0.3.40", "", "v0.3.40", ""},              // #51: the pipeline's spelling of this binary
		{"v0.3.40", "abc", "v0.3.40", ""},          // this binary, as before
		{"", "", "", ""},                           // no version recorded: the supervisor's choice
		{"0.0.1", "abc", "", ""},                   // a source checkout's release: the same
		{"v0.3.39", "def", "v0.3.39", "def"},       // another release's binary
		{"0.3.39", "", "0.3.39", ""},               // the supervisor finds releases/0.3.39
		{"v0.3.40-rc.1", "x", "v0.3.40-rc.1", "x"}, // a prerelease is another binary
	} {
		if v, s := releaseBinary(c.rv, c.rs); v != c.wantV || s != c.wantS {
			t.Errorf("releaseBinary(%q, %q) = %q, %q; want %q, %q", c.rv, c.rs, v, s, c.wantV, c.wantS)
		}
	}
}

// A staged release that names no binary (a source checkout's pipeline records 0.0.1)
// asks the supervisor for this binary, not for a release directory 0.0.1 that no install
// has; a release that names one asks for it with its checksum.
func TestStageStartAsksForTheReleasesBinary(t *testing.T) {
	g, _ := stageRig(t, 0)
	was := version.Version
	version.Version = "v0.3.40"
	t.Cleanup(func() { version.Version = was })
	var mu sync.Mutex
	var bodies []map[string]string
	sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(sup.Close)
	g.slots.Supervisor = sup.URL
	stage := func(ver, sum string) map[string]string {
		t.Helper()
		plan := StagingPlan{ChangeID: uuid.NewString(), ReleaseID: uuid.NewString(), Version: ver, SHA256: sum,
			Channels: []string{g.ch.String()}}
		if _, err := g.slots.StageStart(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		for i := len(bodies) - 1; i >= 0; i-- {
			if bodies[i]["release_id"] == plan.ReleaseID {
				return bodies[i]
			}
		}
		t.Fatalf("the supervisor was not asked to start release %s: %v", plan.ReleaseID, bodies)
		return nil
	}
	if b := stage("0.0.1", ""); b["version"] != "" || b["sha256"] != "" {
		t.Fatalf("a source checkout's release asked for %q", b["version"])
	}
	if b := stage("v0.3.41", "abc"); b["version"] != "v0.3.41" || b["sha256"] != "abc" {
		t.Fatalf("a binary change asked for %v", b)
	}
}
