// Command release-notes prints a release's notes (release.yml), with the exact identity
// machines pin (tools/release/releasenotes).
//
//	release-notes --tag v1.5.0 --repo Hamza-Labs-Core/Maktaba [--linux-payload-missing aarch64]
//	  [--packages-url https://packages.example] [--tap-repo owner/homebrew-tap]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/tools/release/releasenotes"
)

func main() {
	tag := flag.String("tag", "", "release tag (vX.Y.Z)")
	repo := flag.String("repo", "", "repository owner/name of the signing workflow (default: the one machines pin)")
	missing := flag.String("linux-payload-missing", "", "architectures whose Linux payload is not in the release")
	packages := flag.String("packages-url", "", "the apt / dnf repositories' address (none: not mentioned)")
	tap := flag.String("tap-repo", "", "the Homebrew tap's repository, owner/homebrew-<name> (none: not mentioned)")
	flag.Parse()
	notes, err := releasenotes.Notes(*tag, *repo, *missing, releasenotes.Where{PackagesURL: *packages, TapRepo: *tap})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(notes)
}
