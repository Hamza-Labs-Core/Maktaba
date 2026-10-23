// Package layout is the side-by-side install layout every Maktaba machine uses
// (Epic 35, design §2.1, story 35.2):
//
//	<install>/
//	  maktaba-launcher[.exe]
//	  current     "1.5.0\n"   the release the launcher starts
//	  previous    "1.4.2\n"   the release it falls back to
//	  pending     {"version":"1.5.0","previous":"1.4.2","run_id":"…","since":"…"}  (only while unconfirmed)
//	  watermark   "1.5.0\n"   highest version ever committed here (only rises)
//	  bin/releases/<version>/  maktaba-server maktaba-api maktaba-streaming … .verified
//	  updates/    launcher.log, lock, journal.json, downloads/, logs/
//
// Pointer files are plain text, never symlinks (Windows symlinks need
// privileges), and are replaced atomically: write a temp file, fsync, rename
// over the pointer, fsync the directory (Windows: MoveFileExW with
// MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH). A crash at any point
// leaves either the old or the new pointer, never a torn one.
//
// The package is shared by the launcher (cmd/maktaba-launcher) and
// maktaba-server (setup --self-managed, the supervisor, the updater). It uses
// only the standard library and golang.org/x/sys.
package layout

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Pointer file names.
const (
	CurrentFile   = "current"
	PreviousFile  = "previous"
	PendingFile   = "pending"
	WatermarkFile = "watermark"
	VerifiedFile  = ".verified"
)

// Environment the launcher hands the server it starts.
const (
	EnvLauncher    = "MAKTABA_LAUNCHER"     // "1" when started by maktaba-launcher
	EnvReleaseDir  = "MAKTABA_RELEASE_DIR"  // <install>/bin/releases/<current>
	EnvInstallRoot = "MAKTABA_INSTALL_ROOT" // <install>
)

// Errors.
var (
	ErrNoPointer      = errors.New("pointer not set")
	ErrBadVersion     = errors.New("bad version")
	ErrNotVerified    = errors.New("release not verified")
	ErrDigest         = errors.New("digest mismatch")
	ErrMissingRelease = errors.New("release directory missing")
	ErrDowngrade      = errors.New("below the watermark")
	ErrUnsafePerm     = errors.New("writable by other users")
)

// Layout is one install root.
type Layout struct {
	Root string
}

// New returns the layout at root (made absolute).
func New(root string) Layout {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return Layout{Root: root}
}

// BinRoot is <install>/bin (the Epic 33 §3.3 root ResolveReleaseBinary takes).
func (l Layout) BinRoot() string { return filepath.Join(l.Root, "bin") }

// ReleasesDir is <install>/bin/releases.
func (l Layout) ReleasesDir() string { return filepath.Join(l.Root, "bin", "releases") }

// ReleaseDir is <install>/bin/releases/<version>.
func (l Layout) ReleaseDir(version string) string { return filepath.Join(l.ReleasesDir(), version) }

// UpdatesDir is <install>/updates.
func (l Layout) UpdatesDir() string { return filepath.Join(l.Root, "updates") }

// LauncherLog is <install>/updates/launcher.log.
func (l Layout) LauncherLog() string { return filepath.Join(l.UpdatesDir(), "launcher.log") }

// LockPath is <install>/updates/lock (one launcher per install, EC5).
func (l Layout) LockPath() string { return filepath.Join(l.UpdatesDir(), "lock") }

// LauncherPath is <install>/maktaba-launcher[.exe].
func (l Layout) LauncherPath() string { return filepath.Join(l.Root, Exe("maktaba-launcher")) }

// ServerPath is the maktaba-server executable of a release.
func (l Layout) ServerPath(version string) string {
	return filepath.Join(l.ReleaseDir(version), Exe("maktaba-server"))
}

// Exe adds .exe on Windows.
func Exe(name string) string {
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
		return name + ".exe"
	}
	return name
}

// IsLayout reports whether root holds a layout (a current pointer).
func IsLayout(root string) bool {
	_, err := os.Stat(filepath.Join(root, CurrentFile))
	return err == nil
}

// ValidVersion accepts a release directory name: semver-like, no path parts.
func ValidVersion(v string) bool {
	if v == "" || len(v) > 64 || v == "." || v == ".." {
		return false
	}
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '-', r == '+', r == '_':
		default:
			return false
		}
	}
	return v[0] >= '0' && v[0] <= '9'
}

// NormalizeVersion strips a leading "v" and spaces.
func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && v[1] >= '0' && v[1] <= '9' {
		v = v[1:]
	}
	return v
}

// ReadPointer reads a version pointer (current, previous, watermark).
// A missing file is ErrNoPointer.
func (l Layout) ReadPointer(name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(l.Root, name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoPointer
	}
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", ErrNoPointer
	}
	if !ValidVersion(v) {
		return "", fmt.Errorf("%w: %s holds %q", ErrBadVersion, name, v)
	}
	return v, nil
}

// WritePointer atomically replaces a version pointer.
func (l Layout) WritePointer(name, version string) error {
	if !ValidVersion(version) {
		return fmt.Errorf("%w: %q", ErrBadVersion, version)
	}
	return WriteFileAtomic(filepath.Join(l.Root, name), []byte(version+"\n"), 0o644)
}

// Current is the current release ("" + ErrNoPointer when unset).
func (l Layout) Current() (string, error) { return l.ReadPointer(CurrentFile) }

// Previous is the fallback release.
func (l Layout) Previous() (string, error) { return l.ReadPointer(PreviousFile) }

// Watermark is the highest committed version ("" when unset).
func (l Layout) Watermark() string {
	v, _ := l.ReadPointer(WatermarkFile)
	return v
}

// RaiseWatermark sets the watermark to v when v is higher (it never falls).
func (l Layout) RaiseWatermark(v string) error {
	if w := l.Watermark(); w != "" && CompareVersions(v, w) <= 0 {
		return nil
	}
	return l.WritePointer(WatermarkFile, v)
}

// Pending is a switched-to release not yet confirmed by the supervisor.
type Pending struct {
	Version  string    `json:"version"`
	Previous string    `json:"previous,omitempty"`
	RunID    string    `json:"run_id,omitempty"`
	Since    time.Time `json:"since"`
	// Deadline (the update orchestrator's): not confirmed by then, the launcher
	// falls back; zero: its own pending timeout.
	Deadline time.Time `json:"deadline,omitzero"`
	// Record is the database's record the fallback marks for the dump taken
	// before Version's migrations to be put back (MarkRecordRestoring).
	Record string `json:"record,omitempty"`
}

// ReadPending returns the pending marker, or nil when there is none. A
// corrupt marker is reported as an error (the caller treats it as stale).
func (l Layout) ReadPending() (*Pending, error) {
	b, err := os.ReadFile(filepath.Join(l.Root, PendingFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Pending
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("pending: %w", err)
	}
	if !ValidVersion(p.Version) {
		return nil, fmt.Errorf("pending: %w: %q", ErrBadVersion, p.Version)
	}
	return &p, nil
}

// GuardPending adds the orchestrator's deadline and database record to the pending
// marker of version (a marker of another version is left alone).
func (l Layout) GuardPending(version string, deadline time.Time, record string) error {
	p, err := l.ReadPending()
	if err != nil {
		return err
	}
	if p == nil || p.Version != version {
		return fmt.Errorf("no pending marker for %s", version)
	}
	p.Deadline, p.Record = deadline.UTC(), record
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(l.Root, PendingFile), b, 0o644)
}

// ClearPending removes the pending marker (the supervisor's confirmation).
func (l Layout) ClearPending() error {
	err := os.Remove(filepath.Join(l.Root, PendingFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return syncDir(l.Root)
}

// Confirm is ClearPending under the name the orchestrator uses at commit.
func (l Layout) Confirm() error { return l.ClearPending() }

// Switch makes version current, keeping the old current as previous and
// marking version pending until Confirm. The release must validate.
//
// Order (each step one atomic rename, so a power cut anywhere is safe):
//  1. previous = old current
//  2. pending  = {version, previous: old current}
//  3. current  = version
//
// Cut after 1 or 2: current is still the old release (a pending that names
// a version other than current is stale and dropped by Reconcile). Cut after
// 3: the launcher starts the new release under the boot-loop guard.
func (l Layout) Switch(version, runID string, now time.Time) error {
	if err := l.ValidateRelease(version); err != nil {
		return err
	}
	old, err := l.Current()
	if err != nil && !errors.Is(err, ErrNoPointer) {
		return err
	}
	if old == version {
		return nil
	}
	if old != "" {
		if err := l.WritePointer(PreviousFile, old); err != nil {
			return err
		}
	}
	b, err := json.Marshal(Pending{Version: version, Previous: old, RunID: runID, Since: now.UTC()})
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(filepath.Join(l.Root, PendingFile), b, 0o644); err != nil {
		return err
	}
	return l.WritePointer(CurrentFile, version)
}

// Rollback makes previous current again and drops the pending marker
// (the boot-loop guard, design §2.2 step 5). It returns the version now
// current. Without a previous release it returns ErrNoPointer and changes
// nothing.
func (l Layout) Rollback() (string, error) {
	prev, err := l.Previous()
	if p, _ := l.ReadPending(); p != nil && p.Previous != "" {
		prev, err = p.Previous, nil
	}
	if err != nil {
		return "", err
	}
	if err := l.WritePointer(CurrentFile, prev); err != nil {
		return "", err
	}
	return prev, l.ClearPending()
}

// Reconcile repairs what a power cut in the middle of Switch or Rollback can
// leave: a pending marker naming a version that is not current (the switch
// never reached step 3, or the rollback reached its first step) is removed.
// It reports whether it changed anything.
func (l Layout) Reconcile() (bool, error) {
	p, perr := l.ReadPending()
	cur, cerr := l.Current()
	if perr != nil { // corrupt marker: unusable, drop it
		return true, l.ClearPending()
	}
	if p == nil {
		return false, nil
	}
	if cerr != nil || p.Version != cur {
		return true, l.ClearPending()
	}
	return false, nil
}

// Verified is <release>/.verified: what was checked when the release was
// staged (by releaseverify, story 35.1) or migrated (identity "migrated").
type Verified struct {
	SHA256     map[string]string `json:"sha256"`
	VerifiedAt time.Time         `json:"verified_at"`
	Identity   string            `json:"identity"`
	Version    string            `json:"version,omitempty"`
}

// IdentityMigrated marks a release copied from a pre-layout install (§2.4).
const IdentityMigrated = "migrated"

// ReadVerified reads a release's .verified record.
func (l Layout) ReadVerified(version string) (*Verified, error) {
	return ReadVerifiedDir(l.ReleaseDir(version))
}

// ReadVerifiedDir reads dir/.verified.
func ReadVerifiedDir(dir string) (*Verified, error) {
	b, err := os.ReadFile(filepath.Join(dir, VerifiedFile))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotVerified, err)
	}
	var v Verified
	if err := json.Unmarshal(b, &v); err != nil || len(v.SHA256) == 0 {
		return nil, fmt.Errorf("%w: %s/.verified is unreadable", ErrNotVerified, dir)
	}
	return &v, nil
}

// WriteVerified records dir's files (every regular file at the top level and
// below, except .verified itself) with their digests.
func WriteVerified(dir, identity, version string, now time.Time) (*Verified, error) {
	sums := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == VerifiedFile || strings.HasPrefix(rel, "venv/") {
			return nil // the venv is checked by its own hashes (pip --require-hashes)
		}
		sum, err := SHA256File(p)
		if err != nil {
			return err
		}
		sums[rel] = sum
		return nil
	})
	if err != nil {
		return nil, err
	}
	v := &Verified{SHA256: sums, VerifiedAt: now.UTC(), Identity: identity, Version: version}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return v, WriteFileAtomic(filepath.Join(dir, VerifiedFile), b, 0o644)
}

// ValidateRelease checks a release directory before it is started (story
// 35.2 AC3): it exists, has .verified, and maktaba-server's digest matches.
func (l Layout) ValidateRelease(version string) error {
	if !ValidVersion(version) {
		return fmt.Errorf("%w: %q", ErrBadVersion, version)
	}
	dir := l.ReleaseDir(version)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%w: %s", ErrMissingRelease, dir)
	}
	v, err := ReadVerifiedDir(dir)
	if err != nil {
		return err
	}
	// the same rule as the Sigstore root cache (releaseverify): nothing another
	// user could write is trusted
	for _, p := range []string{dir, filepath.Join(dir, Exe("maktaba-server")), filepath.Join(dir, VerifiedFile)} {
		if err := checkNotShared(p); err != nil {
			return err
		}
	}
	return checkFile(dir, Exe("maktaba-server"), v)
}

// VerifyFiles re-checks every file .verified lists (launcher --verify).
func VerifyFiles(dir string) (*Verified, error) {
	v, err := ReadVerifiedDir(dir)
	if err != nil {
		return nil, err
	}
	for name := range v.SHA256 {
		if err := checkFile(dir, name, v); err != nil {
			return v, err
		}
	}
	return v, nil
}

func checkFile(dir, name string, v *Verified) error {
	want, ok := v.SHA256[name]
	if !ok && runtime.GOOS == "windows" {
		want, ok = v.SHA256[strings.TrimSuffix(name, ".exe")]
	}
	if !ok {
		return fmt.Errorf("%w: %s is not in .verified", ErrNotVerified, name)
	}
	got, err := SHA256File(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMissingRelease, err)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: %s is %s, .verified says %s", ErrDigest, name, got, want)
	}
	return nil
}

// Versions lists the release directories present, oldest first.
func (l Layout) Versions() ([]string, error) {
	ents, err := os.ReadDir(l.ReleasesDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && ValidVersion(e.Name()) {
			out = append(out, e.Name())
		}
	}
	SortVersions(out)
	return out, nil
}

// RestartRequestPath is <install>/updates/restart: a file asking the running
// server to exit 75 so the launcher starts the (new) current release. The API
// writes it after a self-update; serve consumes it; the launcher clears a
// stale one before each start.
func (l Layout) RestartRequestPath() string { return filepath.Join(l.UpdatesDir(), "restart") }

// RequestRestart writes the restart request.
func (l Layout) RequestRestart() error {
	return WriteFileAtomic(l.RestartRequestPath(), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}

// TakeRestartRequest consumes a pending restart request.
func (l Layout) TakeRestartRequest() bool { return os.Remove(l.RestartRequestPath()) == nil }
