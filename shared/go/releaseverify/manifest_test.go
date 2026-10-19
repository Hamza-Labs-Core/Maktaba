package releaseverify_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestParseChecksums(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	sums, err := rv.ParseChecksums([]byte(a + "  ./one.tar.gz\r\n" + b + " *two.zip\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sums["one.tar.gz"] != a || sums["two.zip"] != b || len(sums) != 2 {
		t.Fatalf("sums = %v", sums)
	}
}

// EC1: duplicates and bad digests are errors, never "first wins".
func TestParseChecksumsRejects(t *testing.T) {
	a := strings.Repeat("a", 64)
	for name, in := range map[string]string{
		"duplicate":   a + "  x\n" + strings.Repeat("b", 64) + "  ./x\n",
		"short hex":   "abc  x\n",
		"upper hex":   strings.Repeat("A", 64) + "  x\n",
		"non hex":     strings.Repeat("g", 64) + "  x\n",
		"path":        a + "  dir/x\n",
		"parent":      a + "  ../x\n",
		"no name":     a + "\n",
		"empty":       "\n\n",
		"option-like": a + "  -rf\n",
	} {
		if _, err := rv.ParseChecksums([]byte(in)); !errors.Is(err, rv.ErrManifest) {
			t.Errorf("%s: err = %v, want ErrManifest", name, err)
		}
	}
}

func FuzzParseChecksums(f *testing.F) {
	f.Add([]byte(strings.Repeat("a", 64) + "  ./x\n"))
	f.Add([]byte("garbage"))
	f.Fuzz(func(t *testing.T, b []byte) {
		sums, err := rv.ParseChecksums(b)
		if err != nil {
			if !errors.Is(err, rv.ErrManifest) {
				t.Fatalf("untyped error %v", err)
			}
			return
		}
		for n, d := range sums {
			if len(d) != 64 || n == "" || strings.ContainsAny(n, "/\\") {
				t.Fatalf("bad entry %q=%q", n, d)
			}
		}
	})
}

func FuzzParseReleaseJSON(f *testing.F) {
	f.Add([]byte(`{"v":1,"version":"1.0.0","tag":"v1.0.0"}`))
	f.Add([]byte(`{"v":2}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := rv.ParseManifest(b)
		if err != nil {
			if !errors.Is(err, rv.ErrManifest) {
				t.Fatalf("untyped error %v", err)
			}
			return
		}
		if err := m.Validate("v1.0.0", rv.Checksums{}); err != nil && !errors.Is(err, rv.ErrManifest) {
			t.Fatalf("untyped validate error %v", err)
		}
	})
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.5.0", "1.4.9", 1},
		{"v1.5.0", "1.5.0", 0},
		{"1.10.0", "1.9.0", 1},
		{"1.5.0-rc.1", "1.5.0", -1},
		{"1.5.0-rc.2", "1.5.0-rc.10", -1},
		{"1.5.0-beta", "1.5.0-alpha", 1},
		{"dev", "0.0.1", -1},
		{"1.5", "1.5.0", 0},
		{"1.5.0+build", "1.5.0", 0},
	}
	for _, c := range cases {
		if got := rv.CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := rv.CompareVersions(c.b, c.a); got != -c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}
