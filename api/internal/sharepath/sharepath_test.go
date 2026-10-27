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
	if m.Len() != 4 {
		t.Fatalf("len = %d, want 4", m.Len())
	}
	for in, want := range map[string]string{
		"/Volumes/Originals/Movies/a.webm":           "O:/Movies/a.webm",
		"/volumes/ORIGINALS/Movies/a.webm":           "O:/Movies/a.webm",
		`\Volumes\Originals\Movies\a.webm`:           "O:/Movies/a.webm",
		"/Volumes/Originals":                         "O:/",
		"/Volumes/Originals/Backup/Youtube/b.mp4":    "Y:/Youtube/b.mp4",
		"/Volumes/OriginalsOld/c.mp4":                "/Volumes/OriginalsOld/c.mp4",
		"/Volumes/Empty/d.mp4":                       "/d.mp4", // an empty target strips, as in streaming
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

// An operator's MAKTABA_STREAM_PATH_MAP is read as the node's streaming role reads it
// (pathmap.Parse + Apply): on a path boundary, the rest's separators made "/", nothing
// rewritten after file:///, the longer prefix first and of two for one prefix the first.
func TestParseStreamMapAsTheStreamingRole(t *testing.T) {
	for _, c := range []struct{ spec, in, want string }{
		{"/Volumes/Media=>/mnt/media", "/Volumes/Media/a.mkv", "/mnt/media/a.mkv"},
		{"/Volumes/Media=>/mnt/media", "/Volumes/Media2/c.mkv", "/Volumes/Media2/c.mkv"},
		{`Y:\=>/Volumes/Y/`, `Y:\Shows\a.mp4`, "/Volumes/Y/Shows/a.mp4"},
		{"/Volumes/Originals=>P:", "file:///Volumes/Originals/a.mkv", "/Volumes/Originals/a.mkv"},
		{"/Volumes/O=>P:;/Volumes/O/Backup=>Y:", "/Volumes/O/Backup/b.mp4", "Y:/b.mp4"},
		{"/Volumes/O=>P:;/volumes/o=>Q:", "/Volumes/O/c.mp4", "P:/c.mp4"},
		{"smb://nas/Originals=>O:", "smb://nas/Originals/d.mkv", "O:/d.mkv"},
		{" ; nonsense ;=>X:; /Volumes/O => P: ", "/Volumes/O/e.mp4", "P:/e.mp4"},
		{"smb://nas=>/Volumes", "smb://nas/Originals/Movies/f.mkv", "/Volumes/Originals/Movies/f.mkv"},
		{"smb://nas=>//nas;smb://nas/Originals=>O:", "smb://nas/Originals/g.mkv", "O:/g.mkv"},
		{"smb://nas=>//nas;smb://nas/Originals=>O:", "smb://nas/Backup/h.mkv", "//nas/Backup/h.mkv"},
		{"/mnt/nas=>", "/mnt/nas/Movies/i.mkv", "/Movies/i.mkv"},
		{"/Volumes/O/=>;/Volumes/O=>P:", "/Volumes/O/j.mkv", "/j.mkv"},
	} {
		if got := ParseStreamMap(c.spec).ToLocal(c.in); got != c.want {
			t.Errorf("%q: ToLocal(%q) = %q, want %q", c.spec, c.in, got, c.want)
		}
	}
}

// Two canonical keys for one share: the longer wins, every time, as in the streaming role.
func TestDuplicateShareKeysPickOne(t *testing.T) {
	for i := 0; i < 50; i++ {
		m := FromNodeEntries(map[string]string{"smb://nas/Originals": "P:/", "smb://nas/Originals/": "O:/"})
		if got := m.ToLocal("smb://nas/Originals/a.mkv"); got != "O:/a.mkv" {
			t.Fatalf("run %d: ToLocal = %q, want O:/a.mkv", i, got)
		}
	}
}

// A stripped prefix (an empty target) is no mount: nothing local maps back to its share.
func TestStrippedShareIsNoMount(t *testing.T) {
	m := FromNodeEntries(map[string]string{"smb://nas/Originals/": "", "smb://nas/Backup/": "B:/"})
	if got := m.ToLocal("smb://nas/Originals/a.mkv"); got != "/a.mkv" {
		t.Errorf("ToLocal = %q, want /a.mkv", got)
	}
	if got, ok := m.ToCanonical("/Users/me/a.mkv"); ok {
		t.Errorf("ToCanonical = %q: a stripped prefix took a local disk", got)
	}
	if got, ok := m.ToCanonical("B:/x"); !ok || got != "smb://nas/Backup/x" {
		t.Errorf("ToCanonical(B:/x) = %q,%v", got, ok)
	}
}
