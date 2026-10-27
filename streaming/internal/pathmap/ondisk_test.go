package pathmap

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// A NAS over NFS keeps the composed name (أحمد); a scanner on macOS stored it decomposed
// (ا + ◌ٔ). Byte for byte that is "no such file" on a Linux or NFS disk, so every such
// video failed to play. (On macOS's own disks either spelling opens.)
func TestOnDiskFindsTheOtherUnicodeSpelling(t *testing.T) {
	root := t.TempDir()
	folder, file := "أحمد منصور", "ما هي أخطر الأمراض؟.mp4"
	if err := os.Mkdir(filepath.Join(root, folder), 0o755); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(root, folder, file)
	if err := os.WriteFile(disk, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	opens := func(p string) bool { _, err := os.Stat(p); return err == nil }
	for name, stored := range map[string]string{
		"all decomposed": norm.NFD.String(disk),
		// the folder as on disk, the file the other way (one rename on each side)
		"mixed":      filepath.Join(root, folder, norm.NFD.String(file)),
		"as on disk": disk,
	} {
		if got := OnDisk(stored); !opens(got) {
			t.Errorf("%s: OnDisk(%q) = %q, which does not open", name, stored, got)
		}
	}
	if missing := filepath.Join(root, folder, "none.mp4"); OnDisk(missing) != missing {
		t.Errorf("a file that is not there: %q", OnDisk(missing))
	}
	if url := "smb://nas/share/a.mp4"; OnDisk(url) != url {
		t.Errorf("a URL is left alone: %q", OnDisk(url))
	}
}
