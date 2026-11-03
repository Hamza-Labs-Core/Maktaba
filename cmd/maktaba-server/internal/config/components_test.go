package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Epic 51 story 51.04: [components] reconcile, on unless off, and a file that does not
// set it is saved without it.
func TestComponentsReconcile(t *testing.T) {
	for v, on := range map[string]bool{"": true, "on": true, "off": false, " OFF ": false, "false": false, "0": false} {
		if got := (ComponentsSection{Reconcile: v}).ReconcileOn(); got != on {
			t.Errorf("reconcile = %q: %v, want %v", v, got, on)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "server.toml")
	if err := os.WriteFile(path, []byte("[components]\nreconcile = \"off\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Components.ReconcileOn() {
		t.Fatal("reconcile = off read as on")
	}
	out := filepath.Join(dir, "saved.toml")
	if err := Defaults().SaveTo(out); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	if strings.Contains(string(raw), "[components]") {
		t.Fatalf("an unset [components] is written:\n%s", raw)
	}
}

// Story 51.09: [components] capabilities_gate, on unless off.
func TestComponentsCapabilitiesGate(t *testing.T) {
	for v, on := range map[string]bool{"": true, "on": true, "off": false, " Off ": false, "no": false} {
		if got := (ComponentsSection{CapabilitiesGate: v}).CapabilitiesGateOn(); got != on {
			t.Errorf("capabilities_gate = %q: %v, want %v", v, got, on)
		}
	}
	path := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(path, []byte("[components]\ncapabilities_gate = \"off\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Components.CapabilitiesGateOn() || !cfg.Components.ReconcileOn() {
		t.Fatalf("read %+v", cfg.Components)
	}
}
