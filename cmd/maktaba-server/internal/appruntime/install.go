package appruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrDiskFull: the runtime's disk has less room than the payload plus 10%.
var ErrDiskFull = errors.New("disk_full")

// Runtime is one user's runtime root (<data>/runtime).
type Runtime struct {
	Root string
	// Now and FreeBytes are what tests replace.
	Now       func() time.Time
	FreeBytes func(dir string) (uint64, error)
}

// New is the runtime at root.
func New(root string) Runtime { return Runtime{Root: root, Now: time.Now, FreeBytes: freeBytes} }

// Installed is a runtime directory's .installed record.
type Installed struct {
	Version     string    `json:"version"`
	Platform    string    `json:"platform"`
	FilesSHA256 string    `json:"files_sha256"`
	From        string    `json:"from"`
	InstalledAt time.Time `json:"installed_at"`
}

// Progress is a copy's progress in bytes.
type Progress func(done, total int64)

// Platform is this machine's payload platform (payload.json's "platform").
func Platform() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return runtime.GOOS + "-" + arch
}

// Install copies the payload in from into the runtime (verifying every file
// against its manifest on the way) and returns its directory. A directory of
// the same name already installed from the same manifest is kept as it is
// (copied false). `current` is not moved: that is Activate's.
func (r Runtime) Install(ctx context.Context, from string, progress Progress) (dir string, copied bool, err error) {
	p, err := ReadPayload(from)
	if err != nil {
		return "", false, err
	}
	if p.Platform != Platform() {
		return "", false, fmt.Errorf("%w: the payload is for %s, this machine is %s", ErrInvalid, p.Platform, Platform())
	}
	entries, err := ReadManifest(from, p)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(r.Root, 0o755); err != nil {
		return "", false, err
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return "", false, err
	}
	defer unlock()

	name := DirName(p)
	final := filepath.Join(r.Root, name)
	if inst, err := readInstalled(final); err == nil && inst.FilesSHA256 == p.Files.SHA256 {
		return final, false, nil
	}
	if cur, _ := r.Current(); cur == name {
		return "", false, fmt.Errorf("%w: the running runtime %s does not match its manifest: stop the service and install again",
			ErrInvalid, final)
	}
	if free, err := r.FreeBytes(r.Root); err == nil && free < uint64(p.Files.Bytes)+uint64(p.Files.Bytes)/10 {
		return "", false, fmt.Errorf("%w: %s has %d MB free, the payload needs %d MB", ErrDiskFull, r.Root, free>>20, p.Files.Bytes*11/10>>20)
	}
	r.removeStaging()
	staging := filepath.Join(r.Root, ".staging-"+name+"-"+randHex())
	if err := os.Mkdir(staging, 0o755); err != nil {
		return "", false, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(staging)
		}
	}()
	var done int64
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if err := copyEntry(from, staging, e); err != nil {
			return "", false, err
		}
		done += e.Size
		if progress != nil && e.Kind == 'f' {
			progress(done, p.Files.Bytes)
		}
	}
	for _, f := range []string{PayloadJSON, Manifest} {
		if err := copyPlain(filepath.Join(from, f), filepath.Join(staging, f)); err != nil {
			return "", false, err
		}
	}
	rec, _ := json.MarshalIndent(Installed{Version: p.Version, Platform: p.Platform, FilesSHA256: p.Files.SHA256,
		From: from, InstalledAt: r.Now().UTC()}, "", "  ")
	if err := os.WriteFile(filepath.Join(staging, installedFn), append(rec, '\n'), 0o644); err != nil {
		return "", false, err
	}
	syncAll()
	if err := os.RemoveAll(final); err != nil { // a copy cut short, or of another build
		return "", false, err
	}
	if err := os.Rename(staging, final); err != nil {
		return "", false, err
	}
	return final, true, nil
}

// copyEntry copies one manifest entry from the payload into dst, checking a
// file's size and SHA-256 as it is copied.
func copyEntry(from, dst string, e Entry) error {
	src := filepath.Join(from, filepath.FromSlash(e.Path))
	out := filepath.Join(dst, filepath.FromSlash(e.Path))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if e.Kind == 'l' {
		if t, err := os.Readlink(src); err != nil || t != e.Target {
			return fmt.Errorf("%w: %s is not the symlink to %s its manifest lists", ErrInvalid, src, e.Target)
		}
		return os.Symlink(e.Target, out)
	}
	info, err := os.Lstat(src)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is missing or not a file", ErrInvalid, src)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	defer in.Close()
	w, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), in)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != e.Size || hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
		return fmt.Errorf("%w: %s changed since the payload was built (its manifest lists %d bytes, SHA-256 %s)",
			ErrInvalid, src, e.Size, e.SHA256)
	}
	return os.Chmod(out, e.Mode)
}

func copyPlain(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func readInstalled(dir string) (Installed, error) {
	var inst Installed
	b, err := os.ReadFile(filepath.Join(dir, installedFn))
	if err != nil {
		return inst, err
	}
	return inst, json.Unmarshal(b, &inst)
}

// removeStaging removes what an install cut short left (under the lock: no
// install is running).
func (r Runtime) removeStaging() {
	ents, _ := os.ReadDir(r.Root)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".staging-") {
			_ = os.RemoveAll(filepath.Join(r.Root, e.Name()))
		}
	}
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func randHex() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
