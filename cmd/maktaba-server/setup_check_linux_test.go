package main

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostcheck"
)

// quietPlatformCheck: a machine the server runs on (TestMain).
func quietPlatformCheck() {
	linuxCheck = func(context.Context) hostcheck.Result { return hostcheck.Result{} }
}

func TestCheckStopsOnALinuxProblem(t *testing.T) {
	newSetupEnv(t)
	prevFacts, prevCheck := hostFacts, linuxCheck
	t.Cleanup(func() { hostFacts, linuxCheck = prevFacts, prevCheck })
	hostFacts = func(string) (uint64, uint64) { return 32 << 30, 500 << 30 }
	linuxCheck = func(context.Context) hostcheck.Result {
		return hostcheck.Result{Problems: []string{"no user manager"}, Warnings: []string{"no linger"}}
	}
	_, _, ev := runStep(t, "check", map[string]any{})
	d := data(t, ev)
	warns, _ := d["warnings"].([]any)
	if d["ok"] != false || len(warns) != 2 || warns[0] != "no user manager" || warns[1] != "no linger" {
		t.Fatalf("check = %v", d)
	}

	// a warning alone does not block
	linuxCheck = func(context.Context) hostcheck.Result { return hostcheck.Result{Warnings: []string{"no linger"}} }
	_, _, ev = runStep(t, "check", map[string]any{})
	if d := data(t, ev); d["ok"] != true {
		t.Fatalf("a warning blocked: %v", d)
	}
}
