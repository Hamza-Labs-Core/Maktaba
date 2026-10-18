package pathmap

import "testing"

func TestApply(t *testing.T) {
	m := Parse(`Y:/=>/mnt/y; Y:/Shows=>/srv/shows ;bad;=>x;D:\Media\=>E:/media`)
	cases := map[string]string{
		"Y:/Shows/a.mp4":      "/srv/shows/a.mp4", // longest prefix wins
		"y:\\shows\\b c.mkv":  "/srv/shows/b c.mkv",
		"Y:/Other/c.mp4":      "/mnt/y/Other/c.mp4",
		"Y:/ShowsExtra/d.mp4": "/mnt/y/ShowsExtra/d.mp4", // boundary respected
		"D:\\Media\\e.mp4":    "E:/media/e.mp4",
		"Z:/untouched.mp4":    "Z:/untouched.mp4",
		"https://x/y.m3u8":    "https://x/y.m3u8",
		"":                    "",
	}
	for in, want := range cases {
		if got := m.Apply(in); got != want {
			t.Errorf("Apply(%q) = %q, want %q", in, got, want)
		}
	}
	if len(m.Rules()) != 3 {
		t.Errorf("rules = %d, want 3 (malformed dropped)", len(m.Rules()))
	}
}

func TestEmptyMapIsIdentity(t *testing.T) {
	if got := Parse("").Apply("Y:/a.mp4"); got != "Y:/a.mp4" {
		t.Errorf("got %q", got)
	}
}
