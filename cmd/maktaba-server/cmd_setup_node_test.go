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
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/service"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
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
		Healthy: func(context.Context) error { healthy++; return nil },
		InstallService: func(sp service.Spec) error {
			rec.calls = append(rec.calls, "install "+string(sp.Kind)+" "+sp.Root)
			return nil
		}}
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
	if !strings.Contains(all, "install "+string(service.DefaultKind(runtime.GOOS, layout.ModeUser))+" "+s.Root) {
		t.Fatalf("the launcher's service is installed as setup --service writes it:\n%s", all)
	}
	if _, err := os.Stat(filepath.Join(s.Root, exeName("maktaba-launcher"))); err != nil {
		t.Fatal("the launcher goes to the install root")
	}
	if cfg.Pipeline.Python != "/opt/worker/python" {
		t.Fatalf("the worker's Python is kept in server.toml: %q", cfg.Pipeline.Python)
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(all, "schtasks /Run /TN Maktaba") || !strings.Contains(all, "schtasks /Delete /F /TN maktaba-node") {
			t.Fatalf("calls:\n%s", all)
		}
		if strings.Index(all, "install ") > strings.Index(all, "/Delete") {
			t.Fatal("the old worker is removed only after the new service is registered and healthy")
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

// Story 38.9: a Windows x86_64 machine whose GPU runs the second listen installs the
// release's cuda pin set (torch's CUDA build); every other machine the default one.
func TestSetupNodeChoosesTorchsBuild(t *testing.T) {
	p := &releaseverify.Pipeline{Constraints: "c.txt", ConstraintsCUDA: "c-cuda.txt"}
	for _, c := range []struct {
		platform string
		gpu      bool
		want     string
	}{
		{"windows/amd64", true, "c-cuda.txt"},
		{"windows/amd64", false, "c.txt"},
		{"linux/amd64", true, "c.txt"}, // PyPI's Linux torch has CUDA already
		{"darwin/arm64", true, "c.txt"},
	} {
		s := &nodeSetup{Platform: c.platform, CUDATorch: func() bool { return c.gpu }}
		if got := s.constraints(p); got != c.want {
			t.Errorf("%s, GPU runs the second listen %v: %s, want %s", c.platform, c.gpu, got, c.want)
		}
	}
	old := &releaseverify.Pipeline{Constraints: "c.txt"} // a release before the cuda set
	s := &nodeSetup{Platform: "windows/amd64", CUDATorch: func() bool { return true }}
	if got := s.constraints(old); got != "c.txt" {
		t.Errorf("a release without the cuda set: %s", got)
	}
	if !p.IsConstraints("c-cuda.txt") || !p.IsConstraints("c.txt") || p.IsConstraints("x.whl") {
		t.Error("IsConstraints")
	}
}
