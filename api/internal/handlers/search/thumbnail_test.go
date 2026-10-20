package search

import "testing"

// A staged thumbnail must point at YouTube's image hosts and nowhere else: the
// old check took everything up to the first "/" as the host, so a query or
// fragment ending in ".ytimg.com" let any site through.
func TestSafeThumbnailURL(t *testing.T) {
	ok := []string{
		"https://i.ytimg.com/vi/abc/hqdefault.jpg",
		"https://i9.ytimg.com/vi/abc/mqdefault.jpg",
		"https://yt3.ggpht.com/ytc/AIdro_x=s88",
		"https://yt3.googleusercontent.com/ytc/AIdro_x=s176",
		"https://I.YTIMG.COM/vi/abc/0.jpg",
	}
	bad := []string{
		"https://evil.example?x.ytimg.com/vi/abc/0.jpg",
		"https://evil.example#.ytimg.com",
		"https://evil.example\\.ytimg.com/a.jpg",
		"https://i.ytimg.com@evil.example/a.jpg",
		"https://user@i.ytimg.com/a.jpg",
		"https://i.ytimg.com:8443/a.jpg",
		"https://i.ytimg.com./a.jpg.evil",
		"https://ytimg.com.evil.example/a.jpg",
		"https://lh3.googleusercontent.com/anyone-can-upload",
		"http://i.ytimg.com/vi/abc/0.jpg",
		"//i.ytimg.com/vi/abc/0.jpg",
		"javascript:alert(1)//i.ytimg.com",
		"",
	}
	for _, u := range ok {
		if !SafeThumbnailURL(u) {
			t.Errorf("rejected %q", u)
		}
	}
	for _, u := range bad {
		if SafeThumbnailURL(u) {
			t.Errorf("accepted %q", u)
		}
	}
}
