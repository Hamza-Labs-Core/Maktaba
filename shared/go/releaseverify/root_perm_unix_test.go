//go:build unix

package releaseverify_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A trusted-root cache another user could write is never a trust anchor: a
// planted trusted_root.json would name the attacker's own CA. (The concrete
// case: `sudo maktaba-server update` with the invoking user's $HOME.)
func TestRootStoreRefusesWritableCacheDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode checks are what is tested; root owns everything")
	}
	s, dir, _ := refreshedStore(t)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, info, err := s.TrustedMaterial(); err != nil || info.Source != "embedded" {
		t.Fatalf("world-writable cache dir used: %+v, %v", info, err)
	}
	// Refresh tightens its own directory (a umask-002 mkdir) and goes on.
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh did not repair its own cache dir: %v", err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm()&0o022 != 0 {
		t.Fatalf("cache dir mode after refresh: %v, %v", fi.Mode(), err)
	}
}

func TestRootStoreRefusesWritableAncestor(t *testing.T) {
	s, dir, _ := refreshedStore(t)
	parent := filepath.Dir(dir)
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	if _, info, err := s.TrustedMaterial(); err != nil || info.Source != "embedded" {
		t.Fatalf("cache under a world-writable parent used: %+v, %v", info, err)
	}
	if err := s.Refresh(context.Background()); err == nil {
		t.Fatal("refresh into a cache under a world-writable parent succeeded")
	}
}

func TestRootStoreRefusesWritableOrLinkedFiles(t *testing.T) {
	s, dir, _ := refreshedStore(t)
	rootFile := filepath.Join(dir, "trusted_root.json")
	if err := os.Chmod(rootFile, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, info, err := s.TrustedMaterial(); err != nil || info.Source != "embedded" {
		t.Fatalf("group/world-writable trusted_root.json used: %+v, %v", info, err)
	}
	if err := os.Chmod(rootFile, 0o644); err != nil {
		t.Fatal(err)
	}

	// A symlink to a file elsewhere is not the cache's own file.
	elsewhere := filepath.Join(t.TempDir(), "planted.json")
	b, err := os.ReadFile(rootFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(elsewhere, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(rootFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, rootFile); err != nil {
		t.Fatal(err)
	}
	if _, info, err := s.TrustedMaterial(); err != nil || info.Source != "embedded" {
		t.Fatalf("symlinked trusted_root.json used: %+v, %v", info, err)
	}
}
