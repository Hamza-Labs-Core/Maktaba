// Package plugins is the supervisor's half of Epic 54's lifecycle (stories 54.07, 54.37,
// 54.38): it puts installed plugins on disk, keeps the files following plugin_installs,
// merges the plugins' catalog entries into the reconciler, and reports each plugin's
// files part in component_status.
//
// The API never writes a plugin folder: it streams a package to this machine's supervisor
// (POST /plugins/install on the loopback admin, the per-boot token), which unpacks it with
// the safe reader (componentpack.UnpackPlugin), validates its manifest, preloads it in the
// sandboxed host (maktaba-plugind --preload) and renames it into
// <state>/plugins/<id>/<version>_<sha12 of files.json>/, on Epic 51's packs layout. The
// API then writes plugin_installs and the desired row; this package's Runner follows them.
package plugins

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

// Reasons of a refused install (the API passes them on; each has words,
// plugins.error.<code>). The packs reasons are 51.16's.
const (
	ReasonUnpackRefused = packs.ReasonUnpackRefused
	ReasonDiskSpace     = packs.ReasonDiskSpace
	ReasonBusy          = packs.ReasonBusy
	// ReasonPreloadUnavailable: the host could not be started for the preload.
	ReasonPreloadUnavailable = "plugin_preload_unavailable"
)

// Refusal is an install this machine refused: its code (a manifest or package code, a
// host load code such as plugin_forbidden_import, or a reason above) and the detail.
type Refusal struct {
	Code   string `json:"error"`
	Detail string `json:"detail"`
	// Reason is the status reason it maps to (unpack_refused, disk_space, busy).
	Reason string `json:"reason"`
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Detail }

func refuse(code, reason, format string, args ...any) *Refusal {
	return &Refusal{Code: code, Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// Installed is a package on disk.
type Installed struct {
	PluginID string `json:"plugin_id"`
	Version  string `json:"version"`
	// Digest is the sha256 of the package's files.json (what a signature covers).
	Digest string `json:"digest"`
	// Folder is <version>_<sha12>; Path the whole folder.
	Folder string `json:"folder"`
	Path   string `json:"path"`
	// Manifest is the normalised manifest (the four validators' one text).
	Manifest json.RawMessage `json:"manifest"`
	// Signed: the package carries a developer signature (none is checked before 54.14).
	Signed bool `json:"signed"`
	// Already: this version and digest were on disk; nothing was written.
	Already bool `json:"already"`
}

// Files puts plugin packages on disk under Layout.Root (<state>/plugins).
type Files struct {
	Layout packs.Layout
	// Preload checks a package folder in the sandboxed host (nil: HostPreload's).
	Preload func(ctx context.Context, id, version, dir string) *Refusal
}

// newStaging is a hidden folder name in the root for an unpack whose id is not known yet.
func newStaging() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return packs.StagingPrefix + hex.EncodeToString(b)
}

// Install unpacks, validates and preloads the package r and moves it into place. A
// version and digest already on disk is answered Already, untouched. The plugin's current
// pointer is set only when it has none (a first install): an update's switch is the
// Runner's, once plugin_installs names the new folder.
func (f *Files) Install(ctx context.Context, r io.Reader) (*Installed, error) {
	if err := os.MkdirAll(f.Layout.Root, 0o755); err != nil {
		return nil, err
	}
	tmp := filepath.Join(f.Layout.Root, newStaging())
	pkg, err := componentpack.UnpackPlugin(r, tmp, componentpack.PluginExpect{})
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			return nil, refuse(ReasonDiskSpace, ReasonDiskSpace, "%v", err)
		}
		if code := componentpack.PluginCode(err); code != "" {
			return nil, refuse(code, ReasonUnpackRefused, "%v", err)
		}
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(tmp)
		}
	}()
	files := func(p string) ([]byte, bool) {
		if !componentpack.SafePluginPath(p) {
			return nil, false
		}
		b, err := os.ReadFile(filepath.Join(tmp, filepath.FromSlash(p)))
		return b, err == nil
	}
	m, errs := pluginmanifest.Validate(pkg.Manifest, files)
	if len(errs) > 0 {
		return nil, refuse(errs[0].Code, ReasonUnpackRefused, "%s", errs.Error())
	}
	id, _ := m["id"].(string)
	version, _ := m["version"].(string)
	out := &Installed{PluginID: id, Version: version, Digest: pkg.FilesSHA256,
		Folder: packs.PackFolder(version, pkg.FilesSHA256), Manifest: pluginmanifest.Normalised(m),
		Signed: len(pkg.DeveloperSig) > 0}
	unlock, err := f.Layout.Lock(ctx, id)
	if err != nil {
		return nil, refuse(ReasonBusy, ReasonBusy, "%v", err)
	}
	defer unlock()
	dest := filepath.Join(f.Layout.Dir(id), out.Folder)
	out.Path = dest
	if info, err := os.Stat(dest); err == nil && info.IsDir() {
		out.Already = true
		return out, nil
	}
	if err := os.Rename(tmp, dest); err != nil {
		return nil, refuse(ReasonBusy, ReasonBusy, "rename %s: %v", dest, err)
	}
	keep = true
	if ref := f.preload(ctx, id, version, dest); ref != nil {
		_ = os.RemoveAll(dest)
		return nil, ref
	}
	if f.Layout.Current(id) == "" {
		if err := f.Layout.SetCurrent(id, out.Folder); err != nil {
			_ = os.RemoveAll(dest)
			return nil, err
		}
	}
	return out, nil
}

func (f *Files) preload(ctx context.Context, id, version, dir string) *Refusal {
	if f.Preload == nil {
		return nil
	}
	return f.Preload(ctx, id, version, dir)
}

// Discard removes a folder of id that is neither its current one nor kept (an install the
// API refused after the files were placed): true when it was removed.
func (f *Files) Discard(ctx context.Context, id, folder string, keep ...string) (bool, error) {
	if folder == "" || strings.ContainsAny(folder, `/\`) || strings.HasPrefix(folder, ".") {
		return false, fmt.Errorf("plugins: not a folder name: %q", folder)
	}
	unlock, err := f.Layout.Lock(ctx, id)
	if err != nil {
		return false, err
	}
	defer unlock()
	if f.Layout.Current(id) == folder {
		return false, nil
	}
	for _, k := range keep {
		if k == folder {
			return false, nil
		}
	}
	dir := filepath.Join(f.Layout.Dir(id), folder)
	if _, err := os.Stat(dir); err != nil {
		return false, nil
	}
	return true, os.RemoveAll(dir)
}

// Remove deletes every file of id (an uninstall, want absent): marked removing first, so
// a crash midway is finished at the next start.
func (f *Files) Remove(ctx context.Context, id string) error {
	unlock, err := f.Layout.Lock(ctx, id)
	if err != nil {
		return err
	}
	if err := f.Layout.MarkRemoving(id); err != nil {
		unlock()
		return err
	}
	unlock() // the lock file is inside the folder that goes
	return os.RemoveAll(f.Layout.Dir(id))
}

// HostPreload is the Files.Preload that runs bin --preload in the host's sandbox, with
// the host's own folders (the compile cache is filled for its first job).
func HostPreload(bin string, args func(dir, id, version string) []string) func(ctx context.Context, id, version, dir string) *Refusal {
	return func(ctx context.Context, id, version, dir string) *Refusal {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(cctx, bin, args(dir, id, version)...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var ans struct {
			OK        bool   `json:"ok"`
			ErrorCode string `json:"error_code"`
			Detail    string `json:"detail"`
		}
		line := bytes.TrimSpace(stdout.Bytes())
		if i := bytes.LastIndexByte(line, '\n'); i >= 0 {
			line = line[i+1:]
		}
		if jerr := json.Unmarshal(line, &ans); jerr != nil {
			msg := strings.TrimSpace(stderr.String())
			if err != nil && msg == "" {
				msg = err.Error()
			}
			return refuse(ReasonPreloadUnavailable, ReasonUnpackRefused, "the plugin host did not check the package: %s", msg)
		}
		if !ans.OK {
			return refuse(ans.ErrorCode, ReasonUnpackRefused, "%s", ans.Detail)
		}
		return nil
	}
}
