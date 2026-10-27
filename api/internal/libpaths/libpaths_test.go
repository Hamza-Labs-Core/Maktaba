package libpaths

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
)

func TestToLocalAndToDB(t *testing.T) {
	t.Setenv(EnvPathMap, "/Volumes/Originals/=/Users/me/nas-nfs/")
	for in, want := range map[string]string{
		"/Volumes/Originals/Backup/Youtube/Chan": "/Users/me/nas-nfs/Backup/Youtube/Chan",
		"/Volumes/Originals":                     "/Users/me/nas-nfs",
		"/Volumes/Originals/":                    "/Users/me/nas-nfs/",
		"/Volumes/Other/x":                       "/Volumes/Other/x",
		"/Users/me/nas-nfs/Backup":               "/Users/me/nas-nfs/Backup",
	} {
		if got := ToLocal(in); got != want {
			t.Errorf("ToLocal(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"/Users/me/nas-nfs/Backup/Youtube": "/Volumes/Originals/Backup/Youtube",
		"/Users/me/nas-nfs":                "/Volumes/Originals",
		"/Users/me/Movies":                 "/Users/me/Movies",
	} {
		if got := ToDB(in); got != want {
			t.Errorf("ToDB(%q) = %q, want %q", in, got, want)
		}
	}
	if p := Pairs(); len(p) != 1 || p[0].DB != "/Volumes/Originals" || p[0].Local != "/Users/me/nas-nfs" {
		t.Errorf("Pairs() = %+v", p)
	}
}

// The JSON form is the one the server writes when a path holds a ";".
func TestJSONMapAndReload(t *testing.T) {
	t.Setenv(EnvPathMap, `{"/Volumes/A/": "/mnt/a/"}`)
	if got := ToLocal("/Volumes/A/x"); got != "/mnt/a/x" {
		t.Errorf("json map: %q", got)
	}
	t.Setenv(EnvPathMap, "")
	if got := ToLocal("/Volumes/A/x"); got != "/Volumes/A/x" {
		t.Errorf("an emptied map still maps: %q", got)
	}
	t.Setenv(EnvPathMap, "{not json")
	if got := ToLocal("/Volumes/A/x"); got != "/Volumes/A/x" {
		t.Errorf("a bad map maps: %q", got)
	}
}

// A canonical share path resolves through the home's share map first.
func TestCanonicalFirst(t *testing.T) {
	prev := sharepath.Default()
	t.Cleanup(func() { sharepath.Set(prev) })
	sharepath.Set(sharepath.FromEntries(map[string]string{"smb://nas/Originals/": "/Volumes/Originals/"}))
	t.Setenv(EnvPathMap, "/Volumes/Originals/=/Users/me/nas-nfs/")
	if got := ToLocal("smb://nas/Originals/Backup"); got != "/Volumes/Originals/Backup" {
		t.Errorf("canonical: %q", got)
	}
}
