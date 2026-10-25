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

// Notes are tag's release notes for repo ("owner/name"; "" is the repository machines
// pin by default). linuxPayloadMissing names the architectures whose Linux payload is
// not in the release ("" none).
func Notes(tag, repo, linuxPayloadMissing string) (string, error) {
	if !releaseverify.ValidTag(tag) {
		return "", fmt.Errorf("releasenotes: %q is no release tag", tag)
	}
	id, err := releaseverify.NewIdentity(repo)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	err = notesTmpl.Execute(&b, struct {
		Tag, SAN, Issuer, LinuxPayloadMissing string
	}{tag, id.SAN(tag), releaseverify.Issuer, strings.TrimSpace(linuxPayloadMissing)})
	return strings.ReplaceAll(b.String(), "\r\n", "\n"), err
}
