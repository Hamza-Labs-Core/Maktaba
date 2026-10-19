package sharepath

import "testing"

func TestToLocalAndBack(t *testing.T) {
	m := FromEntries(map[string]string{
		"smb://home-nas/Originals/":                "/Volumes/Originals",
		"smb://home-nas/Originals/Backup/Youtube/": `Y:\`,
		"/not/canonical/":                          "/x/",
	})
	if m.Len() != 2 {
		t.Fatalf("len = %d, want 2 (non-canonical keys dropped)", m.Len())
	}
	for in, want := range map[string]string{
		"smb://home-nas/Originals/Backup/Youtube/Chan/a.mp4": "Y:/Chan/a.mp4",
		"smb://HOME-NAS/originals/Movies/b.mkv":              "/Volumes/Originals/Movies/b.mkv",
		"smb://home-nas/Originals":                           "/Volumes/Originals",
		"smb://home-nas/Originals/Backup/Youtube":            "Y:/",
		"smb://other/share/c.mp4":                            "smb://other/share/c.mp4",
		"file:///Users/me/d.mp4":                             "/Users/me/d.mp4",
		"file:///C:/v/e.mp4":                                 "C:/v/e.mp4",
		"/Volumes/Originals/legacy.mp4":                      "/Volumes/Originals/legacy.mp4",
	} {
		if got := m.ToLocal(in); got != want {
			t.Errorf("ToLocal(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"/Volumes/Originals/Backup/Youtube": "smb://home-nas/Originals/Backup/Youtube",
		"/Volumes/Originals/":               "smb://home-nas/Originals",
		`y:\Chan`:                           "smb://home-nas/Originals/Backup/Youtube/Chan",
	} {
		got, ok := m.ToCanonical(in)
		if !ok || got != want {
			t.Errorf("ToCanonical(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := m.ToCanonical("/Users/me/Movies"); ok {
		t.Error("a local disk is on no share")
	}
}

func TestDefault(t *testing.T) {
	t.Cleanup(func() { Set(Map{}) })
	if ToLocal("smb://nas/O/a") != "smb://nas/O/a" {
		t.Fatal("empty default must be the identity")
	}
	Set(FromEntries(map[string]string{"smb://nas/O/": "/o/"}))
	if got := ToLocal("smb://nas/O/a"); got != "/o/a" {
		t.Fatalf("got %q", got)
	}
}
