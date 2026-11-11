package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

func nasMachine(platform string, limit uint64) func() hostinfo.Machine {
	return func() hostinfo.Machine {
		return hostinfo.Machine{Platform: platform, Arch: "amd64", Memory: hostinfo.Memory{LimitBytes: limit}}
	}
}

// T07 of story 59.13: a Light NAS joining as a node is warned and needs the explicit
// choice; a Standard NAS, a Light computer that is no NAS and the rollout's conversion
// are not stopped; the admin's tier wins over the detected one.
func TestSetupNodeOnALightNAS(t *testing.T) {
	ss, _, from := fakeRelease(t)
	rec := &recorder{}
	s, _, _ := newTestSetup(t, ss, from, rec)
	s.Machine = nasMachine("dsm", 4*hostinfo.GiB)
	err := s.run(context.Background())
	if !errors.Is(err, ErrLightNAS) {
		t.Fatalf("a Light NAS joined without the choice: %v", err)
	}
	if !strings.Contains(err.Error(), "--light-nas") || len(rec.calls) != 0 {
		t.Fatalf("the warning names the choice and nothing ran: %v %v", err, rec.calls)
	}
	s.LightNAS = true
	if err := s.run(context.Background()); err != nil {
		t.Fatalf("with --light-nas: %v\n%s", err, s.Out)
	}
	if out := s.Out.(*bytes.Buffer).String(); !strings.Contains(out, LightNASText) {
		t.Fatalf("the choice is said: %s", out)
	}

	for _, c := range []struct {
		name string
		m    func() hostinfo.Machine
		toml string
		conv bool
	}{
		{"standard NAS", nasMachine("truenas", 12*hostinfo.GiB), "", false},
		{"light computer", nasMachine("linux", 4*hostinfo.GiB), "", false},
		{"rollout conversion", nasMachine("dsm", 4*hostinfo.GiB), "", true},
		{"admin's standard", nasMachine("unraid", 4*hostinfo.GiB), "[server]\ntier = \"standard\"\ntier_chosen = \"admin\"\n", false},
	} {
		s := &nodeSetup{ConfigPath: filepath.Join(t.TempDir(), "server.toml"), Machine: c.m, Converting: c.conv, Out: &bytes.Buffer{}}
		if c.toml != "" {
			if err := os.WriteFile(s.ConfigPath, []byte(c.toml), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.checkLightNAS(); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}
