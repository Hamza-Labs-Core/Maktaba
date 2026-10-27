package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCheckTargetRefuses(t *testing.T) {
	base := realTemp(t)
	cases := []struct {
		name, target string
		roots        []string
		code         string
	}{
		{"empty", "  ", nil, "target_relative"},
		{"relative", "Big/Maktaba", nil, "target_relative"},
		{"dot dot", base + "/Big/../../etc", nil, "target_relative"},
		{"dot dot at the end", base + "/..", nil, "target_relative"},
		{"the top of the disk", "/", nil, "target_system"},
		{"a system folder", "/usr/local/maktaba", nil, "target_system"},
		{"a system folder itself", "/etc", nil, "target_system"},
		{"not on a listed disk", filepath.Join(base, "x"), []string{"/Volumes/Big"}, "target_not_on_disk"},
	}
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CheckTarget(c.target, c.roots)
			var te *TargetError
			if !errors.As(err, &te) || te.Note.Code != c.code {
				t.Fatalf("CheckTarget(%q) = %q, %v; want %s", c.target, got, err, c.code)
			}
		})
	}
}

func TestCheckTargetCleansAndResolves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links")
	}
	base := realTemp(t)
	disk := filepath.Join(base, "disk")
	if err := os.MkdirAll(disk, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(disk, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	got, err := CheckTarget(" "+base+"//link/./Maktaba/ ", []string{base})
	if err != nil || got != filepath.Join(disk, "Maktaba") {
		t.Fatalf("got %q, %v", got, err)
	}
	// a plan takes only the checked path
	in, _ := macInstall(t)
	fakeDisk(t, 100<<30, "apfs", true)
	if p := PlanMoveOn(in, filepath.Join(base, "link", "M"), []string{"/nowhere"}); p.OK || p.Problems[0].Code != "target_not_on_disk" {
		t.Errorf("plan %+v", p.Problems)
	}
	if p := PlanMove(in, filepath.Join(base, "link", "M")); !p.OK || p.Target != filepath.Join(disk, "M") {
		t.Errorf("plan %q %+v", p.Target, p.Problems)
	}
}
