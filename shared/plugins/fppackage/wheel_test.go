package fppackage

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// wheelBytes: a wheel of these files under the info dist-info, its RECORD exact.
func wheelBytes(t *testing.T, info string, files map[string][]byte) []byte {
	t.Helper()
	var rec bytes.Buffer
	w := csv.NewWriter(&rec)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := sha256.Sum256(files[n])
		_ = w.Write([]string{n, "sha256=" + base64.RawURLEncoding.EncodeToString(s[:]), fmt.Sprint(len(files[n]))})
	}
	_ = w.Write([]string{info + "/RECORD", "", ""})
	w.Flush()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, n := range names {
		f, _ := z.Create(n)
		_, _ = f.Write(files[n])
	}
	f, _ := z.Create(info + "/RECORD")
	_, _ = f.Write(rec.Bytes())
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A wheel is limited in members and in its unpacked total, declared and read (fifth review
// of #1440), each member under its own limit.
func TestAWheelsMembersAndTotalAreLimited(t *testing.T) {
	// a total over the limit, each member under it; and too many members
	oldBytes, oldMembers := maxWheelBytes, maxWheelMembers
	defer func() { maxWheelBytes, maxWheelMembers = oldBytes, oldMembers }()
	big := map[string][]byte{"x/a.py": bytes.Repeat([]byte("a"), 600), "x/b.py": bytes.Repeat([]byte("b"), 600)}
	maxWheelBytes = 1 << 10
	// refused by what it declares, before a byte is read
	if _, _, _, err := ReadWheel(wheelBytes(t, "x-1.dist-info", big)); err == nil || !strings.Contains(err.Error(), "bytes unpacked, at most") {
		t.Fatalf("a wheel declaring over its total: %v", err)
	}
	// refused by what it reads: RECORD names a member twice, so it is read twice, over the
	// total though the declared sizes are under it
	a := bytes.Repeat([]byte("a"), 600)
	sum := sha256.Sum256(a)
	row := "x/a.py,sha256=" + base64.RawURLEncoding.EncodeToString(sum[:]) + ",600\n"
	var twice bytes.Buffer
	z := zip.NewWriter(&twice)
	for n, b := range map[string][]byte{"x/a.py": a, "x-1.dist-info/RECORD": []byte(row + row + "x-1.dist-info/RECORD,,\n")} {
		w, _ := z.Create(n)
		_, _ = w.Write(b)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ReadWheel(twice.Bytes()); err == nil || !strings.Contains(err.Error(), "over ") {
		t.Fatalf("a wheel reading over its total: %v", err)
	}
	maxWheelBytes, maxWheelMembers = oldBytes, 2
	if _, _, _, err := ReadWheel(wheelBytes(t, "x-1.dist-info", big)); err == nil || !strings.Contains(err.Error(), "members") {
		t.Fatalf("a wheel over its members: %v", err)
	}
}
