//go:build unix

package main

import (
	"slices"
	"testing"
)

// keptProcesses with on-demand runners (Epic 54): the rows of a previous release with no
// start mode, and this release's.
func TestKeptProcessesReadsOnDemandRunners(t *testing.T) {
	before := []string{
		"api/ wanted=true running=true",
		"plugin-runner/fp wanted=true running=false",
		"plugin-runner/light wanted=true running=false",
	}
	after := []string{
		"api/ wanted=true running=true mode=always",
		"plugin-runner/fp wanted=false running=false mode=on_demand",           // kept: always -> on_demand, idle
		"plugin-runner/light wanted=false running=false mode=on_demand",        // kept: idle
		"plugin-runner/files wanted=false running=false mode=on_demand",        // new: open, idle
		"plugin-runner/service wanted=false running=false held mode=on_demand", // new: held
	}
	sub := &testing.T{}
	got := keptProcesses(sub, before, after, "full")
	if sub.Failed() {
		t.Fatal("rows that follow the rules were refused")
	}
	if !slices.Equal(got, before) {
		t.Fatalf("kept %v, want %v", got, before)
	}

	// a new on-demand runner that runs on an idle home, a kept one that changed otherwise, a
	// kept always process that changed: each one is still caught
	for name, rows := range map[string][2][]string{
		"new and running": {nil, []string{"plugin-runner/net wanted=true running=true mode=on_demand"}},
		"new and wanted":  {nil, []string{"plugin-runner/net wanted=true running=false mode=on_demand"}},
		"kept and running": {
			[]string{"plugin-runner/fp wanted=true running=false"},
			[]string{"plugin-runner/fp wanted=true running=true mode=on_demand"},
		},
		"always changed": {
			[]string{"api/ wanted=true running=true"},
			[]string{"api/ wanted=true running=false mode=always"},
		},
		"both report a mode": {
			[]string{"plugin-runner/fp wanted=true running=false mode=always"},
			[]string{"plugin-runner/fp wanted=false running=false mode=on_demand"},
		},
	} {
		sub := &testing.T{}
		kept := keptProcesses(sub, rows[0], rows[1], "full")
		if !sub.Failed() && slices.Equal(kept, rows[0]) {
			t.Errorf("%s: passed (%v)", name, kept)
		}
	}
}
