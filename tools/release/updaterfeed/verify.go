package updaterfeed

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// Required are the bare platform keys a desktop release must have before it can be the
// latest (one per desktop leg of desktop-release.yml): an app reads the newest release's
// feed, and a release whose feed lacks its platform strands it (v0.3.1 had no
// darwin-aarch64: the Apple-silicon Mac home could take no update).
// No Intel Mac (darwin-x86_64) since v0.3.15: Macs are Apple silicon only.
var Required = []string{"darwin-aarch64", "windows-x86_64", "linux-x86_64", "linux-aarch64"}

// ServerApps are the platforms whose app carries the server (every desktop platform now
// that the Intel Mac's client is gone): a release is published (no longer a draft) only once their bundles are in its
// feed, since a home or a machine on them updates from it.
var ServerApps = []string{"darwin-aarch64", "windows-x86_64", "linux-x86_64", "linux-aarch64"}

// Wanted is the platform keys a --require names: "desktop" (Required), "server-apps"
// (ServerApps), or a comma list.
func Wanted(require string) []string {
	switch r := strings.TrimSpace(require); r {
	case "":
		return nil
	case "desktop":
		return Required
	case "server-apps":
		return ServerApps
	default:
		var want []string
		for _, k := range strings.Split(r, ",") {
			if k = strings.TrimSpace(k); k != "" {
				want = append(want, k)
			}
		}
		return want
	}
}

// Missing is the keys of want the feed lacks, sorted.
func Missing(f Feed, want []string) []string {
	var out []string
	for _, k := range want {
		if _, ok := f.Platforms[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// pubKey is a minisign public key: its key id and Ed25519 key.
type pubKey struct {
	id  [8]byte
	key ed25519.PublicKey
}

// parsePubKey reads the updater's public key as tauri.conf.json carries it (the
// repository variable TAURI_UPDATER_PUBKEY): base64 of a minisign public key file
// ("untrusted comment: …" then base64 of "Ed", the key id and the key), or that file's
// key line alone.
func parsePubKey(s string) (pubKey, error) {
	var pk pubKey
	text := strings.TrimSpace(s)
	// the whole key file (its "untrusted comment:" line first); a key line alone also decodes,
	// to 42 bytes that may hold a newline by chance, and is read as itself
	if dec, err := base64.StdEncoding.DecodeString(text); err == nil &&
		bytes.HasPrefix(bytes.TrimSpace(dec), []byte("untrusted comment:")) {
		text = string(dec)
	}
	line := lastKeyLine(text)
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) != 2+8+32 || string(raw[:2]) != "Ed" {
		return pk, errors.New("not a minisign public key (base64 of \"Ed\", an 8-byte key id and a 32-byte key)")
	}
	copy(pk.id[:], raw[2:10])
	pk.key = ed25519.PublicKey(raw[10:])
	return pk, nil
}

// lastKeyLine is the last line that is not a comment.
func lastKeyLine(text string) string {
	line := ""
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "untrusted comment:") {
			line = l
		}
	}
	return line
}

// Verify checks a bundle's signature (the .sig tauri wrote: base64 of a minisign
// signature file) as the app's updater does (tauri-plugin-updater 2.14,
// verify_signature): signed by key over the file (prehashed with BLAKE2b-512 for "ED"),
// its trusted comment signed too, and naming version (requireSignedVersion).
func Verify(file []byte, sig, pubkey, version string) error {
	pk, err := parsePubKey(pubkey)
	if err != nil {
		return err
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil {
		return fmt.Errorf("the signature is not base64: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(string(dec), "\r", ""), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "untrusted comment:") ||
		!strings.HasPrefix(lines[2], "trusted comment: ") {
		return errors.New("not a minisign signature (four lines: two comments, the signature, the global signature)")
	}
	raw, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil || len(raw) != 2+8+64 {
		return errors.New("the signature line is not an algorithm, a key id and an Ed25519 signature")
	}
	alg, id, s := string(raw[:2]), raw[2:10], raw[10:]
	if !bytes.Equal(id, pk.id[:]) {
		return fmt.Errorf("signed by key %X, not the app's %X", id, pk.id[:])
	}
	msg := file
	switch alg {
	case "ED":
		h := blake2b.Sum512(file)
		msg = h[:]
	case "Ed":
	default:
		return fmt.Errorf("unknown signature algorithm %q", alg)
	}
	if !ed25519.Verify(pk.key, msg, s) {
		return errors.New("the signature does not match the file")
	}
	trusted := strings.TrimPrefix(lines[2], "trusted comment: ")
	global, err := base64.StdEncoding.DecodeString(lines[3])
	if err != nil || len(global) != ed25519.SignatureSize {
		return errors.New("the global signature is not an Ed25519 signature")
	}
	if !ed25519.Verify(pk.key, append(append([]byte{}, s...), trusted...), global) {
		return errors.New("the trusted comment's signature does not match")
	}
	signed := ""
	for _, field := range strings.Split(trusted, "\t") {
		if v, ok := strings.CutPrefix(field, "version:"); ok {
			signed = v
		}
	}
	if signed == "" {
		return errors.New("the signature names no version (the apps require one)")
	}
	if strings.TrimPrefix(signed, "v") != strings.TrimPrefix(version, "v") {
		return fmt.Errorf("signed for version %s, not %s", signed, version)
	}
	return nil
}

// VerifyDir checks every signed bundle in dir against pubkey (Verify), for version.
func VerifyDir(dir, pubkey, version string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	n := 0
	for _, e := range ents {
		bundle, ok := strings.CutSuffix(e.Name(), ".sig")
		if !ok || !e.Type().IsRegular() {
			continue
		}
		file, err := os.ReadFile(filepath.Join(dir, bundle))
		if err != nil {
			return err
		}
		sig, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if err := Verify(file, string(sig), pubkey, version); err != nil {
			return fmt.Errorf("%s: %v", bundle, err)
		}
		n++
	}
	if n == 0 {
		return errors.New("no signed bundle to check")
	}
	return nil
}
