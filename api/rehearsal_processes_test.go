//go:build unix

package main

import (
	"encoding/json"
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

// crashLooping names a process the supervisor marked failed or that crash-loops, with its
// last exit; a running, idle or held one is none.
func TestCrashLoopingNamesFailedProcesses(t *testing.T) {
	body := `{"processes":[
		{"role":"api","state":"running"},
		{"role":"plugin-runner","instance":"light","state":"stopped"},
		{"role":"plugin-runner","instance":"ml-1","state":"failed","crash_loop":true,
		 "last_exit":{"code":1,"reason":"exited","error":"exit status 1"}},
		{"role":"plugin-runner","instance":"index","state":"backoff","crash_loop":true},
		{"role":"pipeline","state":"failed","detail":"the transcription tools could not be installed"}]}`
	var snap struct {
		Processes []supervisedProcess `json:"processes"`
	}
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pipeline/ state=failed crash_loop=false; the transcription tools could not be installed",
		"plugin-runner/index state=backoff crash_loop=true",
		"plugin-runner/ml-1 state=failed crash_loop=true last exit 1 (exited): exit status 1",
	}
	if got := crashLooping(snap.Processes); !slices.Equal(got, want) {
		t.Fatalf("crashLooping =\n%q\nwant\n%q", got, want)
	}
	if bad := crashLooping(snap.Processes[:2]); len(bad) != 0 {
		t.Fatalf("a running and an idle process read %v", bad)
	}
}
