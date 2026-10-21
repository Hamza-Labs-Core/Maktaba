package main

import (
	"strings"
	"testing"
)

// Inside Maktaba.app the app updates the server (Epic 36 story 36.8).
func TestUpdateRefusedInsideTheApp(t *testing.T) {
	for _, env := range [][2]string{{"MAKTABA_APP_BUNDLE", t.TempDir()}, {"MAKTABA_UPDATE_DISABLE", "1"}} {
		t.Setenv("MAKTABA_APP_BUNDLE", "")
		t.Setenv("MAKTABA_UPDATE_DISABLE", "")
		t.Setenv(env[0], env[1])
		cmd := newUpdateCmd()
		cmd.SetArgs([]string{"--check"})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "Maktaba.app") {
			t.Fatalf("%s: update = %v, want it refused", env[0], err)
		}
	}
}
