package appvenv

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Story 51.17: a venv with no extras takes the payload's constraints-core.txt (the runtime
// alone: no torch, no MLX); the default extras and a payload without the file keep
// --payload and the full constraints.
func TestRunNoExtrasTakesTheCoreConstraints(t *testing.T) {
	payload := fakePayload(t, "build-ok")
	core := filepath.Join(payload, "pipeline", CoreConstraintsFile)
	if CoreConstraints(filepath.Join(payload, "pipeline")) != "" {
		t.Fatal("a payload without the file has core constraints")
	}
	if err := os.WriteFile(core, []byte("x==1 --hash=sha256:00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := spec(payload)
	s.NoExtras = true
	if _, err := Run(context.Background(), s, nil); err != nil {
		t.Fatal(err)
	}
	cs := calls(t, payload)
	args := cs[len(cs)-1].Args
	i := slices.Index(args, "--constraints")
	if i < 0 || args[i+1] != core || slices.Contains(args, "--payload") || !slices.Contains(args, "--wheel") {
		t.Fatalf("NoExtras with constraints-core.txt: %v", args)
	}
	_ = os.Remove(filepath.Join(payload, "fake-calls.jsonl"))
	if _, err := Run(context.Background(), spec(payload), nil); err != nil {
		t.Fatal(err)
	}
	cs = calls(t, payload)
	if args := cs[len(cs)-1].Args; slices.Contains(args, "--constraints") || !slices.Contains(args, "--payload") {
		t.Fatalf("the default extras: %v", args)
	}
}
