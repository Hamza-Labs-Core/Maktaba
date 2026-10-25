// Package releasenotes writes a release's notes (release.yml): how to install it and how
// to verify it by hand, with the exact identity every Maktaba machine pins
// (shared/go/releaseverify: the release workflow of the repository, for exactly this
// tag), so a person checks what the updater checks (story 35.15 AC7, plan D4).
package releasenotes

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

//go:embed notes.md.tmpl
var notesText string

var notesTmpl = template.Must(template.New("notes").Parse(notesText))

// Where says where a release is published besides its own page (packages.yml), each ""
// when not configured.
type Where struct {
	// PackagesURL is the apt / dnf repositories' address (MAKTABA_PACKAGES_URL).
	PackagesURL string
	// TapRepo is the Homebrew tap's repository, "owner/homebrew-<name>" (HOMEBREW_TAP_REPO).
	TapRepo string
}

// tap is TapRepo as brew names it ("owner/homebrew-tap" is "owner/tap").
func (w Where) tap() string {
	owner, name, ok := strings.Cut(strings.TrimSpace(w.TapRepo), "/")
	if !ok || owner == "" || name == "" {
		return ""
	}
	return strings.ToLower(owner + "/" + strings.TrimPrefix(name, "homebrew-"))
}

// Notes are tag's release notes for repo ("owner/name"; "" is the repository machines
// pin by default). linuxPayloadMissing names the architectures whose Linux payload is
// not in the release ("" none); where, the repositories and the tap.
func Notes(tag, repo, linuxPayloadMissing string, where Where) (string, error) {
	if !releaseverify.ValidTag(tag) {
		return "", fmt.Errorf("releasenotes: %q is no release tag", tag)
	}
	id, err := releaseverify.NewIdentity(repo)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	err = notesTmpl.Execute(&b, struct {
		Tag, SAN, Issuer, LinuxPayloadMissing, PackagesURL, Tap string
	}{tag, id.SAN(tag), releaseverify.Issuer, strings.TrimSpace(linuxPayloadMissing),
		strings.TrimSpace(where.PackagesURL), where.tap()})
	return strings.ReplaceAll(b.String(), "\r\n", "\n"), err
}
