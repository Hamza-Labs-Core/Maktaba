package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func TestStorageCommand(t *testing.T) {
	cfgPath, data, _ := storageEnv(t)
	if err := os.MkdirAll(filepath.Join(data, "models"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "models", "m.bin"), []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printStorage(&out, storageDoc(cfg))
	if !strings.Contains(out.String(), "Data folder ([server] data_dir): "+data) || !strings.Contains(out.String(), "models") {
		t.Errorf("show:\n%s", out.String())
	}
	target := filepath.Join(filepath.Dir(filepath.Dir(data)), "New")
	out.Reset()
	if err := runStorageMove(context.Background(), &out, cfg, target, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nothing was changed (--dry-run).") {
		t.Errorf("dry run:\n%s", out.String())
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("a dry run made the target")
	}
	out.Reset()
	if err := runStorageMove(context.Background(), &out, cfg, target, false); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, w := range []string{"Maktaba's data is now in " + target, "The old copy is kept", "rm -rf"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("move output lacks %q:\n%s", w, out.String())
		}
	}
	after, _, _ := config.LoadFrom(cfgPath)
	if after.Server.DataDir != target {
		t.Errorf("data_dir %q", after.Server.DataDir)
	}
	// a refused target says why and changes nothing
	out.Reset()
	if err := runStorageMove(context.Background(), &out, after, target, false); err == nil ||
		!strings.Contains(out.String(), "Cannot move:") {
		t.Errorf("same target: %v\n%s", err, out.String())
	}
}

// Epic 54, story 54.25 §3: `maktaba-server stages impl` flips only a built-in plugin's
// stage, to inproc or plugin; it refuses anything else before it touches the database.
func TestStagesImplRefusesBeforeTheDatabase(t *testing.T) {
	var out bytes.Buffer
	if err := runStagesImpl(context.Background(), &out, nil, "promo_links", "wasm", 0); err == nil ||
		!strings.Contains(err.Error(), "inproc or plugin") {
		t.Fatalf("a bad impl: %v", err)
	}
	if err := runStagesImpl(context.Background(), &out, nil, "probe", "plugin", 0); err == nil ||
		!strings.Contains(err.Error(), "not a built-in plugin's stage") {
		t.Fatalf("a core stage: %v", err)
	}
	names := stageNamesOf("[[stage]]\nname = \"a\"\n[[stage]]\nname = \"b\"\n")
	if strings.Join(names, ",") != "a,b" {
		t.Fatalf("stages %v", names)
	}
	if newStagesCmd().Commands()[0].Name() != "impl" {
		t.Fatal("stages impl")
	}
}
