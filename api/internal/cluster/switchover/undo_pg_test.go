package switchover

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A step of undoing a switch that fails is said in the log (it used to be dropped):
// here the relay agent the supervisor is asked to start again.
func TestAnUndoStepThatFailsIsLogged(t *testing.T) {
	o, h, s, b, _ := setup(t)
	var logs lockedBuf
	o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	h.answers["switch.promote"] = func(json.RawMessage) (bool, any) { return false, "the kit does not open" }
	h.answers["switch.status"] = func(json.RawMessage) (bool, any) { return true, map[string]any{"state": "called_off"} }
	s.mu.Lock()
	s.fail = "relay:true"
	s.mu.Unlock()
	id, err := o.Start(context.Background(), b, "system")
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, o, id, "failed", "handed_over")
	end := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "event=switchover_undo_failed") {
		if time.Now().After(end) {
			t.Fatalf("no switchover_undo_failed line: %s", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "step=relay_on") {
		t.Fatalf("the line does not say which step: %s", logs.String())
	}
}
