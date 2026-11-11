package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// Story 59.13: on a NAS home the libraries step's answer carries the share step's
// question: each root's share by the platform's layout (or none: the screens ask), the
// share's folder, whether it is the NAS's own path, and the names the NAS goes by.
func TestShareProposals(t *testing.T) {
	old := hostNames
	hostNames = func() []string { return []string{"diskstation", "diskstation.local", "192.168.1.20"} }
	t.Cleanup(func() { hostNames = old })

	st := shareProposals("dsm", []string{"/volume1/Movies/en", "/media/TV"})
	if st == nil || st.Platform != "dsm" || len(st.Servers) != 3 || len(st.Roots) != 2 {
		t.Fatalf("dsm: %+v", st)
	}
	if r := st.Roots[0]; r.Share != "Movies" || r.Folder != "/volume1/Movies" || !r.HostPath {
		t.Fatalf("dsm volume: %+v", r)
	}
	if r := st.Roots[1]; r.Share != "" || r.Folder != "/media/TV" || r.HostPath {
		t.Fatalf("catch-all: %+v", r)
	}
	if st := shareProposals("truenas", []string{"/mnt/tank/media"}); st == nil || st.Roots[0].Share != "" || !st.Roots[0].HostPath {
		t.Fatalf("truenas asks: %+v", st)
	}
	if st := shareProposals("docker", []string{"/srv/media"}); st == nil {
		t.Fatal("a container on another Linux names its shares too")
	}
	for _, p := range []string{"darwin", "windows", "linux", ""} {
		if st := shareProposals(p, []string{"/x"}); st != nil {
			t.Errorf("%q: no share step off a NAS: %+v", p, st)
		}
	}
}

func TestStepSharesKeepsTheAnswerForFinish(t *testing.T) {
	e := newSetupEnv(t)
	cfg := config.Defaults()
	cfg.Server.DataDir = e.state
	s := &setupRun{ctx: context.Background(), cfg: cfg, out: json.NewEncoder(io.Discard), log: io.Discard}
	for _, bad := range []string{
		`{"server":"","shares":[]}`,
		`{"server":"a/b","shares":[]}`,
		`{"server":"nas","shares":[{"path":"relative","share":"x"}]}`,
		`{"server":"nas","shares":[{"path":"/a","share":"x/y"}]}`,
	} {
		if _, err := stepShares(s, json.RawMessage(bad)); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
	if _, err := stepShares(s, json.RawMessage(`{"server":"DiskStation.local","aliases":["diskstation","192.168.1.20","192.168.1.20"],
		"shares":[{"path":"/volume1/Movies/","share":"Movies"},{"path":"/volume1/Private","share":""}]}`)); err != nil {
		t.Fatal(err)
	}
	p, err := s.loadPending()
	if err != nil {
		t.Fatal(err)
	}
	if p.ShareServer != "diskstation" || len(p.ShareAliases) != 1 || p.ShareAliases[0] != "192.168.1.20" ||
		p.Shares["/volume1/Movies"] != "Movies" || p.Shares["/volume1/Private"] != "" || len(p.Shares) != 2 {
		t.Fatalf("pending %+v", p)
	}
	// finish writes them to [media] (round trip through server.toml)
	cfg.Media.Shares, cfg.Media.ShareServer, cfg.Media.ShareAliases = p.Shares, p.ShareServer, p.ShareAliases
	path := filepath.Join(t.TempDir(), "server.toml")
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	back, _, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Media.Shares["/volume1/Private"] != "" || back.Media.Shares["/volume1/Movies"] != "Movies" || back.Media.ShareServer != "diskstation" {
		t.Fatalf("server.toml %+v", back.Media)
	}
	raw, _ := os.ReadFile(path)
	if len(raw) == 0 {
		t.Fatal("empty file")
	}
}

// the libraries step on a NAS carries the question
func TestLibrariesStepCarriesTheShareQuestion(t *testing.T) {
	e := newSetupEnv(t)
	cfg := config.Defaults()
	cfg.Server.DataDir = e.state
	s := &setupRun{ctx: context.Background(), cfg: cfg, out: json.NewEncoder(io.Discard), log: io.Discard}
	old := hostMachine
	hostMachine = func() hostinfo.Machine { return hostinfo.Machine{Platform: hostinfo.PlatformTrueNAS} }
	t.Cleanup(func() { hostMachine = old })
	root := filepath.Join(e.dir, "media")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := stepLibraries(s, json.RawMessage(`{"roots":["`+filepath.ToSlash(root)+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	st, ok := out.(map[string]any)["shares"].(*shareStep)
	if !ok || st.Platform != "truenas" || len(st.Roots) != 1 {
		t.Fatalf("answer %+v", out)
	}
	hostMachine = func() hostinfo.Machine { return hostinfo.Machine{Platform: "darwin"} }
	out, _ = stepLibraries(s, json.RawMessage(`{"roots":["`+filepath.ToSlash(root)+`"]}`))
	if _, ok := out.(map[string]any)["shares"]; ok {
		t.Fatal("no share step off a NAS")
	}
}
