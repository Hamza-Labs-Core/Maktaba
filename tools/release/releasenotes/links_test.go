package releasenotes

import (
	"regexp"
	"strings"
	"testing"
)

// maktabaLink is a maktaba.tv link as web/packages/website/scripts/links.mjs finds one: the
// apex or www with a path, with or without a scheme, not a subdomain or a mail address.
var maktabaLink = regexp.MustCompile(`(?:^|[^A-Za-z0-9._@/-])((?:https?://)?(?:www\.)?maktaba\.tv/[A-Za-z0-9_./#?=%~+-]*)`)

func maktabaLinks(text string) []string {
	var out []string
	for _, m := range maktabaLink.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.TrimRight(m[1], ".,"))
	}
	return out
}

// Story 60.08 T09: the notes rendered with every optional line on (the package repositories,
// the tap, a missing Linux payload) print only maktaba.tv links that the template holds as
// literals. The link check (links.mjs) reads the template's text, so every link a release can
// print is checked there; a link built from a value at render time would escape it.
func TestNotesLinksAreTheTemplatesLiterals(t *testing.T) {
	notes, err := Notes("v1.5.0", "", "arm64", Where{PackagesURL: "https://packages.example", TapRepo: "Hamza-Labs-Core/homebrew-tap"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"apt repository", "brew install --cask", "Not in this release:** the Linux payload for arm64"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("an optional line is off (%q missing):\n%s", want, notes)
		}
	}
	links := maktabaLinks(notes)
	if len(links) < 2 {
		t.Fatalf("expected the install guides' links in the notes, found %v:\n%s", links, notes)
	}
	literal := map[string]bool{}
	for _, l := range maktabaLinks(notesText) {
		literal[l] = true
	}
	for _, l := range links {
		if !literal[l] {
			t.Errorf("the rendered notes link %s, which is not a literal of notes.md.tmpl: links.mjs cannot check it", l)
		}
	}
}

func TestMaktabaLinksFindsWhatLinksMjsFinds(t *testing.T) {
	got := maktabaLinks("api.maktaba.tv/v1 privacy@maktaba.tv maktaba.tv is ours\nsee maktaba.tv/install/nas. Or https://maktaba.tv/install/docker/, done")
	want := []string{"maktaba.tv/install/nas", "https://maktaba.tv/install/docker/"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
}
