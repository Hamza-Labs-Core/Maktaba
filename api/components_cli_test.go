package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// fakeComponents answers the CLI like the service, and records what it applied.
type fakeComponents struct {
	preview components.Preview
	applied []components.Request
	actors  []components.Actor
	refuse  error
}

func (f *fakeComponents) List(context.Context) (components.ListView, error) {
	return components.ListView{Revision: 3, Bundle: components.BundleLine{Base: "full", Minus: []string{"youtube"}},
		Components: []components.View{{ID: "llm", Group: "processing", Want: catalog.WantEnabled, Phase: catalog.PhaseRunning}}}, nil
}

func (f *fakeComponents) Show(_ context.Context, id string) (components.Detail, error) {
	return components.Detail{View: components.View{ID: id, Want: catalog.WantEnabled}}, nil
}

func (f *fakeComponents) Preview(context.Context, components.Request) (components.Preview, error) {
	return f.preview, nil
}

func (f *fakeComponents) Apply(_ context.Context, req components.Request, a components.Actor) (components.Result, error) {
	if f.refuse != nil {
		return components.Result{}, f.refuse
	}
	f.applied = append(f.applied, req)
	f.actors = append(f.actors, a)
	return components.Result{Changed: []store.Desired{{ID: req.Changes[0].ID, Want: req.Changes[0].Want, Generation: 2}}}, nil
}

func cli(f *fakeComponents, tty bool, stdin string) (*componentsCLI, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &componentsCLI{svc: f, in: strings.NewReader(stdin), out: &out, errOut: &errOut, tty: tty, user: "maya"}, &out, &errOut
}

func runCLI(c *componentsCLI, argv ...string) int {
	flags, args := splitComponentsArgs(argv)
	return c.run(context.Background(), flags, args)
}

func llmOff() *fakeComponents {
	return &fakeComponents{preview: components.Preview{Effects: []components.Effect{
		{ID: "llm", From: catalog.PhaseRunning, To: catalog.PhaseOff},
		{ID: "classify", From: catalog.PhaseRunning, To: catalog.PhaseDegraded, Reason: "want_missing:llm"}}}}
}

// T11: a write with no terminal and no --yes refuses with exit 2 and changes nothing.
func TestComponentsCLINeedsYesWithoutATerminal(t *testing.T) {
	f := llmOff()
	c, out, errOut := cli(f, false, "")
	if code := runCLI(c, "disable", "llm"); code != exitComponentsNeedYes {
		t.Fatalf("exit %d", code)
	}
	if len(f.applied) != 0 || !strings.Contains(errOut.String(), "--yes") || !strings.Contains(out.String(), "classify: running -> degraded") {
		t.Fatalf("applied %v; out %q; err %q", f.applied, out, errOut)
	}
}

// --yes (after the id, too) applies, as the OS user with actor source cli.
func TestComponentsCLIYesApplies(t *testing.T) {
	f := llmOff()
	c, out, _ := cli(f, false, "")
	if code := runCLI(c, "disable", "llm", "--yes"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(f.applied) != 1 || f.applied[0].Changes[0].Want != catalog.WantInstalled ||
		f.actors[0] != (components.Actor{By: "maya", Source: "cli"}) || !strings.Contains(out.String(), "Done.") {
		t.Fatalf("applied %+v as %+v: %s", f.applied, f.actors, out)
	}
	f = llmOff()
	c, _, _ = cli(f, false, "")
	if code := runCLI(c, "enable", "livetv.inprocess", "--with-conflicts", "--yes"); code != 0 || !f.applied[0].WithConflicts {
		t.Fatalf("with-conflicts: %d %+v", code, f.applied)
	}
}

// On a terminal it asks; anything but y changes nothing.
func TestComponentsCLIAsks(t *testing.T) {
	f := llmOff()
	c, out, _ := cli(f, true, "n\n")
	if code := runCLI(c, "disable", "llm"); code != exitComponentsFailed || len(f.applied) != 0 || !strings.Contains(out.String(), "Continue? [y/N]") {
		t.Fatalf("exit %d, applied %v: %s", code, f.applied, out)
	}
	c, _, _ = cli(f, true, "y\n")
	if code := runCLI(c, "disable", "llm"); code != 0 || len(f.applied) != 1 {
		t.Fatalf("yes: exit %d, applied %v", code, f.applied)
	}
}

// A stop is a hold with no end; start needs no question.
func TestComponentsCLIStopStart(t *testing.T) {
	f := &fakeComponents{}
	c, _, _ := cli(f, false, "")
	if code := runCLI(c, "stop", "youtube", "--yes"); code != 0 || f.applied[0].Changes[0].Hold == nil || !*f.applied[0].Changes[0].Hold {
		t.Fatalf("stop: %d %+v", code, f.applied)
	}
	if code := runCLI(c, "start", "youtube"); code != 0 || *f.applied[1].Changes[0].Hold {
		t.Fatalf("start: %d %+v", code, f.applied)
	}
}

// A refusal, a preview refusal, an unknown command or flag, and what waits for packs exit 1.
func TestComponentsCLIRefusals(t *testing.T) {
	f := &fakeComponents{preview: components.Preview{Refused: []components.Refusal{
		{Code: components.CodeRequiredBy, Detail: "required", RequiredBy: []string{"transcription"}}}}}
	c, _, errOut := cli(f, false, "")
	if code := runCLI(c, "remove", "runtime.ml", "--yes"); code != exitComponentsFailed || len(f.applied) != 0 ||
		!strings.Contains(errOut.String(), "transcription") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, argv := range [][]string{{"frob", "x"}, {"list", "--loud"}, {"enable"}, {"remove", "youtube", "--purge", "--yes"}} {
		c, _, _ := cli(&fakeComponents{}, false, "")
		if code := runCLI(c, argv...); code != exitComponentsFailed {
			t.Errorf("%v: exit %d", argv, code)
		}
	}
}

// list prints the table and the bundle line; bundle with no name prints the line.
func TestComponentsCLIList(t *testing.T) {
	c, out, _ := cli(&fakeComponents{}, false, "")
	if code := runCLI(c, "list"); code != 0 || !strings.Contains(out.String(), "llm") ||
		!strings.Contains(out.String(), "Based on full, minus youtube.") {
		t.Fatalf("exit %d: %s", code, out)
	}
	c, out, _ = cli(&fakeComponents{}, false, "")
	if code := runCLI(c, "list", "--json"); code != 0 || !strings.Contains(out.String(), `"revision": 3`) {
		t.Fatalf("json: %d %s", code, out)
	}
}

// With the server's database not answering, the CLI says so in one line and exits 3.
func TestComponentsCLIServerNotRunning(t *testing.T) {
	code := runComponents([]string{"list", "--dsn", "postgres://x:x@127.0.0.1:1/x?sslmode=disable&connect_timeout=1"})
	if code != exitComponentsNotRunning {
		t.Fatalf("exit %d", code)
	}
}
