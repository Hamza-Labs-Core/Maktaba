package firstparty

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

func toy(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/toy.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTheBundleIsReadWithItsTrustedTable(t *testing.T) {
	restore := UseBundle(toy(t))
	defer restore()
	l, err := Plugins()
	if err != nil || len(l) != 1 {
		t.Fatalf("%v %v", l, err)
	}
	fp := l[0]
	if fp.ID != "tv.maktaba.toy" || fp.Trusted.Runner != "light" || fp.Trusted.Component != "classify" ||
		strings.Join(fp.Trusted.DBWrite, ",") != "fp_toy_out" || len(fp.Statements) != 5 {
		t.Fatalf("%+v", fp)
	}
	if ByStage("fp_toy") != fp || ByID("tv.maktaba.toy") != fp || ByStage("transcribe") != nil {
		t.Fatal("lookups")
	}
	keys := []string{}
	for _, c := range fp.Capabilities() {
		keys = append(keys, c["key"])
	}
	if !strings.Contains(strings.Join(keys, " "), "fp.trusted.db.statements") {
		t.Fatalf("what it can do: %v", keys)
	}
}

func TestTheEmbeddedBundleIsValid(t *testing.T) {
	if _, err := Plugins(); err != nil {
		t.Fatal(err)
	}
}

func TestABundleWithAThirdPartyIDIsRefused(t *testing.T) {
	raw := strings.ReplaceAll(string(toy(t)), "tv.maktaba.toy", "dev.example.toy")
	if _, _, err := parseBundle([]byte(raw)); err == nil {
		t.Fatal("a bundle plugin outside tv.maktaba. was accepted")
	}
}

func TestCheckPackage(t *testing.T) {
	files := []byte(`{"files":{"manifest.toml":"abc"}}`)
	sum := sha256.Sum256(files)
	signed := releaseverify.Checksums{"plugins/first-party/tv.maktaba.toy/files.json": hex.EncodeToString(sum[:])}
	ok := Package{ID: "tv.maktaba.toy", Version: "0.9.0", FilesJSON: files}
	code := func(err error) string {
		var e *Error
		if errors.As(err, &e) {
			return e.Code
		}
		return ""
	}
	if err := CheckPackage(ok, signed, "0.9.0"); err != nil {
		t.Fatalf("a signed package of this release: %v", err)
	}
	tampered := ok
	tampered.FilesJSON = append([]byte{}, files...)
	tampered.FilesJSON[3] ^= 1
	if c := code(CheckPackage(tampered, signed, "0.9.0")); c != CodeTrustedUnsigned {
		t.Fatalf("one byte changed: %q", c)
	}
	if c := code(CheckPackage(ok, signed, "0.9.1")); c != CodeTrustedVersion {
		t.Fatalf("the previous release's package: %q", c)
	}
	wrong := ok
	wrong.ID = "dev.example.toy"
	if c := code(CheckPackage(wrong, signed, "0.9.0")); c != CodeTrustedUnsigned {
		t.Fatalf("a package outside tv.maktaba.: %q", c)
	}
	if c := code(CheckPackage(ok, nil, "0.9.0")); c != CodeTrustedUnsigned {
		t.Fatalf("no verified checksums: %q", c)
	}
}
