package pathmap

import "testing"

// Story 31.3: canonical share paths resolve through the rules, then the
// machine's default share map; file:/// is the path itself.
func TestCanonicalPaths(t *testing.T) {
	t.Cleanup(func() { SetDefault(Map{}) })
	SetDefault(FromEntries(map[string]string{
		"smb://home-nas/Originals/":                "/Volumes/Originals/",
		"smb://home-nas/Originals/Backup/Youtube/": "Y:/",
		"smb://home-nas/":                          "//192.168.50.94/",
	}))
	env := Parse(`smb://home-nas/Music/=>/srv/music`)
	cases := map[string]string{
		"smb://home-nas/Originals/Backup/Youtube/Chan/a b.mp4": "Y:/Chan/a b.mp4",
		"smb://HOME-NAS/originals/Movies/x.mkv":                "/Volumes/Originals/Movies/x.mkv",
		"smb://home-nas/Originals":                             "/Volumes/Originals",
		"smb://home-nas/Music/c.flac":                          "/srv/music/c.flac", // env rule first
		"smb://home-nas/Other/d.mp4":                           "//192.168.50.94/Other/d.mp4",
		"smb://unknown/share/e.mp4":                            "smb://unknown/share/e.mp4",
		"file:///Users/me/a.mp4":                               "/Users/me/a.mp4",
		"file:///C:/Videos/a.mp4":                              "C:/Videos/a.mp4",
		"https://x/y.m3u8":                                     "https://x/y.m3u8",
		"udp://x":                                              "udp://x",
		"/Volumes/Originals/legacy.mp4":                        "/Volumes/Originals/legacy.mp4",
	}
	for in, want := range cases {
		if got := env.Apply(in); got != want {
			t.Errorf("Apply(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Resolve("smb://home-nas/Originals/Backup/Youtube"); got != "Y:/" {
		t.Errorf("share root = %q, want Y:/", got)
	}
}

func TestMergeKeepsTheFirstOnAnEqualPrefix(t *testing.T) {
	m := Parse(`smb://nas/O/=>P:/`).Merge(FromEntries(map[string]string{"SMB://nas/O/": "O:/", "smb://nas/M/": "M:/"}))
	if got := m.Apply("smb://nas/O/a.mp4"); got != "P:/a.mp4" {
		t.Errorf("got %q", got)
	}
	if got := m.Apply("smb://nas/M/a.mp4"); got != "M:/a.mp4" {
		t.Errorf("got %q", got)
	}
}

func TestIsCanonical(t *testing.T) {
	for p, want := range map[string]bool{
		"smb://nas/Originals/a": true, "NFS://nas/export/a": true, "file:///a": true,
		"smb://nas": false, "http://x/y": false, "/a/b": false, "Y:/a": false,
	} {
		if IsCanonical(p) != want {
			t.Errorf("IsCanonical(%q) != %v", p, want)
		}
	}
}
