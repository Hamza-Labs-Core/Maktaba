package pgbin

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Mac's pin (what Maktaba.app ships) and ours are one: a node's copy needs the
// home's exact build.
func TestPinsMatchTheMacApp(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "..", "tools", "release", "mac-app", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok && !strings.HasPrefix(k, "#") {
			env[k] = v
		}
	}
	mac, _ := For("darwin", "arm64")
	if env["POSTGRES_VERSION"] != Version || env["POSTGRES_SHA256"] != mac.SHA256 || env["POSTGRES_URL"] != mac.URL() {
		t.Fatalf("pins.env %s %s %s, here %s %s %s", env["POSTGRES_VERSION"], env["POSTGRES_SHA256"], env["POSTGRES_URL"],
			Version, mac.SHA256, mac.URL())
	}
	if !strings.HasPrefix(Version, Major+".") {
		t.Fatalf("major %s of %s", Major, Version)
	}
	for _, p := range []string{"windows/amd64", "linux/amd64", "darwin/arm64"} {
		os, arch, _ := strings.Cut(p, "/")
		if _, ok := For(os, arch); !ok {
			t.Fatalf("no archive for %s", p)
		}
	}
}

var programs = []string{"postgres", "initdb", "pg_ctl", "pg_basebackup", "psql"}

// archive builds an install's archive of the theseus-rs layout.
func archive(t *testing.T, pin Pin, extra map[string]string) []byte {
	t.Helper()
	files := map[string]string{pin.Dir() + "/share/postgresql.conf.sample": "#"}
	for _, b := range programs {
		files[pin.Dir()+"/bin/"+Exe(b)] = "#!/bin/sh\n"
	}
	for k, v := range extra {
		files[k] = v
	}
	var buf bytes.Buffer
	if pin.Zip {
		zw := zip.NewWriter(&buf)
		for name, body := range files {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, body)
		}
		_ = zw.Close()
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = io.WriteString(tw, body)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func pinFor(raw []byte, zipped bool) Pin {
	s := sha256.Sum256(raw)
	return Pin{Triple: "test-triple", SHA256: hex.EncodeToString(s[:]), Size: int64(len(raw)), Zip: zipped}
}

func serve(raw []byte, calls *int) Fetcher {
	return func(_ context.Context, _ string, w io.Writer) error {
		*calls++
		_, err := w.Write(raw)
		return err
	}
}

func TestEnsureUnpacksOnce(t *testing.T) {
	for _, zipped := range []bool{true, false} {
		root := t.TempDir()
		probe := Pin{Triple: "test-triple", Zip: zipped}
		raw := archive(t, probe, nil)
		pin := pinFor(raw, zipped)
		calls := 0
		home, err := Ensure(context.Background(), root, pin, serve(raw, &calls), io.Discard)
		if err != nil {
			t.Fatalf("zip=%v: %v", zipped, err)
		}
		if home != filepath.Join(root, pin.Dir()) || !Installed(home) {
			t.Fatalf("zip=%v: home %s installed %v", zipped, home, Installed(home))
		}
		if _, err := Ensure(context.Background(), root, pin, serve(raw, &calls), io.Discard); err != nil || calls != 1 {
			t.Fatalf("zip=%v: an install there was downloaded again (%d): %v", zipped, calls, err)
		}
		left, _ := filepath.Glob(filepath.Join(root, ".*"))
		if len(left) != 0 {
			t.Fatalf("zip=%v: leftovers %v", zipped, left)
		}
	}
}

func TestEnsureRefusesAnArchiveNotPinned(t *testing.T) {
	root := t.TempDir()
	raw := archive(t, Pin{Triple: "test-triple", Zip: true}, nil)
	pin := pinFor(raw, true)
	pin.SHA256 = strings.Repeat("0", 64)
	calls := 0
	if _, err := Ensure(context.Background(), root, pin, serve(raw, &calls), io.Discard); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("a changed archive: %v", err)
	}
	if Installed(filepath.Join(root, pin.Dir())) {
		t.Fatal("installed anyway")
	}
}

func TestEnsureRefusesAPathOutOfTheInstall(t *testing.T) {
	for _, zipped := range []bool{true, false} {
		root := t.TempDir()
		raw := archive(t, Pin{Triple: "test-triple", Zip: zipped}, map[string]string{"../../escaped.txt": "x"})
		calls := 0
		if _, err := Ensure(context.Background(), root, pinFor(raw, zipped), serve(raw, &calls), io.Discard); err == nil ||
			!strings.Contains(err.Error(), "leaves the install") {
			t.Fatalf("zip=%v: %v", zipped, err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escaped.txt")); err == nil {
			t.Fatal("a file was written outside the install")
		}
	}
}

func TestEnsureReportsAFailedDownload(t *testing.T) {
	failing := func(context.Context, string, io.Writer) error { return errors.New("offline") }
	if _, err := Ensure(context.Background(), t.TempDir(), Pin{Triple: "x", SHA256: "y"}, failing, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "offline") {
		t.Fatalf("%v", err)
	}
}

// A link may only name a file beside it: "..", ".", or a path would let a later entry
// be written outside the install through it.
func TestUntarRefusesALinkThatLeaves(t *testing.T) {
	for _, target := range []string{"..", ".", "../lib", "sub/libpq.so", `..\x`, ""} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: "pg/lib/libpq.so.5", Linkname: target, Typeflag: tar.TypeSymlink})
		_ = tw.Close()
		_ = gz.Close()
		dir := t.TempDir()
		arc := filepath.Join(t.TempDir(), "a.tar.gz")
		if err := os.WriteFile(arc, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := untargz(arc, dir); err == nil || !strings.Contains(err.Error(), "leaves its directory") {
			t.Fatalf("link to %q: %v", target, err)
		}
	}
	// a library's versioned name is fine
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "pg/lib/libpq.so.5", Linkname: "libpq.so.5.16", Typeflag: tar.TypeSymlink})
	_ = tw.Close()
	_ = gz.Close()
	arc := filepath.Join(t.TempDir(), "a.tar.gz")
	_ = os.WriteFile(arc, buf.Bytes(), 0o600)
	if err := untargz(arc, t.TempDir()); err != nil && !os.IsPermission(err) && !strings.Contains(err.Error(), "privilege") {
		t.Fatalf("a link beside it: %v", err)
	}
}
