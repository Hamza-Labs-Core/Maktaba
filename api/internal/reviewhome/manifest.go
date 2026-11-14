// Package reviewhome seeds and maintains the store reviewers' home (Epic 58 story 58.04):
// review.maktaba.tv, a Maktaba home the project runs with public-domain and openly licensed
// content only. A store reviewer signs in to it as a viewer; a restricted child account
// beside it is there for the parental checks.
//
// Seed is idempotent: it makes the content set's libraries, the viewer and the child, their
// library access, the child's limit and its guardian, the looped live channel, and writes each
// scanned item's two titles and licence note from the manifest. Rotate gives both accounts new
// passwords and signs every one of their devices out (a release checklist step after each
// store submission round). Neither ever makes an admin, and neither reads a password from
// anywhere but its caller: the release secrets, through the environment.
package reviewhome

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
)

// Manifest is the content set (deploy/review-home/content/manifest.json). tools/review_home
// fetches and checks it; Seed reads the libraries, the channel and each item's titles and
// licence.
type Manifest struct {
	Schema    int               `json:"schema"`
	MediaRoot string            `json:"media_root"`
	Libraries []ManifestLibrary `json:"libraries"`
	Channel   ManifestChannel   `json:"channel"`
	Items     []ManifestItem    `json:"items"`
}

// ManifestLibrary is one library of the content set, in its own folder under MediaRoot.
type ManifestLibrary struct {
	ID     string `json:"id"`
	Dir    string `json:"dir"`
	NameEN string `json:"name_en"`
	NameAR string `json:"name_ar"`
	// AgeTier is the library's age tier (Epic 57): 0 all ages, 9, 13, 16 or 18.
	AgeTier int `json:"age_tier"`
}

// ManifestChannel is the live channel looped from one library.
type ManifestChannel struct {
	NameEN  string `json:"name_en"`
	NameAR  string `json:"name_ar"`
	Library string `json:"library"`
	Number  int    `json:"number"`
}

// ManifestItem is one file of the content set.
type ManifestItem struct {
	ID          string `json:"id"`
	Library     string `json:"library"`
	File        string `json:"file"`
	TitleEN     string `json:"title_en"`
	TitleAR     string `json:"title_ar"`
	Licence     string `json:"licence"`
	LicenceURL  string `json:"licence_url"`
	Attribution string `json:"attribution"`
}

// Licences are the licences the review home may carry: public domain and the attribution
// licences, never a non-commercial or no-derivatives one.
var Licences = map[string][2]string{
	"public-domain": {"Public domain", "ملكية عامة"},
	"CC0-1.0":       {"CC0 1.0 (public domain)", "CC0 1.0 (ملكية عامة)"},
	"CC-BY-3.0":     {"CC BY 3.0", "CC BY 3.0"},
	"CC-BY-4.0":     {"CC BY 4.0", "CC BY 4.0"},
}

// Tiers are the age tiers a library may have (parental.Tiers).
var tiers = map[int]bool{0: true, 9: true, 13: true, 16: true, 18: true}

// LoadManifest reads and checks a manifest file.
func LoadManifest(file string) (*Manifest, error) {
	b, err := os.ReadFile(file) //nolint:gosec // the operator's own file
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.Check(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Check is what Seed needs to hold: every library and item named once, each item in a
// library of the set, both titles, a licence of Licences, and the channel on a library.
// tools/review_home/content.py checks the rest (sources, checksums, what each file covers).
func (m *Manifest) Check() error {
	var errs []string
	if m.Schema != 1 {
		errs = append(errs, fmt.Sprintf("schema %d (want 1)", m.Schema))
	}
	if !path.IsAbs(m.MediaRoot) {
		errs = append(errs, "media_root must be an absolute path")
	}
	libs := map[string]bool{}
	for _, l := range m.Libraries {
		switch {
		case l.ID == "" || libs[l.ID]:
			errs = append(errs, fmt.Sprintf("library %q: missing or named twice", l.ID))
		case !safeName(l.Dir):
			errs = append(errs, fmt.Sprintf("library %s: dir %q is not one folder name", l.ID, l.Dir))
		case strings.TrimSpace(l.NameEN) == "" || !hasArabic(l.NameAR):
			errs = append(errs, fmt.Sprintf("library %s: needs an English and an Arabic name", l.ID))
		case !tiers[l.AgeTier]:
			errs = append(errs, fmt.Sprintf("library %s: age_tier %d", l.ID, l.AgeTier))
		}
		libs[l.ID] = true
	}
	if !libs[m.Channel.Library] {
		errs = append(errs, fmt.Sprintf("channel: library %q is not in the set", m.Channel.Library))
	}
	if strings.TrimSpace(m.Channel.NameEN) == "" || !hasArabic(m.Channel.NameAR) || m.Channel.Number <= 0 {
		errs = append(errs, "channel: needs an English and an Arabic name and a number")
	}
	items := map[string]bool{}
	for _, it := range m.Items {
		switch {
		case it.ID == "" || items[it.ID]:
			errs = append(errs, fmt.Sprintf("item %q: missing or named twice", it.ID))
		case !libs[it.Library]:
			errs = append(errs, fmt.Sprintf("item %s: library %q is not in the set", it.ID, it.Library))
		case !safeName(it.File):
			errs = append(errs, fmt.Sprintf("item %s: file %q is not one file name", it.ID, it.File))
		case strings.TrimSpace(it.TitleEN) == "" || !hasArabic(it.TitleAR):
			errs = append(errs, fmt.Sprintf("item %s: needs an English and an Arabic title", it.ID))
		case Licences[it.Licence] == [2]string{}:
			errs = append(errs, fmt.Sprintf("item %s: licence %q is not public domain or CC BY", it.ID, it.Licence))
		case strings.TrimSpace(it.Attribution) == "" || !strings.HasPrefix(it.LicenceURL, "https://"):
			errs = append(errs, fmt.Sprintf("item %s: needs an attribution and a licence URL", it.ID))
		}
		items[it.ID] = true
	}
	if len(m.Items) == 0 {
		errs = append(errs, "no items")
	}
	if len(errs) > 0 {
		return errors.New("manifest: " + strings.Join(errs, "; "))
	}
	return nil
}

// Library returns the library with that id.
func (m *Manifest) Library(id string) (ManifestLibrary, bool) {
	for _, l := range m.Libraries {
		if l.ID == id {
			return l, true
		}
	}
	return ManifestLibrary{}, false
}

// Name is a library's name as the home shows it: both languages.
func (l ManifestLibrary) Name() string { return Both(l.NameEN, l.NameAR) }

// Root is a library's folder.
func (m *Manifest) Root(l ManifestLibrary) string { return path.Join(m.MediaRoot, l.Dir) }

// Title is an item's title as the home shows it: the English and the Arabic.
func (it ManifestItem) Title() string { return Both(it.TitleEN, it.TitleAR) }

// LicenceNote is the item's description: its licence and attribution, in English and in
// Arabic (the T03 content check looks for both lines).
func (it ManifestItem) LicenceNote() string {
	l := Licences[it.Licence]
	return fmt.Sprintf("Licence: %s. %s (%s)\nالترخيص: %s. %s",
		l[0], it.Attribution, it.LicenceURL, l[1], it.Attribution)
}

// Both joins an English and an Arabic name.
func Both(en, ar string) string { return en + " · " + ar }

func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`) && strings.TrimSpace(s) == s
}

// hasArabic reports whether s has a letter of the Arabic block.
func hasArabic(s string) bool {
	for _, r := range s {
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
	}
	return false
}
