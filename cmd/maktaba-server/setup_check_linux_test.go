package main

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostcheck"
)

// quietLinuxCheck: a machine the server runs on, so no test depends on this
// machine's systemd and glibc.
func quietLinuxCheck(context.Context) hostcheck.Result { return hostcheck.Result{} }

func init() { linuxCheck = quietLinuxCheck }

func TestCheckStopsOnALinuxProblem(t *testing.T) {
	newSetupEnv(t)
	prevFacts := hostFacts
	t.Cleanup(func() { hostFacts, linuxCheck = prevFacts, quietLinuxCheck })
	hostFacts = func(string) (uint64, uint64) { return 32 << 30, 500 << 30 }
	linuxCheck = func(context.Context) hostcheck.Result {
		return hostcheck.Result{
			Problems: []hostcheck.Note{{Code: "glibc_old", Message: "old glibc", Params: []any{"have", "2.31", "need", "2.35"}}},
			Warnings: []hostcheck.Note{{Code: "linger_off", Message: "no linger"}},
		}
	}
	_, _, ev := runStep(t, "check", map[string]any{})
	d := data(t, ev)
	problems, _ := d["problems"].([]any)
	warnings, _ := d["warnings"].([]any)
	if d["ok"] != false || len(problems) != 1 || len(warnings) < 1 {
		t.Fatalf("check = %v", d)
	}
	p, _ := problems[0].(map[string]any)
	params, _ := p["params"].(map[string]any)
	if p["code"] != "glibc_old" || p["message"] != "old glibc" || params["have"] != "2.31" || params["need"] != "2.35" {
		t.Errorf("problem %v", p)
	}
	found := false
	for _, w := range warnings {
		if m, _ := w.(map[string]any); m["code"] == "linger_off" {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings %v", warnings)
	}

	// a warning alone does not block
	linuxCheck = func(context.Context) hostcheck.Result {
		return hostcheck.Result{Warnings: []hostcheck.Note{{Code: "linger_off", Message: "no linger"}}}
	}
	_, _, ev = runStep(t, "check", map[string]any{})
	if d := data(t, ev); d["ok"] != true {
		t.Fatalf("a warning blocked: %v", d)
	}
}
