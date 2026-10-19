package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

const testRepo = "Hamza-Labs-Core/Maktaba"

type recorder struct{ calls []string }

func (r *recorder) run(name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	// the old worker exists only as maktaba-node (schtasks /Query of the others fails)
	if name == "schtasks" && len(args) >= 3 && args[0] == "/Query" && args[2] != "maktaba-node" {
		return errors.New("no such task")
	}
	return nil
}

func joinedNode() config.NodeConfig {
	return config.NodeConfig{NodeID: "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7", Name: "studio",
		PrivateKey: "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=",
		Home: config.HomeRef{URL: "http://home.invalid:8080", InstanceID: "0a1b2c3d-0000-4000-8000-000000000000",
			ClusterPub: "RSujjNFA61v0BQ1NiOVfwb46U0QPe3dj8ve08aDH0V8="},
		DatabaseURL: "postgresql://maktaba_node_x:secret@home.invalid:5432/maktaba"}
}

func fakeRelease(t *testing.T) (*releaseverifytest.Sigstore, *releaseverifytest.FakeRelease, string) {
	t.Helper()
	ss := releaseverifytest.MustNew()
	rel := ss.MustRelease(testRepo, "v1.5.0", runtime.GOOS+"/"+runtime.GOARCH)
	dir := t.TempDir()
	if err := rel.WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	return ss, rel, dir
}

func newTestSetup(t *testing.T, ss *releaseverifytest.Sigstore, from string, rec *recorder) (*nodeSetup, string, []byte) {
	t.Helper()
	tm, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	id, err := releaseverify.NewIdentity(testRepo)
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	nodePath := filepath.Join(tmp, "node.json")
	nodeBytes := []byte(`{"node_id":"6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7","private_key":"…","home":{"url":"http://home.invalid:8080"}}`)
	if err := os.WriteFile(nodePath, nodeBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	healthy := 0
	s := &nodeSetup{Root: filepath.Join(tmp, "install"), From: from, Python: "/opt/worker/python", Mode: "task",
		Instances: 2, Out: &bytes.Buffer{}, NodeConfig: joinedNode(), NodePath: nodePath,
		ConfigPath: filepath.Join(tmp, "server.toml"), Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Run: rec.run, Identity: id, Trust: tm,
		Healthy: func(context.Context) error { healthy++; return nil }}
	return s, nodePath, nodeBytes
}

// TC6/TC7 of story 35.8: a converted node keeps its node config, installs the
// verified release side by side, writes [pipeline] instances, registers the service
// and removes the old worker only after the new service answered.
func TestSetupNodeConvertsWorkerOnlyNode(t *testing.T) {
	ss, rel, from := fakeRelease(t)
	rec := &recorder{}
	s, nodePath, nodeBytes := newTestSetup(t, ss, from, rec)
	if err := s.run(context.Background()); err != nil {
		t.Fatalf("setup --node: %v\n%s", err, s.Out)
	}
	dir := filepath.Join(s.Root, "bin", "releases", "1.5.0")
	for name, want := range rel.Binaries {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s not installed: %v", name, err)
		}
	}
	for _, f := range []string{".verified", "release.json", "checksums.txt", "checksums.txt.sigstore.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s missing", f)
		}
	}
	if readPointer(filepath.Join(s.Root, "current")) != "1.5.0" || readPointer(filepath.Join(s.Root, "watermark")) != "1.5.0" {
		t.Fatal("pointers not written")
	}
	if got, _ := os.ReadFile(nodePath); !bytes.Equal(got, nodeBytes) {
		t.Fatal("node.json must be kept untouched")
	}
	cfg, found, err := config.LoadFrom(s.ConfigPath)
	if err != nil || !found || cfg.Pipeline.Instances != 2 {
		t.Fatalf("server.toml [pipeline] instances: %+v %v %v", cfg.Pipeline, found, err)
	}
	all := strings.Join(rec.calls, "\n")
	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(all, "schtasks /Create /F /TN Maktaba /SC ONLOGON") ||
			!strings.Contains(all, "schtasks /Delete /F /TN maktaba-node") {
			t.Fatalf("calls:\n%s", all)
		}
		if strings.Index(all, "/Create") > strings.Index(all, "/Delete") {
			t.Fatal("the old worker is removed only after the new service is registered and healthy")
		}
		script, _ := os.ReadFile(filepath.Join(s.Root, "run-maktaba.cmd"))
		if !strings.Contains(string(script), "maktaba-server.exe\" serve") || !strings.Contains(string(script), "MAKTABA_PYTHON=/opt/worker/python") {
			t.Fatalf("run-maktaba.cmd:\n%s", script)
		}
	case "linux":
		if !strings.Contains(all, "systemctl --user enable --now maktaba-server.service") {
			t.Fatalf("calls:\n%s", all)
		}
	}
	if strings.Contains(s.Out.(*bytes.Buffer).String(), "secret") {
		t.Fatal("a credential was printed")
	}
}

// A tampered binary is refused before anything is installed.
func TestSetupNodeRefusesTamperedRelease(t *testing.T) {
	ss, rel, from := fakeRelease(t)
	for name := range rel.Binaries {
		if strings.HasPrefix(name, "maktaba-api") {
			if err := os.WriteFile(filepath.Join(from, name), []byte("evil"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	rec := &recorder{}
	s, _, _ := newTestSetup(t, ss, from, rec)
	if err := s.run(context.Background()); err == nil {
		t.Fatal("a tampered release must be refused")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "current")); err == nil {
		t.Fatal("nothing may be installed")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("no service change: %v", rec.calls)
	}
}

// EC2: a node joined before handshake v2 must re-join.
func TestSetupNodeNeedsClusterKey(t *testing.T) {
	ss, _, from := fakeRelease(t)
	s, _, _ := newTestSetup(t, ss, from, &recorder{})
	s.NodeConfig.Home.ClusterPub = ""
	if err := s.run(context.Background()); !errors.Is(err, ErrRejoin) {
		t.Fatalf("err = %v", err)
	}
}

// --dry-run changes nothing.
func TestSetupNodeDryRun(t *testing.T) {
	ss, _, from := fakeRelease(t)
	rec := &recorder{}
	s, _, _ := newTestSetup(t, ss, from, rec)
	s.DryRun = true
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Root); err == nil || len(rec.calls) != 0 {
		t.Fatal("--dry-run changed something")
	}
	if !strings.Contains(s.Out.(*bytes.Buffer).String(), "would verify") {
		t.Fatal(s.Out)
	}
}
