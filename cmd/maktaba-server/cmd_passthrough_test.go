package main

import (
	"errors"
	"os/exec"
	"runtime"
	"testing"
)

// `components` keeps the child's exit code (2: no --yes without a terminal, 3: the
// server is not running); an error that is no child's exit is not one.
func TestComponentsPassesTheExitCodeThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	for _, want := range []int{1, 2, 3} {
		err := exec.Command("sh", "-c", "exit "+string(rune('0'+want))).Run()
		if code, ok := childExitCode(err); !ok || code != want {
			t.Errorf("exit %d read as %d (%v)", want, code, ok)
		}
	}
	if _, ok := childExitCode(errors.New("no maktaba-api")); ok {
		t.Error("an error that is no exit has a code")
	}
	if _, ok := childExitCode(nil); ok {
		t.Error("success has a code")
	}
	found := false
	for _, c := range newRootCmd().Commands() {
		if c.Name() == "components" {
			found = c.DisableFlagParsing
		}
	}
	if !found {
		t.Fatal("no components command that hands its flags to maktaba-api")
	}
}
