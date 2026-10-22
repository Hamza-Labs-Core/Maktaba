package main

import (
	"os"
	"path/filepath"
	"testing"
)

// uninstall --purge on Windows removes both places: the new server.toml and keys\,
// and the originals of an install from before Epic 38 that the first command copied
// (or the next install's first command would copy them back). The node config and
// the node installer's files stay, as on every OS. Temp dirs only.
func TestPurgeConfigBothPlaces(t *testing.T) {
	base := t.TempDir()
	root, roaming := filepath.Join(base, "Local", "Maktaba"), filepath.Join(base, "Roaming", "Maktaba")
	write := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{
		filepath.Join(root, "server.toml"), filepath.Join(root, "keys", "secrets.key"),
		filepath.Join(roaming, "server.toml"), filepath.Join(roaming, "secrets.key"),
		filepath.Join(roaming, "stream-signing.key"), filepath.Join(roaming, "postgres-maktaba.password"),
		filepath.Join(roaming, "node.json"), filepath.Join(roaming, "node.env"),
		filepath.Join(root, "paths-move.json"),
	} {
		write(p)
	}
	originals := []string{
		filepath.Join(roaming, "server.toml"), filepath.Join(roaming, "secrets.key"),
		filepath.Join(roaming, "stream-signing.key"), filepath.Join(roaming, "postgres-maktaba.password"),
		filepath.Join(root, "paths-move.lock"), filepath.Join(root, "paths-move.json"),
	}
	purgeConfig(filepath.Join(root, "server.toml"), filepath.Join(root, "keys"), originals)
	for _, gone := range append(originals, filepath.Join(root, "server.toml"), filepath.Join(root, "keys")) {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s must be purged", gone)
		}
	}
	for _, kept := range []string{filepath.Join(roaming, "node.json"), filepath.Join(roaming, "node.env")} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s must stay (the machine's identity, the node installer's settings): %v", kept, err)
		}
	}
}
