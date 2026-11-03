package packs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Names inside the components folder.
const (
	CurrentFile  = "current"
	lockFile     = ".lock"
	removingFile = ".removing"
	removedFile  = ".removed"
	// missingFile names the release whose folder a rollback found gone (story 51.18)
	missingFile = ".missing"
	// versionsFile, in a model's rev_ folder, lists the releases whose index pins it
	versionsFile = "versions"
	partialDir   = ".partial"
	stagingPre   = ".staging-"
	revPre       = "rev_"
	// ModelFile is a model revision's record: its repository, revision and files.
	ModelRecordFile = "model.json"
)

// Layout is the components folder (<state>/components).
type Layout struct {
	Root string
}

// Dir is one component's folder. The callers pass catalog ids; elem keeps any other name
// inside Root all the same.
func (l Layout) Dir(id string) string { return filepath.Join(l.Root, elem(id)) }

// elem is s as one path element: rooted and cleaned first, so no "..", separator or
// volume in s reaches above the folder it is joined to (an id, a version, a digest are
// one element as they are).
func elem(s string) string { return filepath.Base(filepath.Clean("/" + s)) }

// PartialDir is where downloads in progress are kept.
func (l Layout) PartialDir() string { return filepath.Join(l.Root, partialDir) }

// Folder is a pack's folder name: <version>_<first 12 of its sha256>.
func PackFolder(version, sha string) string { return elem(version + "_" + short(sha)) }

// RevFolder is a model revision's folder name: rev_<first 12 of the revision>.
func RevFolder(rev string) string { return elem(revPre + short(rev)) }

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// Current is the folder the component's current pointer names ("" when none, or when
// it names a folder that is not there).
func (l Layout) Current(id string) string {
	b, err := os.ReadFile(filepath.Join(l.Dir(id), CurrentFile))
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(b))
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return ""
	}
	if info, err := os.Stat(filepath.Join(l.Dir(id), name)); err != nil || !info.IsDir() {
		return ""
	}
	return name
}

// SetCurrent writes the pointer atomically (layout.WriteFileAtomic, as the launcher
// layout's current).
func (l Layout) SetCurrent(id, folder string) error {
	return layout.WriteFileAtomic(filepath.Join(l.Dir(id), CurrentFile), []byte(folder+"\n"), 0o644)
}

// Removing reports a removal of id that started and has not finished.
func (l Layout) Removing(id string) bool {
	_, err := os.Stat(filepath.Join(l.Dir(id), removingFile))
	return err == nil
}

func (l Layout) markRemoving(id string) error {
	if err := os.MkdirAll(l.Dir(id), 0o755); err != nil {
		return err
	}
	return layout.WriteFileAtomic(filepath.Join(l.Dir(id), removingFile), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}

// Removed reports that id was removed through packs and not installed since (the app's
// venv step then builds the venv without the extras runtime.ml brought).
func (l Layout) Removed(id string) bool {
	_, err := os.Stat(filepath.Join(l.Dir(id), removedFile))
	return err == nil
}

// lockWait is how often a held lock is tried again.
var lockWait = 500 * time.Millisecond

// Lock takes id's lock (one install or removal at a time, also across processes and
// machines sharing the disk), waiting while another holds it, until ctx ends.
func (l Layout) Lock(ctx context.Context, id string) (func(), error) {
	p := filepath.Join(l.Dir(id), lockFile)
	for {
		f, err := layout.LockFile(p)
		if err == nil {
			return func() { layout.Unlock(f) }, nil
		}
		if !errors.Is(err, layout.ErrLocked) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockWait):
		}
	}
}

// Folders are the component's installed folders (not staging, not hidden).
func (l Layout) Folders(id string) []string {
	entries, err := os.ReadDir(l.Dir(id))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

// IDs are the components with a folder here.
func (l Layout) IDs() []string {
	entries, err := os.ReadDir(l.Root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

// Recover is the boot's tidy-up for the release version (story 51.16 §1, §2): staging
// folders a crash left are deleted, and a component whose current pointer does not name
// one of this release's folders while one is there (a crash between the rename and the
// pointer, or a rollback to a kept release) points at it. It returns what it did, for
// the log. Removals left unfinished (.removing) are the reconciler's to finish.
func (l Layout) Recover(version string) []string {
	var did []string
	for _, id := range l.IDs() {
		entries, err := os.ReadDir(l.Dir(id))
		if err != nil {
			continue
		}
		var mine []string
		for _, e := range entries {
			name := e.Name()
			switch {
			case e.IsDir() && strings.HasPrefix(name, stagingPre):
				if err := os.RemoveAll(filepath.Join(l.Dir(id), name)); err == nil {
					did = append(did, fmt.Sprintf("%s: deleted the staging folder %s", id, name))
				}
			case e.IsDir() && version != "" && strings.HasPrefix(name, version+"_"):
				mine = append(mine, name)
			}
		}
		cur := l.Current(id)
		if len(mine) == 1 && cur != mine[0] && !l.Removing(id) {
			if err := l.SetCurrent(id, mine[0]); err == nil {
				did = append(did, fmt.Sprintf("%s: current now names %s", id, mine[0]))
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(l.PartialDir(), ".import"))
	return did
}

// renameRetry is how long a rename held by another program (an antivirus on Windows) is
// tried again before the install fails busy.
var renameRetry = 30 * time.Second

func renameWithRetry(ctx context.Context, from, to string) error {
	deadline := time.Now().Add(renameRetry)
	for {
		err := os.Rename(from, to)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fail(ReasonBusy, map[string]any{"path": to}, "rename %s: %v", from, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
