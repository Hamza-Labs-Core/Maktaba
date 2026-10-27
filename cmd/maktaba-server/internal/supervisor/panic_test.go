package supervisor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A panic in a child's loop (the supervisor's own bug) does not kill the launcher: it
// is logged at error level with the role and its stack, and the stack stops as on a
// fatal exit, Run returning the panic as its error.
func TestLoopPanicStopsTheStackAndIsLogged(t *testing.T) {
	logs := &syncBuffer{}
	p := Process{Role: RoleAPI, Cmd: exec.Command(os.Args[0], "-test.run=^$"),
		BeforeStart: func() { panic("boom") }}
	s := New([]Process{p}, testPolicy(), WithLog(logs))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := s.Run(ctx, time.Second)
	if err == nil || !strings.Contains(err.Error(), "panicked") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Run returned %v, want the panic", err)
	}
	out := logs.String()
	for _, want := range []string{"[supervisor] level=error api: the supervisor's loop panicked (boom)", "panic_test.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if s.panicStop.Load() == nil {
		t.Error("no exit watchdog armed for a stop that never ends")
	}
}
