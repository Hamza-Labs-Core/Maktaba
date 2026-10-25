package updaterfeed

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/blake2b"
)

// minisignKey is a key pair as tauri's signer makes them: the public key as the apps carry
// it (TAURI_UPDATER_PUBKEY: base64 of the minisign public key file).
type minisignKey struct {
	id   [8]byte
	priv ed25519.PrivateKey
	pub  string
}

func newKey(t *testing.T) minisignKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var k minisignKey
	_, _ = rand.Read(k.id[:])
	k.priv = priv
	line := base64.StdEncoding.EncodeToString(append(append([]byte("Ed"), k.id[:]...), pub...))
	k.pub = base64.StdEncoding.EncodeToString([]byte("untrusted comment: minisign public key: X\n" + line + "\n"))
	return k
}

// sign is a .sig as `tauri signer sign` writes it (prehashed: "ED"), with trusted comment.
func (k minisignKey) sign(file []byte, trusted string) string {
	h := blake2b.Sum512(file)
	s := ed25519.Sign(k.priv, h[:])
	line := base64.StdEncoding.EncodeToString(append(append([]byte("ED"), k.id[:]...), s...))
	global := ed25519.Sign(k.priv, append(append([]byte{}, s...), trusted...))
	text := "untrusted comment: signature from tauri secret key\n" + line + "\ntrusted comment: " + trusted + "\n" +
		base64.StdEncoding.EncodeToString(global) + "\n"
	return base64.StdEncoding.EncodeToString([]byte(text))
}

// A signature checks as the apps check it: their key, the file, the signed trusted comment
// and the release's version in it; anything else is refused.
func TestVerify(t *testing.T) {
	k, other := newKey(t), newKey(t)
	file := []byte("the app bundle")
	good := k.sign(file, "timestamp:1792735800\tfile:Maktaba_0.3.2_aarch64.app.tar.gz\tversion:0.3.2")
	if err := Verify(file, good, k.pub, "v0.3.2"); err != nil {
		t.Fatalf("a good signature: %v", err)
	}
	for name, err := range map[string]error{
		"another key":         Verify(file, good, other.pub, "0.3.2"),
		"another file":        Verify([]byte("tampered"), good, k.pub, "0.3.2"),
		"another version":     Verify(file, good, k.pub, "0.3.1"),
		"no version":          Verify(file, k.sign(file, "timestamp:1\tfile:x"), k.pub, "0.3.2"),
		"not base64":          Verify(file, "%%%", k.pub, "0.3.2"),
		"not a key":           Verify(file, good, "nope", "0.3.2"),
		"the comment changed": Verify(file, tamperComment(t, good), k.pub, "0.3.2"),
	} {
		if err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	// the key's line alone (no comment) is read too
	raw, _ := base64.StdEncoding.DecodeString(k.pub)
	if err := Verify(file, good, lastKeyLine(string(raw)), "0.3.2"); err != nil {
		t.Errorf("the key line alone: %v", err)
	}
}

// tamperComment changes the trusted comment and keeps its old global signature.
func tamperComment(t *testing.T, sig string) string {
	t.Helper()
	dec, _ := base64.StdEncoding.DecodeString(sig)
	return base64.StdEncoding.EncodeToString([]byte(strings.Replace(string(dec), "version:0.3.2", "version:0.3.2\tx:y", 1)))
}

// The feed is written only when every signature checks against the apps' key, and a feed
// that lacks a desktop platform says so (the release must not become the latest).
func TestMainChecksAndRequires(t *testing.T) {
	k, other := newKey(t), newKey(t)
	files := map[string]string{}
	for name, body := range map[string]string{
		"Maktaba_0.3.2_aarch64.app.tar.gz": "mac-arm", "Maktaba_0.3.2_x64.app.tar.gz": "mac-intel",
		"Maktaba_0.3.2_x64-setup.exe": "nsis", "Maktaba_0.3.2_amd64.AppImage": "ai-x86",
		"Maktaba_0.3.2_aarch64.AppImage": "ai-arm",
	} {
		files[name] = body
		files[name+".sig"] = k.sign([]byte(body), "timestamp:1\tfile:"+name+"\tversion:0.3.2")
	}
	dir := stage(t, files)
	out := filepath.Join(t.TempDir(), "latest.json")
	var so, se bytes.Buffer
	args := []string{"--dir", dir, "--repo", "o/r", "--tag", "v0.3.2", "--out", out, "--require", "desktop"}
	if code := Main(append(args, "--pubkey", k.pub), &so, &se, now); code != 0 {
		t.Fatalf("every leg, good signatures: exit %d: %s", code, se.String())
	}
	_ = os.Remove(out)
	se.Reset()
	if code := Main(append(args, "--pubkey", other.pub), &so, &se, now); code != 1 || !strings.Contains(se.String(), "refuse") {
		t.Fatalf("signed by another key: exit %d: %s", code, se.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("a feed written with a signature the apps would refuse")
	}
	// the Apple-silicon leg failed (v0.3.1): written, but incomplete
	delete(files, "Maktaba_0.3.2_aarch64.app.tar.gz")
	delete(files, "Maktaba_0.3.2_aarch64.app.tar.gz.sig")
	se.Reset()
	if code := Main(append([]string{"--dir", stage(t, files)}, args[2:]...), &so, &se, now); code != ExitIncomplete ||
		!strings.Contains(se.String(), "darwin-aarch64") {
		t.Fatalf("a missing leg: exit %d: %s", code, se.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal("an incomplete feed is still written (a rerun of the failed leg completes it)")
	}
}

// A release's feed is checked for the platforms a step needs: the server-carrying apps
// before the release is published, every desktop platform before it is the latest.
func TestCheckFeed(t *testing.T) {
	dir := t.TempDir()
	feed := filepath.Join(dir, "latest.json")
	write := func(keys ...string) {
		f := Feed{Version: "0.3.2", Platforms: map[string]Platform{}}
		for _, k := range keys {
			f.Platforms[k] = Platform{Signature: "s", URL: "u"}
		}
		b, _ := json.Marshal(f)
		if err := os.WriteFile(feed, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(require string) int {
		var out, errb bytes.Buffer
		return Main([]string{"--check", feed, "--require", require}, &out, &errb, time.Now())
	}
	write("darwin-aarch64", "windows-x86_64", "linux-x86_64", "linux-aarch64")
	if got := run("server-apps"); got != 0 {
		t.Fatalf("server apps all there: %d", got)
	}
	// no Intel Mac build any more: a desktop release is whole without darwin-x86_64
	if got := run("desktop"); got != 0 {
		t.Fatalf("every desktop platform there: %d", got)
	}
	write("darwin-x86_64", "windows-x86_64", "linux-x86_64", "linux-aarch64")
	if got := run("server-apps"); got != ExitIncomplete {
		t.Fatalf("no Apple-silicon app: %d", got)
	}
	if got := Main([]string{"--check", filepath.Join(dir, "none.json"), "--require", "desktop"}, io.Discard, io.Discard, time.Now()); got != 1 {
		t.Fatalf("no feed: %d", got)
	}
}

// --verify checks a release's downloaded app packages as the apps would (the package
// repositories publish a Linux app's .deb or .rpm only once it checks): their key, their
// release's version; nothing signed, or a usage error, is no pass.
func TestMainVerify(t *testing.T) {
	k, other := newKey(t), newKey(t)
	files := map[string]string{}
	for name, body := range map[string]string{"Maktaba_0.3.6_amd64.deb": "deb-x86", "Maktaba-0.3.6-1.aarch64.rpm": "rpm-arm"} {
		files[name] = body
		files[name+".sig"] = k.sign([]byte(body), "timestamp:1	file:"+name+"	version:0.3.6")
	}
	dir := stage(t, files)
	run := func(args ...string) (int, string) {
		var so, se bytes.Buffer
		code := Main(append([]string{"--verify"}, args...), &so, &se, now)
		return code, se.String()
	}
	if code, se := run("--dir", dir, "--pubkey", k.pub, "--tag", "v0.3.6"); code != 0 {
		t.Fatalf("the apps' key: exit %d: %s", code, se)
	}
	if code, se := run("--dir", dir, "--pubkey", other.pub, "--tag", "v0.3.6"); code != 1 || !strings.Contains(se, "refuse") {
		t.Fatalf("another key: exit %d: %s", code, se)
	}
	if code, _ := run("--dir", dir, "--pubkey", k.pub, "--tag", "v0.3.7"); code != 1 {
		t.Fatalf("another release's version: exit %d", code)
	}
	if code, _ := run("--dir", stage(t, map[string]string{"Maktaba_0.3.6_amd64.deb": "deb-x86"}), "--pubkey", k.pub, "--tag", "v0.3.6"); code != 1 {
		t.Fatalf("nothing signed: exit %d", code)
	}
	if code, _ := run("--dir", dir, "--tag", "v0.3.6"); code != 2 {
		t.Fatalf("no key: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "latest.json")); !os.IsNotExist(err) {
		t.Fatal("--verify wrote a feed")
	}
}
