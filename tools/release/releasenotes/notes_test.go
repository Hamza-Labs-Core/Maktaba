package releasenotes

import (
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// TC6: the notes print the exact certificate identity machines pin for the tag (the
// release workflow at refs/tags/<tag>) and the issuer, never a regular expression.
func TestReleaseNotesPinnedIdentity(t *testing.T) {
	notes, err := Notes("v1.5.0", "", "", Where{})
	if err != nil {
		t.Fatal(err)
	}
	san := "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/v1.5.0"
	id, _ := releaseverify.NewIdentity("")
	if id.SAN("v1.5.0") != san {
		t.Fatalf("the pinned identity moved: %s", id.SAN("v1.5.0"))
	}
	for _, want := range []string{
		"--certificate-identity " + san + " \\",
		"--certificate-oidc-issuer " + releaseverify.Issuer,
		"`--certificate-identity " + san + "`",
		"Automated release for v1.5.0.",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("missing %q in\n%s", want, notes)
		}
	}
	if strings.Contains(notes, "identity-regexp") || strings.Contains(notes, "${") || strings.Contains(notes, "Not in this release") {
		t.Fatalf("a loose identity, an unfilled field or a missing payload in\n%s", notes)
	}
	// the repository the workflow runs in (a fork names its own; machines that follow it
	// pin it by MAKTABA_UPDATE_REPO)
	fork, err := Notes("v2.0.0-rc.1", "someone/maktaba", "aarch64", Where{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fork, "https://github.com/someone/maktaba/.github/workflows/release.yml@refs/tags/v2.0.0-rc.1") ||
		!strings.Contains(fork, "the Linux payload for aarch64") {
		t.Fatalf("fork notes:\n%s", fork)
	}
	if _, err := Notes("main", "", "", Where{}); err == nil {
		t.Fatal("a branch name made notes")
	}
}

// The repositories and the tap are named once configured (packages.yml publishes there),
// never before.
func TestNotesNameTheRepositoriesAndTheTap(t *testing.T) {
	plain, err := Notes("v1.5.0", "", "", Where{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "repository, which carries") || strings.Contains(plain, "brew install") {
		t.Fatalf("named before they are configured:\n%s", plain)
	}
	notes, err := Notes("v1.5.0", "", "", Where{PackagesURL: "https://packages.example", TapRepo: "Hamza-Labs-Core/homebrew-tap"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(or from the apt repository, which carries every stable release: https://packages.example)",
		"(or from the dnf repository, which carries every stable release: https://packages.example)",
		"`brew install hamza-labs-core/tap/maktaba`",
		"`brew install --cask hamza-labs-core/tap/maktaba-app`",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("missing %q in:\n%s", want, notes)
		}
	}
}
