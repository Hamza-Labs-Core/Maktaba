package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.12: --machine <name|id> on show, enable, disable, stop and start.

type fakeMachines struct {
	*fakeComponents
}

const cliNode = "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7"

func (fakeMachines) ResolveMachine(_ context.Context, nameOrID string) (string, error) {
	if nameOrID == "old-laptop" || nameOrID == cliNode {
		return cliNode, nil
	}
	return "", &components.Refusal{Code: components.CodeUnknownMachine, Detail: "no machine " + nameOrID}
}

func (fakeMachines) ListMachine(_ context.Context, node string) (components.MachineList, error) {
	ov := store.Override{Node: node, ID: "transcription", Want: catalog.WantInstalled}
	return components.MachineList{Machine: components.Machine{ID: node, Name: "old-laptop"},
		Components: []components.MachineComponent{{View: components.View{ID: "transcription", Want: catalog.WantEnabled},
			Here: components.MachineState{Want: catalog.WantInstalled, Override: &ov, Phase: catalog.PhaseOff}}}}, nil
}

func TestComponentsCLIMachine(t *testing.T) {
	f := fakeMachines{llmOff()}
	c, out, _ := cli(nil, false, "")
	c.svc = f
	if code := runCLI(c, "disable", "transcription", "--machine", "old-laptop", "--yes"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(f.applied) != 1 || f.applied[0].Changes[0].Machine != cliNode || f.applied[0].Changes[0].Want != catalog.WantInstalled {
		t.Fatalf("applied %+v", f.applied)
	}
	out.Reset()
	if code := runCLI(c, "show", "transcription", "--machine=old-laptop"); code != 0 {
		t.Fatalf("show exit %d", code)
	}
	if s := out.String(); !strings.Contains(s, "on old-laptop") || !strings.Contains(s, "installed (home: enabled)") {
		t.Fatalf("show printed %q", s)
	}
	// a machine there is none of, and a command that takes no --machine, are refused
	c2, _, errOut := cli(nil, false, "")
	c2.svc = f
	if code := runCLI(c2, "stop", "transcription", "--machine", "nope", "--yes"); code != exitComponentsFailed ||
		!strings.Contains(errOut.String(), "unknown_machine") {
		t.Fatalf("unknown machine: exit %d %q", code, errOut.String())
	}
	if code := runCLI(c2, "remove", "transcription", "--machine", "old-laptop", "--yes"); code != exitComponentsFailed {
		t.Fatalf("remove --machine: exit %d", code)
	}
	if len(f.applied) != 1 {
		t.Fatalf("a refused command applied %+v", f.applied)
	}
}
