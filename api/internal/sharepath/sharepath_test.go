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

// A full node's map ({"/Volumes/Originals/": "O:/"}, inferred from the home's mounts
// and this machine's) rewrites the paths the database stores, as its streaming role
// does: a Windows node that reads the share on O: plays the video itself instead of
// sending every session to the home.
func TestNodeMapRewritesStoredPrefixes(t *testing.T) {
	m := FromNodeEntries(map[string]string{
		"/Volumes/Originals/":       "O:/",
		"/Volumes/Originals/Backup": `Y:\`,
		"smb://home-nas/Originals/": "O:/",
		"/Volumes/Empty/":           " ",
	})
	if m.Len() != 3 {
		t.Fatalf("len = %d, want 3", m.Len())
	}
	for in, want := range map[string]string{
		"/Volumes/Originals/Movies/a.webm":           "O:/Movies/a.webm",
		"/volumes/ORIGINALS/Movies/a.webm":           "O:/Movies/a.webm",
		`\Volumes\Originals\Movies\a.webm`:           "O:/Movies/a.webm",
		"/Volumes/Originals":                         "O:/",
		"/Volumes/Originals/Backup/Youtube/b.mp4":    "Y:/Youtube/b.mp4",
		"/Volumes/OriginalsOld/c.mp4":                "/Volumes/OriginalsOld/c.mp4",
		"/Volumes/Empty/d.mp4":                       "/Volumes/Empty/d.mp4",
		"smb://home-nas/Originals/Movies/e.mkv":      "O:/Movies/e.mkv",
		"smb://other/share/f.mp4":                    "smb://other/share/f.mp4",
		"http://example.com/Volumes/Originals/g.mp4": "http://example.com/Volumes/Originals/g.mp4",
		"file:///C:/v/h.mp4":                         "C:/v/h.mp4",
	} {
		if got := m.ToLocal(in); got != want {
			t.Errorf("ToLocal(%q) = %q, want %q", in, got, want)
		}
	}
	// a stored prefix is not a share: nothing local maps back to it
	if got, ok := m.ToCanonical("O:/Movies/a.webm"); !ok || got != "smb://home-nas/Originals/Movies/a.webm" {
		t.Errorf("ToCanonical = %q,%v", got, ok)
	}
	if _, ok := m.ToCanonical("Y:/Youtube/b.mp4"); ok {
		t.Error("a stored prefix's mount is on no share")
	}
	// two keys for one prefix: the longer wins, as in the streaming role, every time
	for i := 0; i < 50; i++ {
		dup := FromNodeEntries(map[string]string{"/Volumes/Originals": "P:/", "/Volumes/Originals/": "O:/"})
		if got := dup.ToLocal("/Volumes/Originals/a.mkv"); got != "O:/a.mkv" {
			t.Fatalf("run %d: ToLocal = %q, want O:/a.mkv", i, got)
		}
	}
	// the home's own map keeps only shares
	if FromEntries(map[string]string{"/Volumes/Originals/": "O:/"}).ToLocal("/Volumes/Originals/a") != "/Volumes/Originals/a" {
		t.Error("the home's map rewrote a stored path")
	}
}
