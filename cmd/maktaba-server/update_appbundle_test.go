package main

import (
	"strings"
	"testing"
)

// Inside Maktaba.app the app updates the server (Epic 36 story 36.8).
func TestUpdateRefusedInsideTheApp(t *testing.T) {
	t.Setenv("MAKTABA_APP_BUNDLE", t.TempDir())
	cmd := newUpdateCmd()
	cmd.SetArgs([]string{"--check"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "Maktaba.app") {
		t.Fatalf("update = %v, want it refused", err)
	}
}
