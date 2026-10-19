package libraries

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
)

type recordChecker struct{ seen []string }

func (c *recordChecker) Check(root string) error { c.seen = append(c.seen, root); return nil }

// Story 31.3: a root entered as the home's local path is stored canonical
// when it is on one of the home's shares; canonical roots survive cleaning.
func TestRootsAreStoredCanonical(t *testing.T) {
	t.Cleanup(func() { sharepath.Set(sharepath.Map{}) })
	sharepath.Set(sharepath.FromEntries(map[string]string{"smb://home-nas/Originals/": "/Volumes/Originals/"}))
	ck := &recordChecker{}
	h := &Handler{PathChecker: ck}
	got, errs := h.normaliseAndValidateRoots([]string{
		"/Volumes/Originals/Backup/Youtube",
		"smb://home-nas/Originals/Backup/Youtube", // the same root: deduplicated
		"/Users/me/Movies",
	})
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	want := []string{"smb://home-nas/Originals/Backup/Youtube", "/Users/me/Movies"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("roots = %v, want %v", got, want)
	}
	if canonicalPath("smb://home-nas/Originals/Backup/") != "smb://home-nas/Originals/Backup" {
		t.Fatal("canonicalPath folded the scheme")
	}
}

func TestCanonicalRootsOverlapCaseInsensitively(t *testing.T) {
	if !pathsOverlap("smb://nas/Originals/Backup", "SMB://nas/originals") {
		t.Fatal("nested canonical roots must overlap")
	}
	if pathsOverlap("smb://nas/Originals/Back", "smb://nas/Originals/Backup") {
		t.Fatal("a sibling prefix is not an overlap")
	}
}
