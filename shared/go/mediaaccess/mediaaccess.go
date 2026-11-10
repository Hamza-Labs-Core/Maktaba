// Package mediaaccess checks what this process may do in a library root (Epic 59, story
// 59.03): read and write, read only, or nothing. On a NAS the container's user sees each
// share through the NAS's ACLs, and a household may give Maktaba read-only access to its
// originals; the check says which, per root, so a missing permission shows up at setup
// and on the library page instead of as jobs failing one at a time with EACCES.
//
// The check writes: a temporary directory under <root>/.maktaba, removed at once. A probe
// write wakes a sleeping disk, so callers run it only at setup, when a library is added or
// its roots change, from the library page's Check access and after a write failed with a
// permission error — never at every start.
package mediaaccess

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Access is what this process may do in a root.
type Access string

// The three results of a check.
const (
	ReadWrite Access = "rw"   // list, read and write
	ReadOnly  Access = "ro"   // list and read, not write
	None      Access = "none" // cannot list the root
)

// rank orders the results from least to most access.
func (a Access) rank() int {
	switch a {
	case None:
		return 0
	case ReadOnly:
		return 1
	case ReadWrite:
		return 2
	}
	return -1
}

// Valid reports whether a is one of the three results.
func (a Access) Valid() bool { return a.rank() >= 0 }

// Writable reports whether a allows writing. An unknown access (a library never checked)
// is writable: that is how Maktaba behaved before the check existed.
func (a Access) Writable() bool { return a == ReadWrite || !a.Valid() }

// Worst is the least access among results (a library is as writable as its least
// writable root); "" for none.
func Worst(as ...Access) Access {
	var out Access
	for _, a := range as {
		if !a.Valid() {
			continue
		}
		if out == "" || a.rank() < out.rank() {
			out = a
		}
	}
	return out
}

// MaxExamples is how many unreadable paths a result names.
const MaxExamples = 5

// Result is one root's check.
type Result struct {
	Path   string `json:"path"`
	Access Access `json:"access"`
	// Partial: some sampled files (or folders) could not be read.
	Partial bool `json:"partial"`
	// Unreadable names up to MaxExamples of them.
	Unreadable []string `json:"unreadable"`
	// MountReadOnly: the root's mount is read-only (from /proc/self/mountinfo; false when
	// unknown or not Linux). Inside a container without Landlock only a :ro mount
	// guarantees nothing writes, so the page says whether a read-only root is one.
	MountReadOnly bool `json:"mount_read_only"`
}

// Options tune a check; the zero value is the defaults.
type Options struct {
	// SampleFiles is how many files the sample reads at most (default 50).
	SampleFiles int
	// SampleBudget stops the sample after this long (default 2 s).
	SampleBudget time.Duration
	// MountInfo is the mountinfo file read for MountReadOnly (default /proc/self/mountinfo).
	MountInfo string
}

func (o Options) withDefaults() Options {
	if o.SampleFiles <= 0 {
		o.SampleFiles = 50
	}
	if o.SampleBudget <= 0 {
		o.SampleBudget = 2 * time.Second
	}
	if o.MountInfo == "" {
		o.MountInfo = "/proc/self/mountinfo"
	}
	return o
}

// Check is CheckWith with the defaults.
func Check(root string) Result { return CheckWith(root, Options{}) }

// CheckWith checks root: None when it cannot be listed; else ReadWrite when a temporary
// directory can be made (and is removed) under root/.maktaba, ReadOnly when not. .maktaba
// is created when missing and removed again afterwards; it is left in place only when it
// already existed. A read-only root is left exactly as it was. The sample then reads up
// to SampleFiles files breadth-first (for at most SampleBudget) and marks the result
// Partial, with examples, when some cannot be read. A relative root is None: the probe
// never writes relative to the process's working folder.
func CheckWith(root string, opt Options) Result {
	opt = opt.withDefaults()
	res := Result{Path: root, Unreadable: []string{}}
	if !filepath.IsAbs(root) {
		res.Access = None
		return res
	}
	root = filepath.Clean(root)
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		res.Access = None
		return res
	}
	if _, err := os.ReadDir(root); err != nil {
		res.Access = None
		return res
	}
	if probeWrite(root) {
		res.Access = ReadWrite
	} else {
		res.Access = ReadOnly
		res.MountReadOnly = mountReadOnly(root, opt.MountInfo)
	}
	res.Unreadable = sample(root, opt.SampleFiles, opt.SampleBudget)
	res.Partial = len(res.Unreadable) > 0
	if len(res.Unreadable) > MaxExamples {
		res.Unreadable = res.Unreadable[:MaxExamples]
	}
	return res
}

// probeWrite makes and removes a temporary directory under root/.maktaba.
func probeWrite(root string) bool {
	dir := filepath.Join(root, ".maktaba")
	created := false
	if fi, err := os.Lstat(dir); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		if err := os.Mkdir(dir, 0o755); err != nil {
			return false
		}
		created = true
	} else if !fi.IsDir() {
		return false
	}
	ok := false
	if tmp, err := os.MkdirTemp(dir, ".access-probe-"); err == nil {
		ok = os.Remove(tmp) == nil
	}
	if created {
		_ = os.Remove(dir)
	}
	return ok
}

// SkipDir reports whether the sample (and the scanner, pipeline scanner/walker.py, which
// keeps the same list) leaves a folder out: dot-folders (.maktaba, .zfs, .snapshot,
// .Trash-*, .AppleDouble), Synology's @eaDir, #recycle and #snapshot, and lost+found.
func SkipDir(name string) bool {
	if len(name) > 0 && name[0] == '.' {
		return true
	}
	switch name {
	case "@eaDir", "#recycle", "#snapshot", "lost+found":
		return true
	}
	return false
}

// sample reads up to max files breadth-first under root and returns the paths of the ones
// (and of the folders) it could not read, stopping at the budget.
func sample(root string, max int, budget time.Duration) []string {
	deadline := time.Now().Add(budget)
	var bad []string
	queue := []string{root}
	read := 0
	for len(queue) > 0 && read < max && time.Now().Before(deadline) {
		dir := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(dir)
		if err != nil {
			bad = append(bad, dir)
			continue
		}
		for _, e := range entries {
			if read >= max || !time.Now().Before(deadline) {
				break
			}
			p := filepath.Join(dir, e.Name())
			switch {
			case e.IsDir():
				if !SkipDir(e.Name()) {
					queue = append(queue, p)
				}
			case e.Type().IsRegular():
				if e.Name()[0] == '.' {
					continue
				}
				read++
				if !readable(p) {
					bad = append(bad, p)
				}
			}
		}
	}
	return bad
}

func readable(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var b [1]byte
	_, err = f.Read(b[:])
	return err == nil || errors.Is(err, io.EOF)
}
