package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// keptMarker is the file the server writes last into a kept payload copy
// (cmd/maktaba-server/internal/updater): a copy cut short has none.
const keptMarker = ".kept"

// materializeOnWindows: on Windows a kept payload is put back into the app's own
// payload folder before it runs (review B-H2). The firewall's rules name each program
// by its path (the install's and Windows' own), so a server run from
// updates\payloads\<v> would be cut off from the network: nodes, TVs and phones could
// not reach it. Elsewhere the kept copy runs where it is.
var materializeOnWindows = runtime.GOOS == "windows"

// materialize copies the kept payload kept over the app's payload bundle, leaving the
// running launcher (self) alone, and removes what the release that failed added: the
// bundle is then the kept release, at the paths the firewall knows. A copy cut short
// (a power cut) is done again at the next start: the override stays until it is whole.
func materialize(bundle, kept, self string) error {
	if _, err := os.Stat(filepath.Join(kept, keptMarker)); err != nil {
		return fmt.Errorf("%s is no whole kept copy: %w", kept, err)
	}
	want := map[string]bool{}
	selfClean := filepath.Clean(self)
	err := filepath.WalkDir(kept, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(kept, p)
		if err != nil || rel == "." || rel == keptMarker {
			return err
		}
		dst := filepath.Join(bundle, rel)
		want[strings.ToLower(filepath.Clean(dst))] = true
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(dst, info.Mode().Perm()|0o700)
		case !info.Mode().IsRegular() || strings.EqualFold(filepath.Clean(dst), selfClean):
			return nil // the running launcher stays (it speaks the same protocol)
		}
		return copyInto(p, dst, info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	// what the failed release added (a second pipeline wheel would stop the venv build)
	var extra []string
	_ = filepath.WalkDir(bundle, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == bundle {
			return nil
		}
		c := filepath.Clean(p)
		if want[strings.ToLower(c)] || strings.EqualFold(c, selfClean) || strings.EqualFold(c, selfClean+".old") {
			return nil
		}
		extra = append(extra, p)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	for _, p := range extra {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

// copyInto writes src over dst (beside it, then renamed into place: a file cut short
// never replaces one).
func copyInto(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".materialize"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := layout.ReplaceFile(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
