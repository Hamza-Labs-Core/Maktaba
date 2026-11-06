package supervisor

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logkeeper"
)

// Epic 53 story 53.02 T06: with the log keeper, each child writes its output to its own
// spool; a child that crashes and is restarted keeps writing there, and the keeper puts
// every line in that service's segments (none of another's).
func TestChildOutputGoesToItsSpool(t *testing.T) {
	k := logkeeper.New(logkeeper.Config{Root: t.TempDir(), Node: "home", Free: func(string) int64 { return -1 }})
	t.Cleanup(k.Close)
	p, _ := fakeProc(t, RoleAPI, "crash", "FAKE_STDOUT=api_said_this", "FAKE_CODE=3")
	q, _ := fakeProc(t, RoleStreaming, "crash", "FAKE_STDOUT=streaming_said_this", "FAKE_CODE=3")
	pol := testPolicy()
	pol.MaxRestarts = 3
	s := New([]Process{p, q}, pol, WithLog(&strings.Builder{}), WithSpool(func(r Role, i string) (*os.File, error) {
		return k.Spool(string(r), i)
	}))
	_, _ = running(t, s)
	waitFor(t, s, "a few restarts", func(sn Snapshot) bool {
		return sn.Processes[0].Restarts >= 2 && sn.Processes[1].Restarts >= 2
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		k.Flush()
		res := k.Read(logkeeper.Query{Services: []string{"api"}, Since: time.Now().Add(-time.Hour)})
		n := 0
		for _, raw := range res.Lines {
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			text, _ := m["attrs"].(map[string]any)["text"].(string)
			if m["service"] != "api" || strings.Contains(text, "streaming") {
				t.Fatalf("an api segment holds another's line: %s", raw)
			}
			if text == "api_said_this" {
				n++
			}
		}
		if n >= 3 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("api's lines in its segments: %d, want ≥ 3 (one per start)", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
