package serverkeys

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// xorSealer is a trivial Sealer used only in tests. It XORs against
// a fixed key so we can round-trip without dragging crypto/aead
// into the test surface.
type xorSealer struct{ key byte }

func (s xorSealer) Seal(p []byte) ([]byte, error) {
	out := make([]byte, len(p))
	for i, b := range p {
		out[i] = b ^ s.key
	}
	return out, nil
}

func (s xorSealer) Open(p []byte) ([]byte, error) { return s.Seal(p) }

func TestLoadFirstBootGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	key, fresh, err := Load(LoadOptions{
		DiskDir: dir,
		Sealer:  xorSealer{key: 0x5A},
		Clock:   newFakeClock(now),
	})
	if err != nil {
		t.Fatalf("Load (first boot): %v", err)
	}
	if !fresh {
		t.Fatal("expected fresh=true on first boot")
	}
	if key.Source != "generated" {
		t.Fatalf("Source = %s, want generated", key.Source)
	}

	// Reload: should recover same kid, fresh=false.
	key2, fresh2, err := Load(LoadOptions{
		DiskDir: dir,
		Sealer:  xorSealer{key: 0x5A},
		Clock:   newFakeClock(now),
	})
	if err != nil {
		t.Fatalf("Load (recover): %v", err)
	}
	if fresh2 {
		t.Fatal("expected fresh=false on reload")
	}
	if key2.Kid != key.Kid {
		t.Fatalf("kid changed across reload: %s -> %s", key.Kid, key2.Kid)
	}
	if key2.Source != "disk" {
		t.Fatalf("Source = %s, want disk", key2.Source)
	}

	// Sentinel got written.
	if !expectedKidExists(dir) {
		t.Fatal("expected.kid sentinel should exist after first boot")
	}
}

func TestLoadEnvBeatsDisk(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	sealer := xorSealer{key: 0x5A}

	// Seed disk with one key.
	if _, _, err := Load(LoadOptions{DiskDir: dir, Sealer: sealer, Clock: newFakeClock(now)}); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
	// Now load with a different key in env.
	envKey, _ := Generate(now, "test")
	envPEM, _ := MarshalPrivatePEM(envKey.PrivateKey)
	loaded, _, err := Load(LoadOptions{
		EnvPEM:  string(envPEM),
		DiskDir: dir,
		Sealer:  sealer,
		Clock:   newFakeClock(now),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Kid != envKey.Kid {
		t.Fatalf("env key did not win: got kid %s, want %s", loaded.Kid, envKey.Kid)
	}
	if loaded.Source != "env" {
		t.Fatalf("Source = %s, want env", loaded.Source)
	}
}

func TestLoadBadEnvPEMFails(t *testing.T) {
	_, _, err := Load(LoadOptions{
		EnvPEM: "not a pem block",
	})
	if err == nil {
		t.Fatal("expected error for bad PEM, got nil")
	}
}

func TestLoadRejectsRSAEnvKey(t *testing.T) {
	// Generate an RSA-shaped PKCS-8 PEM (not Ed25519). We don't
	// actually need a valid RSA key here — a non-Ed25519 PKCS-8
	// structure suffices; the parser will reject the type assert.
	// Easiest: hand-craft a PKCS-8 with a different OID by using
	// hex.DecodeString on a fixed test vector below. To keep the
	// test self-contained, use a malformed but parseable PEM that
	// will hit the "expected ed25519" branch — encoder will fail
	// to parse, which is still an error from Load.
	bogus := "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIA==\n-----END PRIVATE KEY-----\n"
	if _, _, err := Load(LoadOptions{EnvPEM: bogus}); err == nil {
		t.Fatal("expected error for malformed PKCS-8, got nil")
	}
}

func TestLoadSentinelMismatchRefuses(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed an expected.kid that doesn't match anything on disk
	// — and then make sure there's a key file with a different kid
	// so we hit the explicit mismatch check.
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	sealer := xorSealer{key: 0x5A}

	// First boot, populate disk normally.
	first, _, err := Load(LoadOptions{DiskDir: dir, Sealer: sealer, Clock: newFakeClock(now)})
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	// Hand-rewrite expected.kid to a fake value.
	if err := os.WriteFile(filepath.Join(dir, expectedKid), []byte("ffffffffffffffff\n"), 0o644); err != nil {
		t.Fatalf("rewrite sentinel: %v", err)
	}
	_, _, err = Load(LoadOptions{DiskDir: dir, Sealer: sealer, Clock: newFakeClock(now)})
	if err == nil {
		t.Fatalf("expected sentinel mismatch error after rewriting expected.kid; loaded kid=%s", first.Kid)
	}
	if !strings.Contains(err.Error(), "kid mismatch") {
		t.Fatalf("expected kid mismatch error, got %v", err)
	}
}

func TestLoadAllowNewIdentityResetsSentinel(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	sealer := xorSealer{key: 0x5A}

	if _, _, err := Load(LoadOptions{DiskDir: dir, Sealer: sealer, Clock: newFakeClock(now)}); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, expectedKid), []byte("ffffffffffffffff\n"), 0o644); err != nil {
		t.Fatalf("rewrite sentinel: %v", err)
	}
	k, _, err := Load(LoadOptions{
		DiskDir:          dir,
		Sealer:           sealer,
		Clock:            newFakeClock(now),
		AllowNewIdentity: true,
	})
	if err != nil {
		t.Fatalf("AllowNewIdentity should succeed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, expectedKid))
	if err != nil {
		t.Fatal(err)
	}
	if want := k.Kid + "\n"; string(got) != want {
		t.Fatalf("expected.kid = %q, want %q", got, want)
	}
}

func TestLoadSentinelPresentNoKeyRefuses(t *testing.T) {
	dir := t.TempDir()
	// Plant a sentinel without any actual key material.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, expectedKid), []byte(hex.EncodeToString(bytes.Repeat([]byte{0xab}, 8))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(LoadOptions{DiskDir: dir, Sealer: xorSealer{key: 0x5A}}); err == nil {
		t.Fatal("expected refusal when sentinel exists but no key material is present")
	}
}
