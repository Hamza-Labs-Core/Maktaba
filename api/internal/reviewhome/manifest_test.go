package reviewhome

import (
	"path/filepath"
	"strings"
	"testing"
)

// The content set the repository ships passes Seed's checks (tools/review_home/content.py
// checks the rest: sources, checksums, what each file covers).
func TestTheShippedManifest(t *testing.T) {
	m, err := LoadManifest(filepath.Join("..", "..", "..", "deploy", "review-home", "content", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.MediaRoot != "/srv/review-media" {
		t.Errorf("media_root = %q: seed.sh and the compose file mount /srv/review-media", m.MediaRoot)
	}
	for _, it := range m.Items {
		if !strings.Contains(it.LicenceNote(), "Licence: ") || !strings.Contains(it.LicenceNote(), "الترخيص: ") {
			t.Errorf("%s: the licence note lacks a language: %q", it.ID, it.LicenceNote())
		}
	}
}

func TestManifestCheckRefuses(t *testing.T) {
	good := func() *Manifest {
		return &Manifest{
			Schema: 1, MediaRoot: "/srv/m",
			Libraries: []ManifestLibrary{{ID: "films", Dir: "films", NameEN: "Films", NameAR: "أفلام"}},
			Channel:   ManifestChannel{NameEN: "TV", NameAR: "قناة", Library: "films", Number: 1},
			Items: []ManifestItem{{ID: "a", Library: "films", File: "a.mp4", TitleEN: "A", TitleAR: "أ",
				Licence: "CC-BY-4.0", LicenceURL: "https://creativecommons.org/licenses/by/4.0/", Attribution: "x"}},
		}
	}
	if err := good().Check(); err != nil {
		t.Fatalf("a good manifest: %v", err)
	}
	for name, tc := range map[string]struct {
		edit func(*Manifest)
		want string
	}{
		"no Arabic title":       {func(m *Manifest) { m.Items[0].TitleAR = "A" }, "Arabic title"},
		"a non-commercial":      {func(m *Manifest) { m.Items[0].Licence = "CC-BY-NC-4.0" }, "not public domain or CC BY"},
		"a path in the file":    {func(m *Manifest) { m.Items[0].File = "../a.mp4" }, "not one file name"},
		"an unknown library":    {func(m *Manifest) { m.Items[0].Library = "x" }, "not in the set"},
		"a bad tier":            {func(m *Manifest) { m.Libraries[0].AgeTier = 12 }, "age_tier 12"},
		"a relative media root": {func(m *Manifest) { m.MediaRoot = "media" }, "absolute"},
		"the channel elsewhere": {func(m *Manifest) { m.Channel.Library = "y" }, "channel"},
		"an item twice":         {func(m *Manifest) { m.Items = append(m.Items, m.Items[0]) }, "named twice"},
	} {
		m := good()
		tc.edit(m)
		err := m.Check()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}

func TestAccountsCheck(t *testing.T) {
	ok := Accounts{Viewer: "reviewer", ViewerPassword: "a-long-viewer-pass-1", Child: "reviewer-child", ChildPassword: "a-long-child-pass-22"}
	if err := ok.Check(); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		edit func(*Accounts)
		want string
	}{
		"short":           {func(a *Accounts) { a.ViewerPassword = "short-one" }, "shorter than 16"},
		"same passwords":  {func(a *Accounts) { a.ChildPassword = a.ViewerPassword }, "different passwords"},
		"holds the name":  {func(a *Accounts) { a.ChildPassword = "Reviewer-Child-and-more" }, "holds the username"},
		"same names":      {func(a *Accounts) { a.Child = "Reviewer" }, "different usernames"},
		"no password":     {func(a *Accounts) { a.ViewerPassword = "" }, "shorter than 16"},
		"no name for one": {func(a *Accounts) { a.Child = "" }, "no username"},
	} {
		a := ok
		tc.edit(&a)
		err := a.Check()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}
