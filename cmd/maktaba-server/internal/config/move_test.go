package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// volumes is a mover whose renames between two of these folders fail the way
// os.Rename fails between two volumes (EXDEV, ERROR_NOT_SAME_DEVICE); a path
// outside all of them is on a volume of its own.
func volumes(roots ...string) mover {
	vol := func(p string) string {
		best := ""
		for _, r := range roots {
			if (p == r || strings.HasPrefix(p, r+string(filepath.Separator))) && len(r) > len(best) {
				best = r
			}
		}
		return best
	}
	return mover{rename: func(from, to string) error {
		if vol(from) != vol(to) {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: errCrossDevice}
		}
		return os.Rename(from, to)
	}}
}

// cutAt fails m's nth rename of a finished copy into place (a power cut half way).
func cutAt(m mover, n int) mover {
	count := 0
	return mover{rename: func(from, to string) error {
		if strings.HasSuffix(from, partSuffix) {
			if count++; count == n {
				return errors.New("power cut")
			}
		}
		return m.rename(from, to)
	}}
}

// plant writes files (path relative to root → body).
func plant(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		writeAt(t, filepath.Join(root, filepath.FromSlash(p)), body)
	}
}

// holds checks root holds exactly these files, and nothing a move leaves behind.
func holds(t *testing.T, root string, files map[string]string) {
	t.Helper()
	seen := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		want, ok := files[filepath.ToSlash(rel)]
		if !ok {
			t.Errorf("%s: unexpected %s", root, rel)
			return nil
		}
		if got := readAt(t, p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
		seen++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != len(files) {
		t.Errorf("%s holds %d of the %d files", root, seen, len(files))
	}
}

// Between volumes a file is copied, flushed, compared and only then removed.
func TestMoveFileAcrossVolumes(t *testing.T) {
	t.Parallel()
	a, b := t.TempDir(), t.TempDir()
	m := volumes(a, b)
	from, to := filepath.Join(a, "secrets.key"), filepath.Join(b, "keys", "secrets.key")
	writeAt(t, from, strings.Repeat("k", 200<<10)) // more than one compare buffer
	if moved, err := m.file(from, to); err != nil || !moved {
		t.Fatalf("file = %v, %v", moved, err)
	}
	if exists(from) || exists(to+partSuffix) || readAt(t, to) != strings.Repeat("k", 200<<10) {
		t.Fatal("the key must be whole in the new place and gone from the old")
	}
}

// A file never replaces another: the same bytes are a move that stopped before it
// removed the source; other bytes stay apart.
func TestMoveFileNeverReplaces(t *testing.T) {
	t.Parallel()
	a, b := t.TempDir(), t.TempDir()
	m := volumes(a, b)
	writeAt(t, filepath.Join(a, "same"), "S")
	writeAt(t, filepath.Join(b, "same"), "S")
	writeAt(t, filepath.Join(a, "other"), "OLD")
	writeAt(t, filepath.Join(b, "other"), "NEW")
	if moved, err := m.file(filepath.Join(a, "same"), filepath.Join(b, "same")); err != nil || !moved || exists(filepath.Join(a, "same")) {
		t.Fatalf("the same bytes: moved %v, %v; the source must go", moved, err)
	}
	if _, err := m.file(filepath.Join(a, "other"), filepath.Join(b, "other")); !errors.Is(err, errKept) {
		t.Fatalf("other bytes: %v, want errKept", err)
	}
	if readAt(t, filepath.Join(a, "other")) != "OLD" || readAt(t, filepath.Join(b, "other")) != "NEW" {
		t.Fatal("both files must stay as they were")
	}
	if moved, err := m.file(filepath.Join(a, "none"), filepath.Join(b, "none")); err != nil || moved {
		t.Fatalf("nothing to move: %v, %v", moved, err)
	}
}

// A copy a crash cut short (a part file beside the new place, the source whole)
// is made again by the next start.
func TestMoveFileAfterACrashMidCopy(t *testing.T) {
	t.Parallel()
	a, b := t.TempDir(), t.TempDir()
	from, to := filepath.Join(a, "stream-signing.key"), filepath.Join(b, "stream-signing.key")
	writeAt(t, from, "WHOLE KEY")
	writeAt(t, to+partSuffix, "WHO")
	if _, err := volumes(a, b).file(from, to); err != nil {
		t.Fatal(err)
	}
	if readAt(t, to) != "WHOLE KEY" || exists(from) || exists(to+partSuffix) {
		t.Fatal("the next start must finish the copy")
	}
}

// Between volumes a folder moves file by file; a move cut half way leaves a note
// beside the new place, and the next start finishes it and removes the old
// folders.
func TestMoveDirAcrossVolumesFinishesAfterACut(t *testing.T) {
	t.Parallel()
	a, b := t.TempDir(), t.TempDir()
	files := map[string]string{"huggingface/hub/w.bin": "W", "huggingface/hub/x.bin": "X", "ollama/m": "M", "top": "T"}
	from, to := filepath.Join(a, "models"), filepath.Join(b, "models")
	plant(t, from, files)
	m := volumes(a, b)
	if _, err := cutAt(m, 2).dir(from, to); err == nil || !strings.Contains(err.Error(), "power cut") {
		t.Fatalf("cut move = %v", err)
	}
	if !exists(to+movingSuffix) || !exists(from) {
		t.Fatal("a cut move keeps its note and what it did not move yet")
	}
	if moved, err := m.dir(from, to); err != nil || !moved {
		t.Fatalf("next start = %v, %v", moved, err)
	}
	holds(t, to, files)
	if exists(from) || exists(to+movingSuffix) {
		t.Fatal("the old folder and the note must be gone")
	}
}

// A folder never merges into another; an empty one is taken.
func TestMoveDirNeverMerges(t *testing.T) {
	t.Parallel()
	a, b := t.TempDir(), t.TempDir()
	m := volumes(a, b)
	plant(t, filepath.Join(a, "chroma"), map[string]string{"old.sqlite3": "OLD"})
	plant(t, filepath.Join(b, "chroma"), map[string]string{"new.sqlite3": "NEW"})
	if _, err := m.dir(filepath.Join(a, "chroma"), filepath.Join(b, "chroma")); !errors.Is(err, errKept) {
		t.Fatalf("two folders: %v, want errKept", err)
	}
	holds(t, filepath.Join(a, "chroma"), map[string]string{"old.sqlite3": "OLD"})
	holds(t, filepath.Join(b, "chroma"), map[string]string{"new.sqlite3": "NEW"})

	plant(t, filepath.Join(a, "cache"), map[string]string{"seg": "C"})
	if err := os.MkdirAll(filepath.Join(b, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	if moved, err := m.dir(filepath.Join(a, "cache"), filepath.Join(b, "cache")); err != nil || !moved {
		t.Fatalf("into an empty folder: %v, %v", moved, err)
	}
	holds(t, filepath.Join(b, "cache"), map[string]string{"seg": "C"})
}

// otherVolume is a folder on another volume than the test's temp dir, when this
// machine has one: $MAKTABA_TEST_OTHER_VOLUME, the CI runner's temp dir
// ($RUNNER_TEMP, another drive on a Windows runner) or /dev/shm (a tmpfs). A
// rename into it must fail as between volumes.
func otherVolume(t *testing.T) string {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe")
	writeAt(t, probe, "p")
	for _, c := range []string{os.Getenv("MAKTABA_TEST_OTHER_VOLUME"), os.Getenv("RUNNER_TEMP"), "/dev/shm"} {
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err != nil || !info.IsDir() {
			continue
		}
		dir, err := os.MkdirTemp(c, "maktaba-move-")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		if err := os.Rename(probe, filepath.Join(dir, "probe")); crossDevice(err) {
			return dir
		}
		_ = os.Rename(filepath.Join(dir, "probe"), probe)
	}
	t.Skip("no other volume here (set MAKTABA_TEST_OTHER_VOLUME to a folder on another drive)")
	return ""
}

// The same moves between two real volumes.
func TestMoveAcrossRealVolumes(t *testing.T) {
	t.Parallel()
	other := otherVolume(t)
	local := t.TempDir()
	key := filepath.Join(other, "secrets.key")
	writeAt(t, key, "REAL KEY")
	if moved, err := osMover.file(key, filepath.Join(local, "keys", "secrets.key")); err != nil || !moved {
		t.Fatalf("file = %v, %v", moved, err)
	}
	if exists(key) || readAt(t, filepath.Join(local, "keys", "secrets.key")) != "REAL KEY" {
		t.Fatal("the key must be in the new place only")
	}
	files := map[string]string{"hub/w.bin": strings.Repeat("w", 100<<10), "m": "M"}
	plant(t, filepath.Join(other, "models"), files)
	if moved, err := osMover.dir(filepath.Join(other, "models"), filepath.Join(local, "models")); err != nil || !moved {
		t.Fatalf("dir = %v, %v", moved, err)
	}
	holds(t, filepath.Join(local, "models"), files)
	if exists(filepath.Join(other, "models")) || exists(filepath.Join(local, "models"+movingSuffix)) {
		t.Fatal("the old folder and the note must be gone")
	}
}
