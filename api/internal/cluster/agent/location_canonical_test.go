package agent

import "testing"

// Story 31.3: canonical roots keep their server and share as the root.
func TestShareRootOfCanonicalRoots(t *testing.T) {
	got := ShareRootOf([]LibraryRoot{
		{Path: "smb://home-nas/Originals/Backup/Youtube"},
		{Path: "smb://home-nas/Originals/Backup/Talks"},
	})
	if got != "smb://home-nas/Originals/Backup" {
		t.Fatalf("got %q", got)
	}
}
