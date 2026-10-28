package updater

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// A TV held on the release before (its change's check failed: the owner's decision of
// 2026-10-28) keeps running that release's streaming binary: no commit prunes it while
// the live TV uses it, whatever the install kind.

// keptPayload is a desktop app's copy of a payload kept before an update.
func keptPayload(t *testing.T, lroot, name, version string) string {
	t.Helper()
	dir := filepath.Join(lroot, "updates", "payloads", name)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "bin", layout.Exe("maktaba-streaming")), []byte("streaming "+version), 0o755)
	if err := os.WriteFile(filepath.Join(dir, keptMarker), []byte(version+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAppCommitKeepsThePayloadTheTVRuns(t *testing.T) {
	lroot := t.TempDir()
	bundled := filepath.Join(t.TempDir(), "server")
	_ = os.MkdirAll(bundled, 0o755)
	from := keptPayload(t, lroot, "1.5.0", "1.5.0")  // the release before: the way back
	held := keptPayload(t, lroot, "1.4.0", "1.4.0")  // the live TV's streaming software
	slot := keptPayload(t, lroot, "1.3.0", "1.3.0")  // a slot bound to it (InUse)
	older := keptPayload(t, lroot, "1.2.0", "1.2.0") // nothing uses it
	broken := filepath.Join(lroot, "updates", "payloads", "cut-short")
	_ = os.MkdirAll(broken, 0o755)
	ai := &AppInstaller{Payload: bundled, Bundled: bundled, LauncherRoot: lroot,
		InUse: func() []string { return []string{"v1.3.0"} }}
	j := &Journal{Job: Job{RunID: "run-2", Version: "1.6.0"}, From: "1.5.0", FromRef: from,
		TV: &TVRun{Outcome: TVHeld, LiveStreaming: "v1.4.0"}}
	if err := ai.Commit(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{from, held, slot} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s was pruned: %v", filepath.Base(keep), err)
		}
	}
	for _, gone := range []string{older, broken} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s was kept", filepath.Base(gone))
		}
	}
	// and a slot bound to 1.4.0 finds its binary there
	if got := ai.StreamingOf("v1.4.0"); len(got) != 1 || got[0] != filepath.Join(held, "bin", layout.Exe("maktaba-streaming")) {
		t.Errorf("StreamingOf %v", got)
	}
	if got := KeptStreaming(lroot, "1.2.0"); len(got) != 0 {
		t.Errorf("a pruned release's binary is found: %v", got)
	}
}

func TestLayoutCommitKeepsTheReleaseTheTVRuns(t *testing.T) {
	l := layout.New(t.TempDir())
	for _, v := range []string{"1.1.0", "1.2.0", "1.3.0", "1.4.0", "1.5.0"} {
		if err := os.MkdirAll(l.ReleaseDir(v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	li := &LayoutInstaller{Layout: l, Version: "1.5.0"}
	j := &Journal{Job: Job{RunID: "run-2", Version: "1.5.0", KeepVersions: 2}, From: "1.4.0",
		TV: &TVRun{Outcome: TVHeld, LiveStreaming: "v1.2.0"}}
	if err := li.Commit(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	vs, _ := l.Versions()
	got := map[string]bool{}
	for _, v := range vs {
		got[v] = true
	}
	if !got["1.2.0"] || !got["1.4.0"] || !got["1.5.0"] || got["1.1.0"] || got["1.3.0"] {
		t.Fatalf("kept %v: want the held TV's 1.2.0, the one before and the newest", vs)
	}
}
